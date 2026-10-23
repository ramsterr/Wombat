package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SearchResult is one web search hit. Published, Author, and Excerpt are
// filled when a backend or the deep page pass provides them.
type SearchResult struct {
	Title     string
	URL       string
	Snippet   string
	Published string
	Author    string
	Excerpt   string

	engine string
	rank   int
}

type searchEngine struct {
	name  string
	query func(ctx context.Context, req searchRequest) ([]SearchResult, error)
}

// searchRequest is one query with the filters a backend can honour.
type searchRequest struct {
	Text      string
	Limit     int
	Page      int
	Freshness string
	Language  string
	Region    string
	Depth     string
}

// searchOptions controls one searchWeb call.
type searchOptions struct {
	Queries   []string
	Limit     int
	Page      int
	Freshness string
	Language  string
	Region    string
	Depth     string
}

// webHTTP is shared by search and fetch so connections are reused across
// turns and parallel workers.
var webHTTP = &http.Client{Timeout: 15 * time.Second}

const (
	searchCollectTimeout = 15 * time.Second
	searchGrace          = 5 * time.Second
	searchEngineTTL      = 10 * time.Second
	searchRetryDelay     = 300 * time.Millisecond
	searchCacheTTL       = 10 * time.Minute
	searchCacheMax       = 256
)

type searchCacheEntry struct {
	results []SearchResult
	expires time.Time
}

var searchCache = struct {
	sync.Mutex
	entries map[string]searchCacheEntry
}{entries: map[string]searchCacheEntry{}}

var (
	bingRSSURL   = "https://www.bing.com/search?format=rss&q="
	ddgHTMLURL   = "https://html.duckduckgo.com/html/?q="
	mojeekRSSURL = "https://www.mojeek.com/search?fmt=rss&q="
	braveAPIURL  = "https://api.search.brave.com/res/v1/web/search?q="
	exaMCPURL    = "https://mcp.exa.ai/mcp"
	tavilyAPIURL = "https://api.tavily.com/search"
	serperAPIURL = "https://google.serper.dev/search"
)

// searchWeb runs every enabled provider for every query in parallel, merges
// and ranks the results, enriches the top hits with page content, and reports
// which backends failed instead of hiding partial failures.
func searchWeb(ctx context.Context, opts searchOptions) ([]SearchResult, []string, error) {
	opts = normalizeSearchOptions(opts)
	queries := normalizeQueries(opts.Queries)
	if len(queries) == 0 {
		return nil, nil, fmt.Errorf("query is required")
	}
	key := searchCacheKey(queries, opts)
	if cached := cachedSearch(key); cached != nil {
		return cached, nil, nil
	}

	engines := configuredSearchEngines()
	if len(engines) == 0 {
		return nil, nil, fmt.Errorf("no search backends are configured")
	}
	states := make(map[string]*engineState, len(engines))
	for _, engine := range engines {
		states[engine.name] = &engineState{}
	}

	searchCtx, cancel := context.WithTimeout(ctx, searchCollectTimeout)
	defer cancel()
	ch := make(chan searchBatch, len(queries)*len(engines))
	remaining := 0
	for _, query := range queries {
		for _, engine := range engines {
			engine, query := engine, query
			remaining++
			go func() {
				engineCtx, engineCancel := context.WithTimeout(searchCtx, searchEngineTTL)
				defer engineCancel()
				results, err := engine.query(engineCtx, searchRequest{
					Text:      query,
					Limit:     opts.Limit,
					Page:      opts.Page,
					Freshness: opts.Freshness,
					Language:  opts.Language,
					Region:    opts.Region,
					Depth:     opts.Depth,
				})
				for i := range results {
					results[i].engine = engine.name
					results[i].rank = i + 1
				}
				ch <- searchBatch{engine: engine.name, results: results, err: err}
			}()
		}
	}

	var batches []searchBatch
	var graceC <-chan time.Time
	var graceTimer *time.Timer
	deadline := time.NewTimer(searchCollectTimeout)
	defer deadline.Stop()
	for remaining > 0 {
		select {
		case batch := <-ch:
			remaining--
			state := states[batch.engine]
			if batch.err != nil && state.err == nil {
				state.err = batch.err
			}
			if len(batch.results) == 0 {
				continue
			}
			state.ok = true
			batches = append(batches, batch)
			if graceC == nil {
				graceTimer = time.NewTimer(searchGrace)
				graceC = graceTimer.C
			}
		case <-graceC:
			remaining = 0
		case <-deadline.C:
			remaining = 0
		case <-ctx.Done():
			remaining = 0
		}
	}
	if graceTimer != nil {
		graceTimer.Stop()
	}
	cancel()
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}

	warnings := searchWarnings(states)
	if len(batches) == 0 {
		if len(warnings) > 0 {
			return nil, warnings, fmt.Errorf("search failed: all backends failed (%s)", strings.Join(warnings, "; "))
		}
		return nil, nil, nil
	}
	ranked := rankSearchResultsWithOptions(strings.Join(queries, " "), batches, opts.Limit, rankOptions{
		Freshness:  opts.Freshness,
		MaxPerHost: 3,
	})
	if len(ranked) == 0 {
		return nil, warnings, nil
	}
	ranked = enrichSearchResults(ctx, ranked, strings.Join(queries, " "), opts.Depth)
	return cacheSearch(key, ranked), warnings, nil
}

type engineState struct {
	ok  bool
	err error
}

func configuredSearchEngines() []searchEngine {
	engines := []searchEngine{}
	if key := strings.TrimSpace(os.Getenv("BRAVE_API_KEY")); key != "" {
		engines = append(engines, searchEngine{name: "brave", query: func(ctx context.Context, req searchRequest) ([]SearchResult, error) {
			return searchBrave(ctx, key, req)
		}})
	}
	if key := strings.TrimSpace(os.Getenv("SERPER_API_KEY")); key != "" {
		engines = append(engines, searchEngine{name: "serper", query: func(ctx context.Context, req searchRequest) ([]SearchResult, error) {
			return searchSerper(ctx, key, req)
		}})
	}
	if tavilyAPIURL != "" {
		engines = append(engines, searchEngine{name: "tavily", query: func(ctx context.Context, req searchRequest) ([]SearchResult, error) {
			return searchTavily(ctx, os.Getenv("TAVILY_API_KEY"), req)
		}})
	}
	if exaMCPURL != "" {
		engines = append(engines, searchEngine{name: "exa", query: func(ctx context.Context, req searchRequest) ([]SearchResult, error) {
			return searchExa(ctx, os.Getenv("EXA_API_KEY"), req)
		}})
	}
	if bingRSSURL != "" {
		engines = append(engines, searchEngine{name: "bing", query: searchBingRSS})
	}
	if ddgHTMLURL != "" {
		engines = append(engines, searchEngine{name: "duckduckgo", query: searchDuckDuckGo})
	}
	if mojeekRSSURL != "" {
		engines = append(engines, searchEngine{name: "mojeek", query: searchMojeekRSS})
	}
	return engines
}

func normalizeSearchOptions(opts searchOptions) searchOptions {
	if opts.Limit <= 0 {
		opts.Limit = 8
	}
	if opts.Limit > 20 {
		opts.Limit = 20
	}
	if opts.Page <= 0 {
		opts.Page = 1
	}
	if opts.Page > 5 {
		opts.Page = 5
	}
	opts.Freshness = strings.ToLower(strings.TrimSpace(opts.Freshness))
	opts.Language = strings.ToLower(strings.TrimSpace(opts.Language))
	opts.Region = strings.ToLower(strings.TrimSpace(opts.Region))
	opts.Depth = strings.ToLower(strings.TrimSpace(opts.Depth))
	if opts.Depth == "" {
		opts.Depth = "auto"
	}
	return opts
}

func normalizeQueries(queries []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(queries))
	for _, query := range queries {
		query = strings.TrimSpace(query)
		if query == "" {
			continue
		}
		key := strings.ToLower(query)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, query)
		if len(out) == 4 {
			break
		}
	}
	return out
}

func searchCacheKey(queries []string, opts searchOptions) string {
	return strings.Join([]string{
		strings.ToLower(strings.Join(queries, "\x00")),
		strconv.Itoa(opts.Limit),
		strconv.Itoa(opts.Page),
		opts.Freshness,
		opts.Language,
		opts.Region,
		opts.Depth,
	}, "|")
}

func searchWarnings(states map[string]*engineState) []string {
	names := make([]string, 0, len(states))
	for name := range states {
		names = append(names, name)
	}
	sort.Strings(names)
	var warnings []string
	for _, name := range names {
		state := states[name]
		if state.ok || state.err == nil {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("%s (%s)", name, shortSearchError(state.err)))
	}
	return warnings
}

func shortSearchError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	runes := []rune(msg)
	if len(runes) > 120 {
		msg = string(runes[:119]) + "…"
	}
	return msg
}

func cachedSearch(key string) []SearchResult {
	searchCache.Lock()
	defer searchCache.Unlock()
	entry, ok := searchCache.entries[key]
	if !ok || time.Now().After(entry.expires) {
		delete(searchCache.entries, key)
		return nil
	}
	out := make([]SearchResult, len(entry.results))
	copy(out, entry.results)
	return out
}

func cacheSearch(key string, results []SearchResult) []SearchResult {
	searchCache.Lock()
	defer searchCache.Unlock()
	if len(searchCache.entries) >= searchCacheMax {
		for k, v := range searchCache.entries {
			if time.Now().After(v.expires) {
				delete(searchCache.entries, k)
			}
		}
		if len(searchCache.entries) >= searchCacheMax {
			for k := range searchCache.entries {
				delete(searchCache.entries, k)
				break
			}
		}
	}
	searchCache.entries[key] = searchCacheEntry{results: results, expires: time.Now().Add(searchCacheTTL)}
	out := make([]SearchResult, len(results))
	copy(out, results)
	return out
}

func clearSearchCache() {
	searchCache.Lock()
	searchCache.entries = map[string]searchCacheEntry{}
	searchCache.Unlock()
}

func canonicalResultURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	u.Fragment = ""
	q := u.Query()
	for key := range q {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "gclid" || lower == "fbclid" {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String()
}

func withQuery(base string, values map[string]string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	for key, value := range values {
		if value != "" {
			q.Set(key, value)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func freshnessCode(freshness string) string {
	switch strings.ToLower(freshness) {
	case "day":
		return "pd"
	case "week":
		return "pw"
	case "month":
		return "pm"
	case "year":
		return "py"
	}
	return ""
}

func freshnessLetter(freshness string) string {
	switch strings.ToLower(freshness) {
	case "day":
		return "d"
	case "week":
		return "w"
	case "month":
		return "m"
	case "year":
		return "y"
	}
	return ""
}

func freshnessStart(freshness string) string {
	days := 0
	switch strings.ToLower(freshness) {
	case "day":
		days = 1
	case "week":
		days = 7
	case "month":
		days = 30
	case "year":
		days = 365
	}
	if days == 0 {
		return ""
	}
	return time.Now().AddDate(0, 0, -days).Format(time.RFC3339)
}

// doWebRequest retries transient network failures, 429s, and 5xx responses.
func doWebRequest(ctx context.Context, build func() (*http.Request, error)) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		req, err := build()
		if err != nil {
			return nil, err
		}
		resp, err := webHTTP.Do(req)
		if err == nil {
			if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
				lastErr = fmt.Errorf("http %d", resp.StatusCode)
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
				_ = resp.Body.Close()
				if attempt == 0 {
					if !retryWait(ctx) {
						return nil, ctx.Err()
					}
					continue
				}
				return nil, lastErr
			}
			return resp, nil
		}
		lastErr = err
		if attempt == 0 && transientWebError(err) {
			if !retryWait(ctx) {
				return nil, ctx.Err()
			}
			continue
		}
		return nil, err
	}
	return nil, lastErr
}

func retryWait(ctx context.Context) bool {
	select {
	case <-time.After(searchRetryDelay):
		return true
	case <-ctx.Done():
		return false
	}
}

func newRequest(ctx context.Context, method, target string, headers map[string]string, payload []byte) (*http.Request, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	return req, nil
}

func searchJSON(ctx context.Context, method, target string, headers map[string]string, payload []byte, result any) error {
	resp, err := doWebRequest(ctx, func() (*http.Request, error) {
		return newRequest(ctx, method, target, headers, payload)
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("search API: %s", resp.Status)
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("search API: decode: %w", err)
	}
	return nil
}

// webJSON decodes a search API response for tools that build their own
// request, such as image search.
func webJSON(req *http.Request, result any) error {
	resp, err := webHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("search API: %s", resp.Status)
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("search API: decode: %w", err)
	}
	return nil
}

func searchBingRSS(ctx context.Context, req searchRequest) ([]SearchResult, error) {
	params := map[string]string{"q": req.Text}
	if req.Page > 1 {
		params["first"] = strconv.Itoa((req.Page-1)*req.Limit + 1)
	}
	if req.Language != "" {
		params["setlang"] = req.Language
	}
	if req.Region != "" {
		params["cc"] = strings.ToUpper(req.Region)
	}
	page, err := fetchSearchPage(ctx, withQuery(bingRSSURL, params))
	if err != nil {
		return nil, err
	}
	return parseRSSResults(page, req.Limit)
}

func searchMojeekRSS(ctx context.Context, req searchRequest) ([]SearchResult, error) {
	params := map[string]string{"q": req.Text}
	if req.Page > 1 {
		params["s"] = strconv.Itoa((req.Page - 1) * req.Limit)
	}
	page, err := fetchSearchPage(ctx, withQuery(mojeekRSSURL, params))
	if err != nil {
		return nil, err
	}
	return parseRSSResults(page, req.Limit)
}

func parseRSSResults(page string, limit int) ([]SearchResult, error) {
	var feed struct {
		Channel struct {
			Items []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				Description string `xml:"description"`
				PubDate     string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal([]byte(page), &feed); err != nil {
		return nil, fmt.Errorf("decode rss: %w", err)
	}
	out := make([]SearchResult, 0, limit)
	for _, item := range feed.Channel.Items {
		out = append(out, SearchResult{
			Title:     stripTags(item.Title),
			URL:       strings.TrimSpace(item.Link),
			Snippet:   stripTags(item.Description),
			Published: normalizeDate(item.PubDate),
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// searchDuckDuckGo queries DuckDuckGo's HTML endpoint and parses the results.
func searchDuckDuckGo(ctx context.Context, req searchRequest) ([]SearchResult, error) {
	params := map[string]string{"q": req.Text}
	if req.Page > 1 {
		params["s"] = strconv.Itoa((req.Page - 1) * req.Limit)
	}
	if req.Region != "" && req.Language != "" {
		params["kl"] = req.Region + "-" + req.Language
	}
	if code := freshnessLetter(req.Freshness); code != "" {
		params["df"] = code
	}
	page, err := fetchSearchPage(ctx, withQuery(ddgHTMLURL, params))
	if err != nil {
		return nil, err
	}
	matches := ddgResultRe.FindAllStringSubmatch(page, -1)
	snippets := ddgSnipRe.FindAllStringSubmatch(page, -1)
	if len(matches) == 0 && !strings.Contains(strings.ToLower(page), "no results") {
		return nil, fmt.Errorf("duckduckgo: unexpected response")
	}
	out := make([]SearchResult, 0, req.Limit)
	for i, m := range matches {
		if len(out) >= req.Limit {
			break
		}
		link, title := m[1], stripTags(m[2])
		if r := ddgRedirect.FindStringSubmatch(link); r != nil {
			if decoded, err := url.QueryUnescape(r[1]); err == nil {
				link = decoded
			}
		}
		snippet := ""
		if i < len(snippets) && len(snippets[i]) > 1 {
			snippet = stripTags(snippets[i][1])
		}
		out = append(out, SearchResult{Title: title, URL: link, Snippet: snippet})
	}
	return out, nil
}

func searchBrave(ctx context.Context, key string, req searchRequest) ([]SearchResult, error) {
	params := map[string]string{
		"q":          req.Text,
		"count":      strconv.Itoa(req.Limit),
		"safesearch": "strict",
	}
	if req.Page > 1 {
		params["offset"] = strconv.Itoa(req.Page - 1)
	}
	if req.Language != "" {
		params["search_lang"] = req.Language
	}
	if req.Region != "" {
		params["country"] = strings.ToUpper(req.Region)
	}
	if code := freshnessCode(req.Freshness); code != "" {
		params["freshness"] = code
	}
	var body struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
				PageAge     string `json:"page_age"`
			} `json:"results"`
		} `json:"web"`
	}
	err := searchJSON(ctx, http.MethodGet, withQuery(braveAPIURL, params), map[string]string{
		"Accept":               "application/json",
		"X-Subscription-Token": key,
	}, nil, &body)
	if err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(body.Web.Results))
	for _, r := range body.Web.Results {
		out = append(out, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Description, Published: normalizeDate(r.PageAge)})
	}
	return out, nil
}

func searchTavily(ctx context.Context, key string, req searchRequest) ([]SearchResult, error) {
	payload := map[string]any{
		"query": req.Text, "search_depth": "basic", "chunks_per_source": 3, "max_results": req.Limit,
	}
	if req.Depth == "deep" {
		payload["search_depth"] = "advanced"
	}
	if req.Freshness != "" {
		payload["time_range"] = req.Freshness
	}
	data, _ := json.Marshal(payload)
	headers := map[string]string{"Content-Type": "application/json"}
	if key != "" {
		headers["Authorization"] = "Bearer " + key
	} else {
		headers["X-Tavily-Access-Mode"] = "keyless"
	}
	var body struct {
		Results []struct {
			Title         string `json:"title"`
			URL           string `json:"url"`
			Content       string `json:"content"`
			PublishedDate string `json:"published_date"`
		} `json:"results"`
	}
	if err := searchJSON(ctx, http.MethodPost, tavilyAPIURL, headers, data, &body); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(body.Results))
	for _, r := range body.Results {
		out = append(out, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Content, Published: normalizeDate(r.PublishedDate)})
	}
	return out, nil
}

func searchExa(ctx context.Context, key string, req searchRequest) ([]SearchResult, error) {
	endpoint := exaMCPURL
	if key != "" {
		endpoint += "?exaApiKey=" + url.QueryEscape(key)
	}
	numResults := req.Limit * 2
	if numResults < 5 {
		numResults = 5
	}
	if numResults > 20 {
		numResults = 20
	}
	args := map[string]any{"query": req.Text, "numResults": numResults}
	if start := freshnessStart(req.Freshness); start != "" {
		args["startPublishedDate"] = start
	}
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "web_search_exa",
			"arguments": args,
		},
	})
	resp, err := doWebRequest(ctx, func() (*http.Request, error) {
		return newRequest(ctx, http.MethodPost, endpoint, map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json, text/event-stream",
		}, payload)
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("exa: %s", resp.Status)
	}
	var result struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := decodeMCPJSON(data, &result); err != nil {
		return nil, err
	}
	for _, item := range result.Result.Content {
		if item.Type == "text" && item.Text != "" {
			return parseExaText(item.Text, numResults), nil
		}
	}
	return nil, fmt.Errorf("exa: empty response")
}

func decodeMCPJSON(data []byte, result any) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") {
		return json.Unmarshal([]byte(trimmed), result)
	}
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), result); err == nil {
			return nil
		}
	}
	return fmt.Errorf("exa: no JSON response")
}

func parseExaText(text string, limit int) []SearchResult {
	blocks := strings.Split(text, "\n\n---\n\n")
	out := make([]SearchResult, 0, len(blocks))
	for _, block := range blocks {
		url := fieldLine(block, "URL:")
		if url == "" || len(out) >= limit {
			continue
		}
		title := fieldLine(block, "Title:")
		content := fieldBlock(block, "Highlights:")
		if content == "" {
			content = fieldBlock(block, "Text:")
		}
		out = append(out, SearchResult{
			Title:     title,
			URL:       url,
			Snippet:   strings.TrimSpace(content),
			Author:    cleanMetaValue(fieldLine(block, "Author:")),
			Published: normalizeDate(fieldLine(block, "Published:")),
		})
	}
	return out
}

func fieldLine(block, prefix string) string {
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func fieldBlock(block, prefix string) string {
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if rest := strings.TrimSpace(strings.TrimPrefix(line, prefix)); rest != "" {
			return rest
		}
		var parts []string
		for _, next := range lines[i+1:] {
			next = strings.TrimSpace(next)
			if next == "" {
				continue
			}
			if isExaField(next) {
				break
			}
			parts = append(parts, next)
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func isExaField(line string) bool {
	for _, prefix := range []string{"Title:", "URL:", "Published:", "Author:", "Highlights:", "Text:"} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func searchSerper(ctx context.Context, key string, req searchRequest) ([]SearchResult, error) {
	payload := map[string]any{"q": req.Text, "num": req.Limit, "safe": "active"}
	if req.Page > 1 {
		payload["page"] = req.Page
	}
	if req.Region != "" {
		payload["gl"] = strings.ToUpper(req.Region)
	}
	if req.Language != "" {
		payload["hl"] = req.Language
	}
	if code := freshnessLetter(req.Freshness); code != "" {
		payload["tbs"] = "qdr:" + code
	}
	data, _ := json.Marshal(payload)
	var body struct {
		Organic []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
			Date    string `json:"date"`
		} `json:"organic"`
	}
	if err := searchJSON(ctx, http.MethodPost, serperAPIURL, map[string]string{
		"Content-Type": "application/json",
		"X-API-KEY":    key,
	}, data, &body); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(body.Organic))
	for _, r := range body.Organic {
		out = append(out, SearchResult{Title: r.Title, URL: r.Link, Snippet: r.Snippet, Published: normalizeDate(r.Date)})
	}
	return out, nil
}

// fetchSearchPage gets a search engine page, retrying transient failures.
func fetchSearchPage(ctx context.Context, endpoint string) (string, error) {
	resp, err := doWebRequest(ctx, func() (*http.Request, error) {
		return newRequest(ctx, http.MethodGet, endpoint, map[string]string{
			"User-Agent":      browserUA,
			"Accept":          "application/rss+xml,text/html,application/xhtml+xml",
			"Accept-Language": "en-US,en;q=0.9",
		}, nil)
	})
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("search failed: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}
	return string(body), nil
}

var (
	ddgResultRe = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgSnipRe   = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>`)
	ddgRedirect = regexp.MustCompile(`uddg=([^&]+)`)
)

// browserUA is a plain browser user agent: search engines block obvious bots.
const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// stripTags removes HTML tags and entities from a string.
func stripTags(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = unescapeEntities(s)
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

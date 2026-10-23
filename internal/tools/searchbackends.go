package tools

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// SearchResult is one web search hit.
type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

type searchEngine struct {
	name  string
	query func(ctx context.Context, query string, limit int) ([]SearchResult, error)
}

// webHTTP is shared by search and fetch so connections are reused across
// turns and parallel workers.
var webHTTP = &http.Client{Timeout: 12 * time.Second}

const (
	searchCollectTimeout = 2200 * time.Millisecond
	searchGrace          = 900 * time.Millisecond
	searchEngineTTL      = 2 * time.Second
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
	braveAPIURL  = "https://api.search.brave.com/res/v1/web/search?q="
	exaMCPURL    = "https://mcp.exa.ai/mcp"
	tavilyAPIURL = "https://api.tavily.com/search"
	serperAPIURL = "https://google.serper.dev/search"
)

// searchWeb runs the providers in parallel, merges and ranks their results,
// and drops poisoned/unsafe hits. A short grace window keeps latency low
// without accepting the first (often weakest) backend.
func searchWeb(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if limit <= 0 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}
	key := strings.ToLower(query) + "|" + fmt.Sprint(limit)
	if cached := cachedSearch(key); cached != nil {
		return cached, nil
	}

	engines := []searchEngine{}
	if key := strings.TrimSpace(os.Getenv("BRAVE_API_KEY")); key != "" {
		engines = append(engines, searchEngine{name: "brave", query: func(ctx context.Context, q string, n int) ([]SearchResult, error) {
			return searchBrave(ctx, key, q, n)
		}})
	}
	if key := strings.TrimSpace(os.Getenv("SERPER_API_KEY")); key != "" {
		engines = append(engines, searchEngine{name: "serper", query: func(ctx context.Context, q string, n int) ([]SearchResult, error) {
			return searchSerper(ctx, key, q, n)
		}})
	}
	if tavilyAPIURL != "" {
		engines = append(engines, searchEngine{name: "tavily", query: func(ctx context.Context, q string, n int) ([]SearchResult, error) {
			return searchTavily(ctx, os.Getenv("TAVILY_API_KEY"), q, n)
		}})
	}
	if exaMCPURL != "" {
		engines = append(engines, searchEngine{name: "exa", query: func(ctx context.Context, q string, n int) ([]SearchResult, error) {
			return searchExa(ctx, os.Getenv("EXA_API_KEY"), q, n)
		}})
	}
	engines = append(engines,
		searchEngine{name: "bing", query: searchBingRSS},
		searchEngine{name: "duckduckgo", query: searchDuckDuckGo},
	)

	searchCtx, cancel := context.WithTimeout(ctx, searchCollectTimeout)
	defer cancel()
	ch := make(chan searchBatch, len(engines))
	for _, engine := range engines {
		engine := engine
		go func() {
			engineCtx, engineCancel := context.WithTimeout(searchCtx, searchEngineTTL)
			defer engineCancel()
			results, err := engine.query(engineCtx, query, limit)
			ch <- searchBatch{engine: engine.name, results: results, err: err}
		}()
	}

	var batches []searchBatch
	var lastErr error
	remaining := len(engines)
	var graceC <-chan time.Time
	var graceTimer *time.Timer
	deadline := time.NewTimer(searchCollectTimeout)
	defer deadline.Stop()
	for remaining > 0 {
		select {
		case batch := <-ch:
			remaining--
			if batch.err != nil {
				lastErr = batch.err
			}
			if len(batch.results) == 0 {
				continue
			}
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
	if len(batches) == 0 {
		if lastErr != nil {
			return nil, fmt.Errorf("search failed: %w", lastErr)
		}
		return nil, nil
	}
	ranked := rankSearchResults(query, batches, limit)
	if len(ranked) == 0 {
		return nil, nil
	}
	return cacheSearch(key, ranked), nil
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

// searchBingRSS asks Bing for its RSS feed. It is much smaller and more stable
// than scraping the HTML result page.
func searchBingRSS(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	page, err := fetchSearchPage(ctx, bingRSSURL+url.QueryEscape(query))
	if err != nil {
		return nil, err
	}
	var feed struct {
		Channel struct {
			Items []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal([]byte(page), &feed); err != nil {
		return nil, fmt.Errorf("decode bing rss: %w", err)
	}
	out := make([]SearchResult, 0, limit)
	for _, item := range feed.Channel.Items {
		out = append(out, SearchResult{
			Title:   stripTags(item.Title),
			URL:     strings.TrimSpace(item.Link),
			Snippet: stripTags(item.Description),
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// searchDuckDuckGo queries DuckDuckGo's HTML endpoint and parses the results.
func searchDuckDuckGo(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	page, err := fetchSearchPage(ctx, ddgHTMLURL+url.QueryEscape(query))
	if err != nil {
		return nil, err
	}
	matches := ddgResultRe.FindAllStringSubmatch(page, -1)
	snippets := ddgSnipRe.FindAllStringSubmatch(page, -1)
	out := make([]SearchResult, 0, limit)
	for i, m := range matches {
		if len(out) >= limit {
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

func searchBrave(ctx context.Context, key, query string, limit int) ([]SearchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, braveAPIURL+url.QueryEscape(query)+"&count="+fmt.Sprint(limit)+"&safesearch=strict", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", key)
	var body struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := webJSON(req, &body); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(body.Web.Results))
	for _, r := range body.Web.Results {
		out = append(out, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Description})
	}
	return out, nil
}

func searchTavily(ctx context.Context, key, query string, limit int) ([]SearchResult, error) {
	payload, _ := json.Marshal(map[string]any{
		"query": query, "search_depth": "basic", "chunks_per_source": 3, "max_results": limit,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tavilyAPIURL, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	} else {
		req.Header.Set("X-Tavily-Access-Mode", "keyless")
	}
	var body struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := webJSON(req, &body); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(body.Results))
	for _, r := range body.Results {
		out = append(out, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return out, nil
}

func searchExa(ctx context.Context, key, query string, limit int) ([]SearchResult, error) {
	endpoint := exaMCPURL
	if key != "" {
		endpoint += "?exaApiKey=" + url.QueryEscape(key)
	}
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "web_search_exa",
			"arguments": map[string]any{"query": query, "numResults": limit},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := webHTTP.Do(req)
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
			return parseExaText(item.Text, limit), nil
		}
	}
	return nil, nil
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
		out = append(out, SearchResult{Title: title, URL: url, Snippet: strings.TrimSpace(content)})
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

func searchSerper(ctx context.Context, key, query string, limit int) ([]SearchResult, error) {
	payload, _ := json.Marshal(map[string]any{"q": query, "num": limit, "safe": "active"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serperAPIURL, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-KEY", key)
	var body struct {
		Organic []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic"`
	}
	if err := webJSON(req, &body); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(body.Organic))
	for _, r := range body.Organic {
		out = append(out, SearchResult{Title: r.Title, URL: r.Link, Snippet: r.Snippet})
	}
	return out, nil
}

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

// fetchSearchPage gets a search engine page.
func fetchSearchPage(ctx context.Context, endpoint string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "application/rss+xml,text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := webHTTP.Do(req)
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

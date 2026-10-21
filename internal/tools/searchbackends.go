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
	searchTimeout   = 10 * time.Second
	searchEngineTTL = 8 * time.Second
	searchCacheTTL  = 10 * time.Minute
	searchCacheMax  = 256
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
	tavilyAPIURL = "https://api.tavily.com/search"
	serperAPIURL = "https://google.serper.dev/search"
)

// searchWeb returns results from an API provider when one is configured,
// otherwise races Bing RSS and DuckDuckGo HTML with a hard deadline. Results
// are cached so parallel workers do not fetch the same query repeatedly.
func searchWeb(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}
	key := strings.ToLower(strings.TrimSpace(query)) + "|" + fmt.Sprint(limit)
	if cached := cachedSearch(key); cached != nil {
		return cached, nil
	}
	searchCtx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	var apiErr error
	if key := strings.TrimSpace(os.Getenv("BRAVE_API_KEY")); key != "" {
		if results, err := searchBrave(searchCtx, key, query, limit); err == nil && len(results) > 0 {
			return cacheSearch(key, cleanResults(results, limit)), nil
		} else {
			apiErr = err
		}
	}
	if key := strings.TrimSpace(os.Getenv("TAVILY_API_KEY")); key != "" {
		if results, err := searchTavily(searchCtx, key, query, limit); err == nil && len(results) > 0 {
			return cacheSearch(key, cleanResults(results, limit)), nil
		} else {
			apiErr = err
		}
	}
	if key := strings.TrimSpace(os.Getenv("SERPER_API_KEY")); key != "" {
		if results, err := searchSerper(searchCtx, key, query, limit); err == nil && len(results) > 0 {
			return cacheSearch(key, cleanResults(results, limit)), nil
		} else {
			apiErr = err
		}
	}

	engines := []searchEngine{
		{name: "bing", query: searchBingRSS},
		{name: "duckduckgo", query: searchDuckDuckGo},
	}
	type response struct {
		results []SearchResult
		err     error
	}
	ch := make(chan response, len(engines))
	var wg sync.WaitGroup
	for _, engine := range engines {
		wg.Add(1)
		go func(engine searchEngine) {
			defer wg.Done()
			engineCtx, cancel := context.WithTimeout(searchCtx, searchEngineTTL)
			defer cancel()
			results, err := engine.query(engineCtx, query, limit)
			ch <- response{results: results, err: err}
		}(engine)
	}
	wg.Wait()
	close(ch)

	var all []SearchResult
	var lastErr error
	for response := range ch {
		if response.err != nil {
			lastErr = response.err
			continue
		}
		all = append(all, response.results...)
	}
	all = cleanResults(all, limit)
	if len(all) > 0 {
		return cacheSearch(key, all), nil
	}
	if apiErr != nil {
		lastErr = apiErr
	}
	if lastErr != nil {
		return nil, fmt.Errorf("search failed: %w", lastErr)
	}
	return nil, nil
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

// cleanResults removes duplicates and tracking parameters, preserving order.
func cleanResults(results []SearchResult, limit int) []SearchResult {
	seen := map[string]bool{}
	out := make([]SearchResult, 0, len(results))
	for _, r := range results {
		r.URL = canonicalResultURL(r.URL)
		r.Title = strings.TrimSpace(r.Title)
		r.Snippet = strings.TrimSpace(r.Snippet)
		if r.URL == "" || seen[r.URL] {
			continue
		}
		seen[r.URL] = true
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, braveAPIURL+url.QueryEscape(query)+"&count="+fmt.Sprint(limit), nil)
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
		"api_key": key, "query": query, "max_results": limit, "search_depth": "basic",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tavilyAPIURL, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
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

func searchSerper(ctx context.Context, key, query string, limit int) ([]SearchResult, error) {
	payload, _ := json.Marshal(map[string]any{"q": query, "num": limit})
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

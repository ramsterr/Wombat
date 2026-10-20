package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// SearchResult is one web search hit.
type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// searchEngine is one backend the websearch tool can use.
type searchEngine struct {
	name  string
	query func(ctx context.Context, query string, limit int) ([]SearchResult, error)
}

// searchEngines are tried in order until one returns results. Public search
// pages block scrapers often, so several are tried and the model is told how
// to proceed when none of them work.
var searchEngines = []searchEngine{
	{name: "bing", query: searchBing},
	{name: "duckduckgo", query: searchDuckDuckGo},
}

var (
	bingBlockRe  = regexp.MustCompile(`(?is)<li class="b_algo".*?</li>`)
	bingTitleRe  = regexp.MustCompile(`(?is)<h2[^>]*>\s*<a[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	bingSnipRe   = regexp.MustCompile(`(?is)<p[^>]*>(.*?)</p>`)
	ddgResultRe  = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgSnipRe    = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>`)
	ddgRedirect  = regexp.MustCompile(`uddg=([^&]+)`)
	bingRedirect = regexp.MustCompile(`[?&]u=a1([A-Za-z0-9_-]+)`)
)

// searchBing queries Bing's HTML endpoint and parses the result blocks.
func searchBing(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	page, err := fetchSearchPage(ctx, "https://www.bing.com/search?q="+url.QueryEscape(query))
	if err != nil {
		return nil, err
	}
	blocks := bingBlockRe.FindAllString(page, -1)
	out := make([]SearchResult, 0, limit)
	for _, b := range blocks {
		if len(out) >= limit {
			break
		}
		m := bingTitleRe.FindStringSubmatch(b)
		if m == nil {
			continue
		}
		link := decodeBingURL(m[1])
		if link == "" || strings.Contains(link, "bing.com") {
			continue
		}
		title := stripTags(m[2])
		snippet := ""
		if p := bingSnipRe.FindStringSubmatch(b); p != nil {
			snippet = stripTags(p[1])
		}
		out = append(out, SearchResult{Title: title, URL: link, Snippet: snippet})
	}
	return out, nil
}

// decodeBingURL unwraps Bing's redirect links.
func decodeBingURL(raw string) string {
	raw = strings.ReplaceAll(raw, "&amp;", "&")
	m := bingRedirect.FindStringSubmatch(raw)
	if m == nil {
		return raw
	}
	dec, err := base64.RawURLEncoding.DecodeString(m[1])
	if err != nil {
		return raw
	}
	return string(dec)
}

// searchDuckDuckGo queries DuckDuckGo's HTML endpoint and parses the results.
func searchDuckDuckGo(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	page, err := fetchSearchPage(ctx, "https://html.duckduckgo.com/html/?q="+url.QueryEscape(query))
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

// fetchSearchPage gets a search engine page.
func fetchSearchPage(ctx context.Context, endpoint string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
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

// browserUA is a plain browser user agent: search engines block obvious bots.
const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// stripTags removes HTML tags and entities from a string.
func stripTags(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = unescapeEntities(s)
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

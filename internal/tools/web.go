package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// WebFetch fetches a URL and returns readable text.
type WebFetch struct{}

type webFetchArgs struct {
	URL string `json:"url"`
}

func (t *WebFetch) Name() string  { return "webfetch" }
func (t *WebFetch) Class() string { return "network" }
func (t *WebFetch) Description() string {
	return "Fetch a URL over the network and return readable text, extracted from the main article when possible. Responses are cached for repeated reads. Use known documentation URLs instead of a shell command; set a focused search query when the URL is unknown."
}
func (t *WebFetch) Schema() map[string]any {
	return obj(map[string]any{
		"url": desc("absolute URL to fetch, for example https://example.com/docs"),
	}, "url")
}

// maxFetchBytes caps how much of a page is read.
const maxFetchBytes = 4 << 20

func (t *WebFetch) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[webFetchArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	target := strings.TrimSpace(a.URL)
	if target == "" {
		return "", fmt.Errorf("url is required")
	}
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://" + target
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("bad url: %w", err)
	}
	if err := env.Check("network", target); err != nil {
		return "", err
	}
	if cached := cacheGet(target); cached != "" {
		return cached, nil
	}
	body, contentType, err := fetchWebPage(ctx, parsed.String())
	if err != nil {
		return "", err
	}
	text := string(body)
	if strings.Contains(contentType, "html") || looksLikeHTML(text) {
		text = htmlToText(text)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Sprintf("fetched %s (%d bytes) but it had no readable text; content-type %s", parsed.String(), len(body), contentType), nil
	}
	text = Truncate(text, 400, 0.5, 32*1024)
	cacheSet(target, text)
	return text, nil
}

var scriptRe = regexp.MustCompile(`(?is)<(script|style|noscript|svg|template)[^>]*>.*?</(script|style|noscript|svg|template)>`)
var chromeRe = regexp.MustCompile(`(?is)<(nav|header|footer|aside|form)[^>]*>.*?</(nav|header|footer|aside|form)>`)
var mainRe = regexp.MustCompile(`(?is)<article[^>]*>(.*?)</article>|<main[^>]*>(.*?)</main>`)
var tagRe = regexp.MustCompile(`(?s)<[^>]+>`)
var spaceRe = regexp.MustCompile(`[ \t]+`)
var blankLinesRe = regexp.MustCompile(`\n{3,}`)

// looksLikeHTML reports whether a body is an HTML page.
func looksLikeHTML(s string) bool {
	head := strings.ToLower(s)
	if len(head) > 2048 {
		head = head[:2048]
	}
	return strings.Contains(head, "<html") || strings.Contains(head, "<body") || strings.Contains(head, "<div")
}

// unescapeEntities turns HTML entities into plain text.
func unescapeEntities(s string) string { return html.UnescapeString(s) }

// htmlToText turns an HTML page into readable text.
func htmlToText(s string) string {
	s = scriptRe.ReplaceAllString(s, " ")
	s = chromeRe.ReplaceAllString(s, " ")
	if m := mainRe.FindStringSubmatch(s); m != nil {
		for _, candidate := range m[1:] {
			if strings.TrimSpace(candidate) != "" {
				s = candidate
				break
			}
		}
	}
	s = strings.ReplaceAll(s, "</p>", "\n")
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "</li>", "\n")
	s = strings.ReplaceAll(s, "</h1>", "\n")
	s = strings.ReplaceAll(s, "</h2>", "\n")
	s = strings.ReplaceAll(s, "</h3>", "\n")
	s = strings.ReplaceAll(s, "</tr>", "\n")
	s = strings.ReplaceAll(s, "</td>", "\t")
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(spaceRe.ReplaceAllString(line, " "))
	}
	s = strings.Join(lines, "\n")
	s = blankLinesRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

type webCacheEntry struct {
	text    string
	expires time.Time
}

var webCache = struct {
	sync.Mutex
	entries map[string]webCacheEntry
}{entries: map[string]webCacheEntry{}}

const (
	webCacheTTL = 10 * time.Minute
	webCacheMax = 128
)

func cacheGet(target string) string {
	webCache.Lock()
	defer webCache.Unlock()
	entry, ok := webCache.entries[target]
	if !ok || time.Now().After(entry.expires) {
		delete(webCache.entries, target)
		return ""
	}
	return entry.text
}

func cacheSet(target, text string) {
	webCache.Lock()
	defer webCache.Unlock()
	if len(webCache.entries) >= webCacheMax {
		for key, entry := range webCache.entries {
			if time.Now().After(entry.expires) {
				delete(webCache.entries, key)
			}
		}
		if len(webCache.entries) >= webCacheMax {
			for key := range webCache.entries {
				delete(webCache.entries, key)
				break
			}
		}
	}
	webCache.entries[target] = webCacheEntry{text: text, expires: time.Now().Add(webCacheTTL)}
}

func clearWebCache() {
	webCache.Lock()
	webCache.entries = map[string]webCacheEntry{}
	webCache.Unlock()
}

func fetchWebPage(ctx context.Context, target string) ([]byte, string, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("User-Agent", browserUA)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,text/plain;q=0.9,*/*;q=0.5")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		resp, err := webHTTP.Do(req)
		if err != nil {
			lastErr = err
			if attempt == 0 && transientWebError(err) {
				select {
				case <-ctx.Done():
					return nil, "", ctx.Err()
				case <-time.After(250 * time.Millisecond):
				}
				continue
			}
			return nil, "", fmt.Errorf("fetch failed: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, "", fmt.Errorf("fetch failed: http %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
		if err != nil {
			return nil, "", fmt.Errorf("read failed: %w", err)
		}
		return body, resp.Header.Get("Content-Type"), nil
	}
	return nil, "", fmt.Errorf("fetch failed: %w", lastErr)
}

func transientWebError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{"connection reset", "connection refused", "i/o timeout", "temporary failure", "unexpected eof"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

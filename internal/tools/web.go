package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

// WebFetch fetches a URL and returns readable text.
type WebFetch struct{}

type webFetchArgs struct {
	URL string `json:"url"`
}

func (t *WebFetch) Name() string  { return "webfetch" }
func (t *WebFetch) Class() string { return "network" }
func (t *WebFetch) Description() string {
	return "Fetch a URL over the network and return readable text: HTML is stripped to the main article, JSON is pretty-printed, text/XML is returned, and PDFs/binary files get a clear note. Responses are cached for repeated reads. Use known documentation URLs instead of a shell command; use websearch when the URL is unknown."
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
	if err := env.AllowFetch(); err != nil {
		return "", err
	}
	if err := env.Check("network", target); err != nil {
		return "", err
	}
	cacheKey := canonicalResultURL(parsed.String())
	if cached := cacheGet(cacheKey); cached != "" {
		return cached, nil
	}
	body, contentType, err := fetchWebPage(ctx, parsed.String())
	if err != nil {
		return "", err
	}
	mediaType, params, _ := mime.ParseMediaType(contentType)
	text, note := renderWebBody(body, mediaType, params["charset"])
	if note != "" {
		return note, nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Sprintf("fetched %s (%d bytes) but it had no readable text; content-type %s", parsed.String(), len(body), contentType), nil
	}
	text = Truncate(text, 400, 0.5, 32*1024)
	cacheSet(cacheKey, text)
	return text, nil
}

// renderWebBody chooses the right extraction path for the response. It keeps
// JSON as JSON, refuses binary/PDF payloads with a useful note, and decodes the
// declared charset before running the HTML extractor.
func renderWebBody(body []byte, mediaType, charset string) (string, string) {
	if len(body) == 0 {
		return "", "the response was empty"
	}
	switch {
	case strings.Contains(mediaType, "json") || looksLikeJSON(body):
		var v any
		if json.Unmarshal(body, &v) == nil {
			if pretty, err := json.MarshalIndent(v, "", "  "); err == nil {
				return string(pretty), ""
			}
		}
		return string(body), ""
	case strings.Contains(mediaType, "pdf"):
		return "", fmt.Sprintf("this is a PDF (%d bytes); open it with the browser tool or download it for a PDF reader", len(body))
	case strings.Contains(mediaType, "html") || looksLikeHTML(string(body)):
		return htmlToText(decodeBody(body, charset)), ""
	case strings.HasPrefix(mediaType, "text/") || strings.Contains(mediaType, "xml"):
		return decodeBody(body, charset), ""
	}
	text := string(body)
	if looksLikeHTML(text) {
		return htmlToText(decodeBody(body, charset)), ""
	}
	if isBinary(body) {
		return "", fmt.Sprintf("binary response (%d bytes, content-type %q); use the browser tool if you need to view it", len(body), mediaType)
	}
	return decodeBody(body, charset), ""
}

func looksLikeJSON(body []byte) bool {
	t := strings.TrimSpace(string(body))
	return strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")
}

func isBinary(body []byte) bool {
	check := body
	if len(check) > 4096 {
		check = check[:4096]
	}
	bad := 0
	for _, b := range check {
		if b == 0 {
			return true
		}
		if b < 0x09 || (b > 0x0d && b < 0x20) {
			bad++
		}
	}
	return bad > len(check)/20
}

func decodeBody(body []byte, charset string) string {
	charset = strings.TrimSpace(strings.Trim(charset, `"'`))
	if charset == "" || strings.EqualFold(charset, "utf-8") || strings.EqualFold(charset, "utf8") {
		return strings.TrimPrefix(string(body), "\ufeff")
	}
	enc, err := htmlindex.Get(charset)
	if err != nil {
		return string(body)
	}
	out, _, err := transform.Bytes(enc.NewDecoder(), body)
	if err != nil {
		return string(body)
	}
	return strings.TrimPrefix(string(out), "\ufeff")
}

var scriptRe = regexp.MustCompile(`(?is)<(script|style|noscript|svg|template|iframe|canvas|form|nav|header|footer|aside|button|input|select|textarea)[^>]*>.*?</(script|style|noscript|svg|template|iframe|canvas|form|nav|header|footer|aside|button|input|select|textarea)>`)
var commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
var headRe = regexp.MustCompile(`(?is)<head[^>]*>.*?</head>`)
var mainRe = regexp.MustCompile(`(?is)<article[^>]*>(.*?)</article>|<main[^>]*>(.*?)</main>`)
var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
var metaDescRe = regexp.MustCompile(`(?is)<meta[^>]+(?:name|property)=["'](?:description|og:description)["'][^>]+content=["']([^"']*)["']`)
var metaDescRe2 = regexp.MustCompile(`(?is)<meta[^>]+content=["']([^"']*)["'][^>]+(?:name|property)=["'](?:description|og:description)["']`)
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

// htmlToText turns an HTML page into readable text, dropping chrome and
// keeping the title/description as a short source header.
func htmlToText(s string) string {
	title := stripTags(firstMatch(titleRe, s))
	desc := stripTags(firstMatch(metaDescRe, s))
	if desc == "" {
		desc = stripTags(firstMatch(metaDescRe2, s))
	}
	s = commentRe.ReplaceAllString(s, " ")
	s = scriptRe.ReplaceAllString(s, " ")
	s = headRe.ReplaceAllString(s, " ")
	if m := mainRe.FindStringSubmatch(s); m != nil {
		for _, candidate := range m[1:] {
			if len(strings.TrimSpace(candidate)) > 200 {
				s = candidate
				break
			}
		}
	}
	for _, tag := range []string{
		"</p>", "</div>", "</section>", "</article>", "</li>", "</h1>",
		"</h2>", "</h3>", "</h4>", "</h5>", "</h6>", "</tr>", "</blockquote>",
		"<br>", "<br/>", "<br />",
	} {
		s = strings.ReplaceAll(s, tag, "\n")
	}
	s = strings.ReplaceAll(s, "</td>", "\t")
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(spaceRe.ReplaceAllString(line, " "))
	}
	s = strings.Join(lines, "\n")
	s = blankLinesRe.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)
	var header []string
	if title != "" {
		header = append(header, "Title: "+title)
	}
	if desc != "" {
		header = append(header, "Description: "+desc)
	}
	if len(header) > 0 {
		return strings.Join(header, "\n") + "\n\n" + s
	}
	return s
}

func firstMatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) > 1 {
		return m[1]
	}
	return ""
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

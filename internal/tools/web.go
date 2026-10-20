package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
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
	return "Fetch a URL over the network and return its readable text. Use it for documentation, articles, and API responses, instead of a shell command."
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "wombat/1.0 (+https://github.com/ramsterr/Wombat)")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("fetch failed: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
	if err != nil {
		return "", fmt.Errorf("read failed: %w", err)
	}
	contentType := resp.Header.Get("Content-Type")
	text := string(body)
	if strings.Contains(contentType, "html") || looksLikeHTML(text) {
		text = htmlToText(text)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Sprintf("fetched %s (%d bytes) but it had no readable text; content-type %s", parsed.String(), len(body), contentType), nil
	}
	return Truncate(text, 400, 0.5, 32*1024), nil
}

var scriptRe = regexp.MustCompile(`(?is)<(script|style|noscript|svg)[^>]*>.*?</(script|style|noscript|svg)>`)
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

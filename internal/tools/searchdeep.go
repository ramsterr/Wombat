package tools

import (
	"context"
	"mime"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	deepAutoPages  = 3
	deepMaxPages   = 6
	deepAutoBudget = 4 * time.Second
	deepFullBudget = 7 * time.Second
	deepExcerpt    = 420
)

// enrichSearchResults fetches the top result pages and adds a short excerpt
// plus publication date and author when the page exposes them. Failures are
// skipped: enrichment must never discard search results.
func enrichSearchResults(ctx context.Context, results []SearchResult, query, depth string) []SearchResult {
	pages, budget := deepAutoPages, deepAutoBudget
	switch depth {
	case "fast":
		return results
	case "deep":
		pages, budget = deepMaxPages, deepFullBudget
	}
	if len(results) < pages {
		pages = len(results)
	}
	if pages == 0 {
		return results
	}
	runCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	type pageResult struct {
		index   int
		meta    pageMetadata
		excerpt string
		ok      bool
	}
	ch := make(chan pageResult, pages)
	for i := 0; i < pages; i++ {
		go func(i int, target string) {
			info, ok := fetchPageInfo(runCtx, target, query)
			ch <- pageResult{index: i, meta: info.meta, excerpt: info.excerpt, ok: ok}
		}(i, results[i].URL)
	}
	for done := 0; done < pages; done++ {
		select {
		case page := <-ch:
			if !page.ok {
				continue
			}
			r := &results[page.index]
			if r.Published == "" {
				r.Published = page.meta.Published
			}
			if r.Author == "" {
				r.Author = page.meta.Author
			}
			if r.Excerpt == "" {
				r.Excerpt = page.excerpt
			}
		case <-runCtx.Done():
			return results
		}
	}
	return results
}

type deepPageInfo struct {
	meta    pageMetadata
	excerpt string
}

func fetchPageInfo(ctx context.Context, target, query string) (deepPageInfo, bool) {
	body, contentType, err := fetchWebPage(ctx, target)
	if err != nil || len(body) == 0 {
		return deepPageInfo{}, false
	}
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if !strings.Contains(mediaType, "html") && !looksLikeHTML(string(body)) {
		return deepPageInfo{}, false
	}
	doc := decodeBody(body, params["charset"])
	meta := extractPageMetadata(doc)
	excerpt := queryExcerpt(pageBodyText(htmlToText(doc)), query, deepExcerpt)
	if excerpt == "" {
		return deepPageInfo{}, false
	}
	return deepPageInfo{meta: meta, excerpt: excerpt}, true
}

// pageBodyText drops the title/description header that htmlToText adds.
func pageBodyText(text string) string {
	if strings.HasPrefix(text, "Title: ") || strings.HasPrefix(text, "Description: ") {
		if i := strings.Index(text, "\n\n"); i >= 0 {
			return strings.TrimSpace(text[i+2:])
		}
	}
	return strings.TrimSpace(text)
}

// queryExcerpt picks the densest window of query terms so the model sees
// relevant page text instead of navigation, headers, or boilerplate.
func queryExcerpt(text, query string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if text == "" || maxRunes <= 0 {
		return ""
	}
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) == 0 {
		return ""
	}
	terms := queryTokens(query)
	// Short terms such as "go" or "ai" match navigation and version strings,
	// so anchor the window on longer terms when the query has any.
	anchors := make([]string, 0, len(terms))
	for _, term := range terms {
		if utf8.RuneCountInString(term) > 2 {
			anchors = append(anchors, term)
		}
	}
	if len(anchors) == 0 {
		anchors = terms
	}
	if len(anchors) == 0 {
		end := maxRunes
		if end > len(runes) {
			end = len(runes)
		}
		return strings.TrimSpace(string(runes[:end]))
	}

	lower := strings.ToLower(string(runes))
	lead := maxRunes / 6
	starts := make([]int, 0, len(anchors)*8)
	for _, term := range anchors {
		offset := 0
		for found := 0; found < 40; found++ {
			i := strings.Index(lower[offset:], term)
			if i < 0 {
				break
			}
			pos := utf8.RuneCountInString(lower[:offset+i]) - lead
			if pos < 0 {
				pos = 0
			}
			starts = append(starts, pos)
			offset += i + len(term)
		}
	}
	start, best := 0, -1
	for _, candidate := range starts {
		end := candidate + maxRunes
		if end > len(runes) {
			end = len(runes)
		}
		window := strings.ToLower(string(runes[candidate:end]))
		score := 0
		for _, term := range anchors {
			if strings.Contains(window, term) {
				score++
			}
		}
		if score > best {
			start, best = candidate, score
		}
	}
	end := start + maxRunes
	if end > len(runes) {
		end = len(runes)
	}
	excerpt := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		if i := strings.IndexByte(excerpt, ' '); i > 0 && i < 60 {
			excerpt = excerpt[i+1:]
		}
		excerpt = "…" + excerpt
	}
	if end < len(runes) {
		if i := strings.LastIndexByte(excerpt, ' '); i >= 0 && i > len(excerpt)-60 {
			excerpt = excerpt[:i]
		}
		excerpt += "…"
	}
	return strings.TrimSpace(excerpt)
}

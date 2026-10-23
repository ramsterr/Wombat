package tools

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// pageMetadata is what a deep search can learn from a result page.
type pageMetadata struct {
	Title       string
	Description string
	Author      string
	Published   string
}

var (
	metaTagRe         = regexp.MustCompile(`(?is)<meta\s[^>]*>`)
	metaAttrRe        = regexp.MustCompile(`(?is)([a-z0-9:_-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	timeTagRe         = regexp.MustCompile(`(?is)<time\b[^>]*\bdatetime\s*=\s*(?:"([^"]+)"|'([^']+)'|([^\s>]+))`)
	jsonDateRe        = regexp.MustCompile(`(?is)"date(?:Published|Modified)"\s*:\s*"([^"]+)"`)
	jsonAuthorRe      = regexp.MustCompile(`(?is)"author"\s*:\s*[\[{][^{}]{0,400}?"name"\s*:\s*"([^"]+)"`)
	relativeDateRe    = regexp.MustCompile(`(?i)^(\d+)\s+(minute|hour|day|week|month|year)s?\s+ago$`)
	datedLayouts      = []string{time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05Z0700", "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02", "2006/01/02", "January 2, 2006", "Jan 2, 2006", "2 January 2006", "02 Jan 2006", "Mon, 02 Jan 2006 15:04:05 MST"}
	publishedMetaKeys = []string{"article:published_time", "article:modified_time", "datepublished", "datemodified", "date", "pubdate", "publishdate", "dc.date", "dc.date.issued", "citation_publication_date", "citation_date"}
)

// extractPageMetadata reads common date, author, title, and description tags
// from an HTML page, including Open Graph, JSON-LD, and <time> fallbacks.
func extractPageMetadata(doc string) pageMetadata {
	var md pageMetadata
	values := map[string]string{}
	for _, tag := range metaTagRe.FindAllString(doc, -1) {
		attrs := map[string]string{}
		for _, m := range metaAttrRe.FindAllStringSubmatch(tag, -1) {
			key := strings.ToLower(m[1])
			val := firstNonEmpty(m[2], m[3], m[4])
			attrs[key] = strings.TrimSpace(val)
		}
		key := strings.ToLower(firstNonEmpty(attrs["property"], attrs["name"], attrs["itemprop"]))
		if key == "" || attrs["content"] == "" {
			continue
		}
		values[key] = attrs["content"]
	}
	md.Title = cleanMetaValue(firstNonEmpty(values["og:title"], values["twitter:title"]))
	md.Description = cleanMetaValue(firstNonEmpty(values["description"], values["og:description"], values["twitter:description"]))
	md.Author = cleanMetaValue(firstNonEmpty(values["author"], values["article:author"], values["citation_author"], values["dc.creator"]))
	for _, key := range publishedMetaKeys {
		if md.Published = normalizeDate(values[key]); md.Published != "" {
			break
		}
	}
	if md.Published == "" {
		if m := timeTagRe.FindStringSubmatch(doc); m != nil {
			md.Published = normalizeDate(firstNonEmpty(m[1], m[2], m[3]))
		}
	}
	if md.Published == "" {
		if m := jsonDateRe.FindStringSubmatch(doc); m != nil {
			md.Published = normalizeDate(m[1])
		}
	}
	if md.Author == "" {
		if m := jsonAuthorRe.FindStringSubmatch(doc); m != nil {
			md.Author = cleanMetaValue(stripTags(m[1]))
		}
	}
	return md
}

// normalizeDate turns a machine or human date into YYYY-MM-DD when possible.
// Unknown formats are returned as-is so the model still sees the source value.
func normalizeDate(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if len(s) > 64 {
		s = s[:64]
	}
	switch strings.ToLower(s) {
	case "n/a", "na", "unknown", "none", "null", "-", "—":
		return ""
	}
	if t, ok := parseDateString(s); ok {
		return t.Format("2006-01-02")
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 1_000_000_000 {
		if n > 1_000_000_000_000 {
			n /= 1000
		}
		return time.Unix(n, 0).UTC().Format("2006-01-02")
	}
	lower := strings.ToLower(s)
	if lower == "yesterday" {
		return time.Now().Add(-24 * time.Hour).Format("2006-01-02")
	}
	if m := relativeDateRe.FindStringSubmatch(lower); len(m) == 3 {
		n, _ := strconv.Atoi(m[1])
		unit := 24 * time.Hour
		switch m[2] {
		case "minute":
			unit = time.Minute
		case "hour":
			unit = time.Hour
		case "week":
			unit = 7 * 24 * time.Hour
		case "month":
			unit = 30 * 24 * time.Hour
		case "year":
			unit = 365 * 24 * time.Hour
		}
		return time.Now().Add(-time.Duration(n) * unit).Format("2006-01-02")
	}
	return s
}

func parseDateString(s string) (time.Time, bool) {
	for _, layout := range datedLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// publishedTime returns the parsed publication date when it is known.
func publishedTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if t, ok := parseDateString(s); ok {
		return t, true
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// cleanMetaValue drops placeholder metadata such as "N/A" that backends and
// pages commonly emit when they do not know a value.
func cleanMetaValue(s string) string {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "n/a", "na", "unknown", "none", "null", "-", "—":
		return ""
	}
	return s
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// WebSearch searches the web and returns ranked results with page excerpts.
type WebSearch struct{}

type webSearchArgs struct {
	Query     string   `json:"query"`
	Queries   []string `json:"queries,omitempty"`
	Limit     int      `json:"limit,omitempty"`
	Depth     string   `json:"depth,omitempty"`
	Freshness string   `json:"freshness,omitempty"`
	Language  string   `json:"language,omitempty"`
	Region    string   `json:"region,omitempty"`
	Page      int      `json:"page,omitempty"`
}

func (t *WebSearch) Name() string  { return "websearch" }
func (t *WebSearch) Class() string { return "network" }
func (t *WebSearch) Description() string {
	return "Search the web for targeted external facts. Runs Brave, Serper, Exa, Tavily, Bing, DuckDuckGo, and Mojeek in parallel with retries, merges and ranks their results, gives Exa extra weight, and fetches the top pages by default so each hit can include content, a publication date, and an author. Pass queries to fan out 2-4 independent variants, freshness to prefer recent pages, and depth=fast to skip page fetching or depth=deep for up to six pages. Use specific queries and site: filters; prefer webfetch when you already know the URL."
}
func (t *WebSearch) Schema() map[string]any {
	return obj(map[string]any{
		"query":     desc("the main search query"),
		"queries":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "optional 1-3 additional query variants to run in parallel with the main query"},
		"limit":     map[string]any{"type": "integer", "description": "how many results to return (default 8, max 20)"},
		"depth":     map[string]any{"type": "string", "enum": []string{"auto", "fast", "deep"}, "description": "auto (default) fetches the top 3 pages, fast uses snippets only, deep fetches up to 6 pages"},
		"freshness": map[string]any{"type": "string", "enum": []string{"day", "week", "month", "year"}, "description": "drop dated results older than this window and prefer recent pages"},
		"language":  desc("optional language code, for example en or fr"),
		"region":    desc("optional region/country code, for example us or gb"),
		"page":      map[string]any{"type": "integer", "description": "result page to request where a backend supports it (1-5, default 1)"},
	}, "query")
}

func (t *WebSearch) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[webSearchArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	query := strings.TrimSpace(a.Query)
	queries := make([]string, 0, 1+len(a.Queries))
	if query != "" {
		queries = append(queries, query)
	}
	for _, q := range a.Queries {
		if q = strings.TrimSpace(q); q != "" {
			queries = append(queries, q)
		}
	}
	if len(queries) == 0 {
		return "", fmt.Errorf("query is required")
	}
	if len(queries) > 4 {
		return "", fmt.Errorf("at most 4 queries are allowed")
	}
	for _, q := range queries {
		if len([]rune(q)) > 400 {
			return "", fmt.Errorf("query is too long (max 400 characters)")
		}
	}
	depth := strings.ToLower(strings.TrimSpace(a.Depth))
	if depth == "" {
		depth = env.ResearchDepth()
		if depth == "" {
			depth = "auto"
		}
	}
	switch depth {
	case "auto", "fast", "deep":
	default:
		return "", fmt.Errorf("depth must be auto, fast, or deep")
	}
	freshness := strings.ToLower(strings.TrimSpace(a.Freshness))
	switch freshness {
	case "", "day", "week", "month", "year":
	default:
		return "", fmt.Errorf("freshness must be day, week, month, or year")
	}
	language := strings.ToLower(strings.TrimSpace(a.Language))
	region := strings.ToLower(strings.TrimSpace(a.Region))
	if !validSearchCode(language, 15) {
		return "", fmt.Errorf("invalid language code %q", a.Language)
	}
	if !validSearchCode(region, 8) {
		return "", fmt.Errorf("invalid region code %q", a.Region)
	}
	if err := env.AllowSearch(); err != nil {
		return "", err
	}
	if err := env.Check("network", strings.Join(queries, " | ")); err != nil {
		return "", err
	}
	results, warnings, err := searchWeb(ctx, searchOptions{
		Queries:   queries,
		Limit:     a.Limit,
		Depth:     depth,
		Freshness: freshness,
		Language:  language,
		Region:    region,
		Page:      a.Page,
	})
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		msg := fmt.Sprintf("no safe, high-quality results for %q.", strings.Join(queries, " / "))
		if len(warnings) > 0 {
			msg += " Backends: " + strings.Join(warnings, "; ") + "."
		}
		return msg + " Try a more specific query, add a site: filter, or fetch a known documentation URL directly.", nil
	}
	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, r.Title, r.URL)
		var meta []string
		if host := hostnameOf(r.URL); host != "" {
			meta = append(meta, "source: "+host)
		}
		if r.Published != "" {
			meta = append(meta, "published: "+r.Published)
		}
		if r.Author != "" {
			meta = append(meta, "author: "+r.Author)
		}
		if len(meta) > 0 {
			fmt.Fprintf(&b, "   %s\n", strings.Join(meta, " · "))
		}
		if r.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", r.Snippet)
		}
		if r.Excerpt != "" {
			fmt.Fprintf(&b, "   page: %s\n", r.Excerpt)
		}
	}
	if len(warnings) > 0 {
		fmt.Fprintf(&b, "\nnote: some backends failed: %s", strings.Join(warnings, "; "))
	}
	return strings.TrimSpace(b.String()), nil
}

func validSearchCode(code string, max int) bool {
	if code == "" {
		return true
	}
	if len(code) > max {
		return false
	}
	for _, r := range code {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// WebSearch searches the web and returns ranked results.
type WebSearch struct{}

type webSearchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

func (t *WebSearch) Name() string  { return "websearch" }
func (t *WebSearch) Class() string { return "network" }
func (t *WebSearch) Description() string {
	return "Search the web for targeted external facts and get ranked titles, URLs, and snippets. Results are cached and backends run in parallel. Prefer known documentation URLs with webfetch when you already know the source; do not search for stable domain knowledge you already have."
}
func (t *WebSearch) Schema() map[string]any {
	return obj(map[string]any{
		"query": desc("the search query"),
		"limit": map[string]any{"type": "integer", "description": "how many results to return (default 8, max 20)"},
	}, "query")
}

func (t *WebSearch) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[webSearchArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	query := strings.TrimSpace(a.Query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}
	if err := env.Check("network", query); err != nil {
		return "", err
	}
	results, err := searchWeb(ctx, query, limit)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return fmt.Sprintf("no results for %q. If searching keeps failing, fetch a search page with webfetch instead.", query), nil
	}
	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", r.Snippet)
		}
	}
	return strings.TrimSpace(b.String()), nil
}

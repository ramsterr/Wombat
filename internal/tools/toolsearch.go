package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ToolSearch searches optional capabilities by task description. It returns
// compact matches only; the model must call request_capability to load one.
type ToolSearch struct{}

func (t *ToolSearch) Name() string  { return "tool_search" }
func (t *ToolSearch) Class() string { return "read" }
func (t *ToolSearch) Description() string {
	return "Search optional capabilities and their tools by task. Returns compact matches, not schemas. Call request_capability with the matching capability name to load it."
}
func (t *ToolSearch) Schema() map[string]any {
	return obj(map[string]any{
		"query": desc("what you need to do, for example 'inspect a web page' or 'find a symbol definition'"),
	}, "query")
}
func (t *ToolSearch) Run(_ context.Context, raw json.RawMessage, _ *Env) (string, error) {
	var a struct {
		Query string `json:"query"`
	}
	if err := decodeInto(raw, &a); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	matches := SearchCapabilities(a.Query)
	if len(matches) == 0 {
		return "No capability matched. Available: " + strings.Join(CapabilityNames(), ", ") + ". You can also use bash for git, tests, curl, and ffmpeg.", nil
	}
	var b strings.Builder
	b.WriteString("Matching capabilities:\n")
	for _, match := range matches {
		fmt.Fprintf(&b, "- %s: %s Tools: %s.\n", match.Name, match.Description, strings.Join(match.Tools, ", "))
	}
	b.WriteString("Call request_capability with the capability name and a short reason to load one.")
	return b.String(), nil
}
func (t *ToolSearch) AllowedInWorkers() bool { return false }

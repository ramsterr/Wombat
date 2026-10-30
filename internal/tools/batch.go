package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Batch runs several read-only calls in one round-trip.
//
// The frontier harnesses all do this in some form: Claude Code's Workflow tool
// runs a script of many calls, DeepSeek's code mode never lets intermediate
// results into the conversation, and opencode tells the agent to issue all its
// calls in one message. One call here is one round-trip instead of N.
type Batch struct{}

type batchArgs struct {
	Calls []batchCall `json:"calls"`
}

type batchCall struct {
	Tool    string `json:"tool"`
	Path    string `json:"path,omitempty"`
	Pattern string `json:"pattern,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Offset  int    `json:"offset,omitempty"`
}

func (t *Batch) Name() string  { return "batch" }
func (t *Batch) Class() string { return "read" }
func (t *Batch) Description() string {
	return "Run several read-only calls (read, grep, glob) in one round-trip and get their results together. Use it instead of issuing many separate read or search calls. The calls must be independent."
}
func (t *Batch) Schema() map[string]any {
	return obj(map[string]any{
		"calls": map[string]any{
			"type":        "array",
			"description": "the read-only calls to run, for example {\"tool\":\"read\",\"path\":\"main.go\"}",
			"minItems":    1,
			"maxItems":    20,
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tool":    map[string]any{"type": "string", "description": "read, grep, or glob", "enum": []string{"read", "grep", "glob"}},
					"path":    map[string]any{"type": "string", "description": "file path, for read"},
					"pattern": map[string]any{"type": "string", "description": "pattern, for grep and glob"},
					"limit":   map[string]any{"type": "integer", "description": "result limit"},
					"offset":  map[string]any{"type": "integer", "description": "starting line, for read"},
				},
				"required": []string{"tool"},
			},
		},
	}, "calls")
}

func (t *Batch) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[batchArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if len(a.Calls) == 0 {
		return "", fmt.Errorf("batch needs at least one call")
	}
	if len(a.Calls) > 20 {
		return "", fmt.Errorf("batch takes at most 20 calls, got %d", len(a.Calls))
	}
	outs := make([]string, len(a.Calls))
	errs := make([]error, len(a.Calls))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i, c := range a.Calls {
		wg.Add(1)
		go func(i int, c batchCall) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			outs[i], errs[i] = runBatchCall(ctx, env, c)
		}(i, c)
	}
	wg.Wait()
	var b strings.Builder
	for i, c := range a.Calls {
		fmt.Fprintf(&b, "--- %d. %s %s ---\n", i+1, c.Tool, describeBatchCall(c))
		if errs[i] != nil {
			fmt.Fprintf(&b, "error: %s\n", errs[i])
			continue
		}
		b.WriteString(outs[i])
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()), nil
}

func runBatchCall(ctx context.Context, env *Env, c batchCall) (string, error) {
	switch c.Tool {
	case "read":
		args := map[string]any{"path": c.Path}
		if c.Limit > 0 {
			args["limit"] = c.Limit
		}
		if c.Offset > 0 {
			args["offset"] = c.Offset
		}
		raw, _ := json.Marshal(args)
		return (&Read{}).Run(ctx, raw, env)
	case "grep":
		args := map[string]any{"pattern": c.Pattern}
		if c.Path != "" {
			args["path"] = c.Path
		}
		if c.Limit > 0 {
			args["limit"] = c.Limit
		}
		raw, _ := json.Marshal(args)
		return (&Grep{}).Run(ctx, raw, env)
	case "glob":
		args := map[string]any{"pattern": c.Pattern}
		if c.Path != "" {
			args["path"] = c.Path
		}
		raw, _ := json.Marshal(args)
		return (&Glob{}).Run(ctx, raw, env)
	default:
		return "", fmt.Errorf("batch only runs read, grep, and glob (got %q)", c.Tool)
	}
}

func describeBatchCall(c batchCall) string {
	switch c.Tool {
	case "read":
		return " " + c.Path
	case "grep":
		if c.Path != "" {
			return " " + c.Pattern + " in " + c.Path
		}
		return " " + c.Pattern
	case "glob":
		return " " + c.Pattern
	}
	return ""
}

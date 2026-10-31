package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ramsterr/Wombat/internal/trace"
)

func TestPromptStatsTrace(t *testing.T) {
	a, _, workdir, _, _ := newTestAgent(t, true)
	if err := os.WriteFile(filepath.Join(workdir, "AGENTS.md"), []byte("# Instructions\nDo the thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.SystemExtra = func() string { return "### Skills\nUse the skill." }

	path := filepath.Join(t.TempDir(), "events.jsonl")
	tr, err := trace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a.Trace = tr
	if err := a.Run(context.Background(), "write hello"); err != nil {
		t.Fatal(err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var prompts, usages []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event struct {
			Kind   string         `json:"kind"`
			Fields map[string]any `json:"fields"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		switch event.Kind {
		case "prompt":
			prompts = append(prompts, event.Fields)
		case "model_usage":
			usages = append(usages, event.Fields)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 {
		t.Fatalf("prompt events = %d, want 2", len(prompts))
	}
	if len(usages) != 2 {
		t.Fatalf("model_usage events = %d, want 2", len(usages))
	}

	first := prompts[0]
	for _, key := range []string{
		"system_tokens", "base_system_tokens", "tool_schema_tokens",
		"skill_tokens", "project_instruction_tokens", "task_tokens",
		"history_tokens",
	} {
		if first[key].(float64) <= 0 {
			t.Errorf("first prompt %s = %v, want > 0", key, first[key])
		}
	}
	if first["mcp_tool_count"].(float64) != 0 {
		t.Errorf("mcp_tool_count = %v, want 0", first["mcp_tool_count"])
	}
	if prompts[1]["tool_result_tokens"].(float64) <= 0 {
		t.Errorf("second prompt tool_result_tokens = %v, want > 0", prompts[1]["tool_result_tokens"])
	}
	if usages[0]["in"].(float64) != 10 {
		t.Errorf("first step input = %v, want 10", usages[0]["in"])
	}
	if usages[1]["in"].(float64) != 20 {
		t.Errorf("second step input = %v, want 20", usages[1]["in"])
	}
}

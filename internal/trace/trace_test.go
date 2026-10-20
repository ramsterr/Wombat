package trace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Emit("run_start", map[string]any{"model": "m"})
	l.Emit("tool_end", map[string]any{"name": "read"})
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"run_start"`) || !strings.Contains(lines[1], `"read"`) {
		t.Fatalf("events = %q", data)
	}
}

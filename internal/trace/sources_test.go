package trace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSources(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	content := `{"kind":"tool_start","name":"bash","detail":"curl -s https://example.com/docs"}
{"kind":"tool_start","name":"bash","detail":"curl -s https://example.com/docs"}
{"kind":"tool_start","name":"read","detail":"main.go"}
{"kind":"tool_start","name":"write","detail":"main.go"}
{"kind":"run_end","name":"","detail":"ignored"}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	urls, files, err := Sources(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 1 || urls[0].Value != "https://example.com/docs" || urls[0].Calls != 2 {
		t.Errorf("urls = %+v", urls)
	}
	if len(files) != 1 || files[0].Value != "main.go" || files[0].Calls != 2 {
		t.Errorf("files = %+v", files)
	}
}

func TestSourcesMissingFile(t *testing.T) {
	if _, _, err := Sources(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Error("expected an error")
	}
}

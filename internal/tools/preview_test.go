package tools

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestPreviewServesWorkspace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>preview</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := testEnv(dir)
	defer env.ClosePreview()
	out := run(t, &Preview{}, env, `{}`)
	if !contains(out, "preview ready: http://127.0.0.1:") {
		t.Fatalf("preview output = %q", out)
	}
	if !contains(out, "index.html") && !contains(out, "http://127.0.0.1:") {
		t.Fatalf("missing URL in %q", out)
	}
	start := 0
	for i := 0; i+len("http://") <= len(out); i++ {
		if out[i:i+len("http://")] == "http://" {
			start = i
			break
		}
	}
	end := start
	for end < len(out) && out[end] != ' ' && out[end] != '\n' {
		end++
	}
	url := out[start:end]
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !contains(string(body), "preview") {
		t.Fatalf("GET %s = %d %q", url, resp.StatusCode, body)
	}

	// A second call reuses the same server instead of leaking another.
	out2 := run(t, &Preview{}, env, `{}`)
	if !contains(out2, "http://127.0.0.1:") {
		t.Fatalf("second preview = %q", out2)
	}
	if env.preview == nil || env.preview.listener == nil {
		t.Fatal("preview server was not kept alive")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

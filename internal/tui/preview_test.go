package tui

import (
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/tools"
)

func TestFindPreviewURL(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"  ➜  Local:   http://localhost:5177/\n", "http://localhost:5177/"},
		{"opened http://127.0.0.1:8080/\nNINE — Cat adoption studio", "http://127.0.0.1:8080/"},
		{"server at http://[::1]:3000/health.", "http://[::1]:3000/health"},
		{"\x1b[32mhttp://localhost:9000/\x1b[0m", "http://localhost:9000/"},
		{"external https://example.com and docs https://localhost.example.com/path", ""},
		{"no url here", ""},
	}
	for _, tc := range cases {
		if got := findPreviewURL(tc.text); got != tc.want {
			t.Errorf("findPreviewURL(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestPreviewBar(t *testing.T) {
	c := &chat{w: 80, streaming: true, previewURL: "http://localhost:5177/"}
	bar := c.previewBar()
	if !strings.Contains(bar, "preview ready") || !strings.Contains(bar, "still iterating") || !strings.Contains(bar, "http://localhost:5177/") {
		t.Fatalf("bar = %q", bar)
	}
	c.streaming = false
	if got := c.previewBar(); !strings.Contains(got, "ready") || strings.Contains(got, "still iterating") {
		t.Fatalf("finished bar = %q", got)
	}
	c.question = &tools.Question{Question: "Does this look right?"}
	if got := c.previewBar(); !strings.Contains(got, "waiting for your feedback") {
		t.Fatalf("question bar = %q", got)
	}
}

func TestPreviewOnlyFromServerTools(t *testing.T) {
	c := &chat{w: 80}
	c.detectToolPreview("read", "Local: http://localhost:5177/")
	if c.previewURL != "" {
		t.Fatal("reading a file must not set a preview")
	}
	c.detectToolPreview("bash", "cat old.log\nhttp://localhost:5177/")
	if c.previewURL != "" {
		t.Fatal("a plain URL in bash output must not set a preview")
	}
	c.detectToolPreview("bash", "VITE v7.3.7 ready in 322 ms\nLocal: http://localhost:5177/")
	if c.previewURL != "http://localhost:5177/" {
		t.Fatalf("vite banner preview = %q", c.previewURL)
	}
	c2 := &chat{w: 80}
	c2.detectToolPreview("browser", "opened http://127.0.0.1:8080/\nTitle")
	if c2.previewURL != "http://127.0.0.1:8080/" {
		t.Fatalf("browser preview = %q", c2.previewURL)
	}
}

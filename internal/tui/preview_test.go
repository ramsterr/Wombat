package tui

import (
	"strings"
	"testing"
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
}

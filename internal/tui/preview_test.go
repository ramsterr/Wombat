package tui

import (
	"os"
	"path/filepath"
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

func TestPreviewsDedupeAndOrder(t *testing.T) {
	c := &chat{w: 100, opt: Options{Cwd: t.TempDir()}}
	c.detectToolPreview("bash", "VITE v7 ready in 10 ms\nLocal: http://localhost:5173/")
	c.detectToolPreview("bash", "ready in 12 ms\nLocal: http://localhost:5173/about")
	if len(c.previews) != 1 {
		t.Fatalf("same origin should be one preview: %+v", c.previews)
	}
	if c.previewURL != "http://localhost:5173/about" {
		t.Errorf("newest URL = %q", c.previewURL)
	}
	c.detectToolPreview("bash", "serving http\nLocal: http://localhost:3000/")
	if len(c.previews) != 2 || c.previews[0].Ref != "http://localhost:3000/" {
		t.Fatalf("newest first = %+v", c.previews)
	}
	c.detectPreviewFromDetail("write", "index.html")
	c.detectPreviewFromDetail("edit", "index.html")
	if len(c.previews) != 3 {
		t.Fatalf("duplicate file previews = %+v", c.previews)
	}
	if c.previews[0].Kind != "html" {
		t.Errorf("file kind = %q", c.previews[0].Kind)
	}
}

func TestPreviewFilesFromToolOutput(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &chat{w: 100, opt: Options{Cwd: dir}}
	c.detectToolPreview("imagegen", "saved "+pdf)
	if len(c.previews) != 1 || c.previews[0].Kind != "pdf" {
		t.Fatalf("imagegen output = %+v", c.previews)
	}
	c2 := &chat{w: 100, opt: Options{Cwd: dir}}
	c2.detectToolPreview("browser", "opened file://"+pdf)
	if len(c2.previews) != 1 || c2.previews[0].Kind != "pdf" {
		t.Fatalf("browser file URL = %+v", c2.previews)
	}
}

func TestPreviewPicker(t *testing.T) {
	c := testChat(t)
	c.resize(120, 34)
	c.opt.Cwd = t.TempDir()
	c.addPreview(sitePreview("http://localhost:5173/", "bash"))
	c.addPreview(preview{Ref: "report.pdf", Kind: "pdf", Label: "report.pdf"})
	if cmd := c.openPreviews(); cmd != nil {
		t.Fatal("openPreviews should not return a command")
	}
	if c.modal != modalPreviews {
		t.Fatal("the preview picker did not open")
	}
	if card := c.previewsCard(); !strings.Contains(card, "PREVIEWS") || !strings.Contains(card, "report.pdf") {
		t.Errorf("preview card =\n%s", card)
	}
	c.previewKey(keyMsg("down"))
	if c.previewSel != 1 {
		t.Errorf("selection = %d", c.previewSel)
	}
	c.previewKey(keyMsg("esc"))
	if c.modal != modalNone {
		t.Error("escape did not close the picker")
	}
}

func TestSanitizeTerminalNoise(t *testing.T) {
	in := "hello \x1b[31mred\x1b[0m <65,80,22m world \x1b[<64;10;5M"
	out := sanitizeTerminalText(in)
	for _, bad := range []string{"\x1b", "<65", "<64"} {
		if strings.Contains(out, bad) {
			t.Errorf("sanitize left %q in %q", bad, out)
		}
	}
	for _, want := range []string{"hello", "red", "world"} {
		if !strings.Contains(out, want) {
			t.Errorf("sanitize removed %q: %q", want, out)
		}
	}
	if plainLen("\x1b[31mred\x1b[0m") != 3 {
		t.Errorf("plainLen counted escapes")
	}
}

func TestCopySelectionText(t *testing.T) {
	c := &chat{entries: []*chatEntry{
		{kind: entryUser, text: "question"},
		{kind: entryAssistant, text: "first answer"},
		{kind: entryAssistant, text: "last answer"},
	}}
	if got := c.lastAssistantText(); got != "last answer" {
		t.Errorf("lastAssistantText = %q", got)
	}
	all := c.transcriptText()
	for _, want := range []string{"You:\nquestion", "Wombat:\nfirst answer", "Wombat:\nlast answer"} {
		if !strings.Contains(all, want) {
			t.Errorf("transcript is missing %q:\n%s", want, all)
		}
	}
}

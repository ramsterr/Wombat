package llm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustAttachment(t *testing.T, path string) Attachment {
	t.Helper()
	a, err := NewAttachment(path)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAttachmentKindAndMIME(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		kind string
		mime string
	}{
		{"cat.png", "image", "image/png"},
		{"clip.mp4", "video", "video/mp4"},
		{"voice.mp3", "audio", "audio/mpeg"},
		{"notes.md", "file", "text/markdown"},
		{"paper.pdf", "file", "application/pdf"},
	}
	for _, c := range cases {
		a := mustAttachment(t, writeTemp(t, dir, c.name, "data"))
		if a.Kind != c.kind || a.MIME != c.mime {
			t.Errorf("%s: got %s/%s, want %s/%s", c.name, a.Kind, a.MIME, c.kind, c.mime)
		}
	}
}

func TestParseMentions(t *testing.T) {
	dir := t.TempDir()
	quoted := writeTemp(t, dir, "my cat.png", "png")
	plain := writeTemp(t, dir, "plain.txt", "txt")
	text, paths := ParseMentions(`describe @"` + quoted + `" and @` + plain + ` please`)
	if len(paths) != 2 {
		t.Fatalf("paths = %v", paths)
	}
	if text != "describe and please" {
		t.Fatalf("text = %q", text)
	}
	text, paths = ParseMentions("mail me at a@b.com")
	if len(paths) != 0 || text != "mail me at a@b.com" {
		t.Fatalf("email was treated as a mention: %q %v", text, paths)
	}
}

func TestOpenAIAttachmentMapping(t *testing.T) {
	dir := t.TempDir()
	img := mustAttachment(t, writeTemp(t, dir, "a.png", "png"))
	txt := mustAttachment(t, writeTemp(t, dir, "notes.txt", "hello notes"))
	msgs, err := (&openAI{}).mapMessages(Request{Messages: []Message{{
		Role: "user", Content: "look", Attachments: []Attachment{img, txt},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	parts, ok := msgs[0].Content.([]map[string]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("content = %#v", msgs[0].Content)
	}
	if parts[0]["type"] != "text" || !strings.Contains(parts[0]["text"].(string), "hello notes") {
		t.Fatalf("text part = %#v", parts[0])
	}
	if parts[1]["type"] != "image_url" {
		t.Fatalf("image part = %#v", parts[1])
	}
	_, err = (&openAI{}).mapMessages(Request{Messages: []Message{{
		Role: "user", Attachments: []Attachment{mustAttachment(t, writeTemp(t, dir, "v.mp4", "v"))},
	}}})
	if err == nil || !strings.Contains(err.Error(), "video") {
		t.Fatalf("expected video error, got %v", err)
	}
}

func TestAnthropicAttachmentMapping(t *testing.T) {
	dir := t.TempDir()
	img := mustAttachment(t, writeTemp(t, dir, "a.jpg", "jpg"))
	pdf := mustAttachment(t, writeTemp(t, dir, "p.pdf", "pdf"))
	msgs, err := (&anthropic{}).mapMessages(Request{Messages: []Message{{
		Role: "user", Content: "what is this", Attachments: []Attachment{img, pdf},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	blocks := msgs[0]["content"].([]map[string]any)
	if len(blocks) != 3 {
		t.Fatalf("blocks = %#v", blocks)
	}
	if blocks[0]["type"] != "text" || blocks[1]["type"] != "image" || blocks[2]["type"] != "document" {
		t.Fatalf("unexpected blocks: %#v", blocks)
	}
	_, err = (&anthropic{}).mapMessages(Request{Messages: []Message{{
		Role: "user", Attachments: []Attachment{mustAttachment(t, writeTemp(t, dir, "a.mp3", "a"))},
	}}})
	if err == nil {
		t.Fatal("expected audio error")
	}
}

func TestGoogleAttachmentMapping(t *testing.T) {
	dir := t.TempDir()
	vid := mustAttachment(t, writeTemp(t, dir, "clip.mp4", "video"))
	contents, err := (&google{}).mapContents([]Message{{Role: "user", Content: "watch", Attachments: []Attachment{vid}}})
	if err != nil {
		t.Fatal(err)
	}
	parts := contents[0]["parts"].([]map[string]any)
	if parts[0]["text"] != "watch" {
		t.Fatalf("parts = %#v", parts)
	}
	inline, ok := parts[1]["inline_data"].(map[string]any)
	if !ok || inline["mime_type"] != "video/mp4" {
		t.Fatalf("inline part = %#v", parts[1])
	}
}

func TestResponsesAttachmentMapping(t *testing.T) {
	dir := t.TempDir()
	pdf := mustAttachment(t, writeTemp(t, dir, "p.pdf", "pdf"))
	input, err := (&responses{}).mapInput([]Message{{Role: "user", Content: "summarize", Attachments: []Attachment{pdf}}})
	if err != nil {
		t.Fatal(err)
	}
	parts := input[0]["content"].([]map[string]any)
	if parts[0]["type"] != "input_text" || parts[1]["type"] != "input_file" {
		t.Fatalf("parts = %#v", parts)
	}
}

func TestOllamaAttachmentMapping(t *testing.T) {
	dir := t.TempDir()
	img := mustAttachment(t, writeTemp(t, dir, "a.png", "png"))
	msgs, err := (&ollama{}).mapMessages(Request{Messages: []Message{{
		Role: "user", Content: "look", Attachments: []Attachment{img},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	images, ok := msgs[0]["images"].([]string)
	if !ok || len(images) != 1 {
		t.Fatalf("images = %#v", msgs[0]["images"])
	}
}

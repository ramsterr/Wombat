package llm

import (
	"errors"
	"strings"
	"testing"
)

func TestSupportsImages(t *testing.T) {
	for _, model := range []string{"beam-free", "Beam", "longcat-2.0"} {
		if SupportsImages(model) {
			t.Errorf("%s should be text-only", model)
		}
	}
	for _, model := range []string{"deepseek-v4.1-flash", "gpt-6-luna", "claude-haiku-5-5"} {
		if !SupportsImages(model) {
			t.Errorf("%s should support images", model)
		}
	}
}

func TestTextOnlyMessagesStripsImages(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "look", Attachments: []Attachment{
			{Kind: "image", Name: "cat.png", Path: "/tmp/cat.png"},
			{Kind: "file", Name: "note.txt", Path: "/tmp/note.txt"},
		}},
	}
	got := TextOnlyMessages(msgs)
	if len(got[0].Attachments) != 1 || got[0].Attachments[0].Kind != "file" {
		t.Fatalf("attachments = %+v", got[0].Attachments)
	}
	if !strings.Contains(got[0].Content, "attachment omitted") || !strings.Contains(got[0].Content, "cat.png") {
		t.Fatalf("content = %q", got[0].Content)
	}
}

func TestIsImageUnsupported(t *testing.T) {
	err := errors.New("opencode-go: http 400: Beam supports text input only. This conversation contains an image.")
	if !IsImageUnsupported(err) {
		t.Fatal("Beam image error was not recognized")
	}
	if IsImageUnsupported(errors.New("http 429: too many requests")) {
		t.Fatal("unrelated error matched")
	}
}

package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/tools"
)

// TestFallbackNoteSurfacesModelSwitch proves the TUI fallback visibility:
// the transcript shows which model failed and which one carried the run,
// with the provider error redacted and clipped.
func TestFallbackNoteSurfacesModelSwitch(t *testing.T) {
	c := testChat(t)
	c.fallbackNote(fallbackMsg{
		from: "big-model",
		to:   "cheap-model",
		err:  errors.New("http 429: rate limited, key sk-abcdef0123456789abcdef " + strings.Repeat("x", 300)),
	})
	last := c.entries[len(c.entries)-1]
	got := last.text
	for _, want := range []string{"big-model", "cheap-model", "unavailable", "429"} {
		if !strings.Contains(got, want) {
			t.Errorf("fallback note missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "sk-abcdef0123456789abcdef") {
		t.Errorf("fallback note leaked a secret: %q", got)
	}
	if !last.isErr {
		t.Error("fallback note should be styled as a warning")
	}
	if c.retryNotice != "" {
		t.Error("fallback note should clear the retry spinner")
	}
}

// TestFallbackNoteHandlesNilError keeps the note readable when the
// provider gives no detail.
func TestFallbackNoteHandlesNilError(t *testing.T) {
	c := testChat(t)
	c.fallbackNote(fallbackMsg{from: "a", to: "b"})
	got := c.entries[len(c.entries)-1].text
	if !strings.Contains(got, "model a is unavailable — continuing on b") {
		t.Errorf("nil-error fallback note = %q", got)
	}
}

var _ = tools.RedactSecrets

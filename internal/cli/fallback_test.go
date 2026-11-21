package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/llm"
)

// TestFallbackNotifierWritesRedactedLine proves the headless fallback note:
// the bypass is visible on stderr and provider bodies are redacted.
func TestFallbackNotifierWritesRedactedLine(t *testing.T) {
	var buf bytes.Buffer
	notify := fallbackNotifier(&buf)
	notify("model-a", "model-b", &llm.HTTPError{
		Provider: "openai", StatusCode: 429, Body: "rate limited, key sk-abcdef0123456789abcdef",
	})
	line := buf.String()
	for _, want := range []string{"model-a", "model-b", "429", "rate limited"} {
		if !strings.Contains(line, want) {
			t.Errorf("fallback note missing %q: %q", want, line)
		}
	}
	if strings.Contains(line, "sk-abcdef0123456789abcdef") {
		t.Errorf("fallback note leaked a secret: %q", line)
	}
	if notify == nil {
		t.Fatal("notifier must not be nil")
	}
}

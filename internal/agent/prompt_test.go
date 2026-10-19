package agent

import (
	"strings"
	"testing"
)

func TestSystemPromptIncludesGuardrails(t *testing.T) {
	prompt := buildSystem("/tmp/project")
	for _, want := range []string{
		"Never commit .env",
		"Do not commit or push unless explicitly asked",
		"destructive or irreversible commands",
		"Never use sudo",
		"parameterize SQL",
		"secrets",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
}

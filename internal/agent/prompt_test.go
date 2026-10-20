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

func TestCraftStandardInPrompt(t *testing.T) {
	p := buildSystem("/tmp")
	for _, want := range []string{
		"Craft standard",
		"Research before you build",
		"network is available",
		"professional result",
		"Prefer proven, maintained, licensed sources",
		"Never copy a copyrighted asset",
		"licence and attribution",
		"examples/ directory",
		"Poly Haven",
		"design systems",
		"Verify before you claim it works",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("the system prompt is missing %q", want)
		}
	}
}

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
		"Quality bar",
		"best practitioners",
		"Work in parallel",
		"2-5 workers",
		"delegate_many",
		"established libraries",
		"prefer proven, licensed sources",
		"Credit what you reuse",
		"Search the web when the network allows it",
		"before you claim it works",
		"never loop",
		"Looks good",
		"action=script",
		"apply_patch",
		"lsp",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("the system prompt is missing %q", want)
		}
	}
}

func TestPromptStaysShort(t *testing.T) {
	// The system prompt is sent with every request, so it has to stay lean.
	if n := len(buildSystem("/tmp")); n > 4000 {
		t.Errorf("the system prompt is %d characters, want under 4000", n)
	}
}

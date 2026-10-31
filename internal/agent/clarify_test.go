package agent

import (
	"strings"
	"testing"
)

func TestIsVaguePrompt(t *testing.T) {
	vague := []string{"fix this", "build a website", "make it better", "do something"}
	for _, prompt := range vague {
		if !IsVaguePrompt(prompt) {
			t.Errorf("IsVaguePrompt(%q) = false, want true", prompt)
		}
	}
	clear := []string{
		"find me top 5 repos to make contributions to inorder to get a job in that company as a fresher, mostly ai engineering and software engineering",
		"add a test for slugify",
	}
	for _, prompt := range clear {
		if IsVaguePrompt(prompt) {
			t.Errorf("IsVaguePrompt(%q) = true, want false", prompt)
		}
	}
}

func TestClarifyNote(t *testing.T) {
	if note := ClarifyNote("fix this", true); !strings.Contains(note, "Ask one concise question") {
		t.Fatalf("vague interactive note = %q", note)
	}
	if note := ClarifyNote("add a test for slugify", true); note != "" {
		t.Fatalf("clear interactive note = %q, want empty", note)
	}
	if note := ClarifyNote("fix this", false); !strings.Contains(note, "No user is available") {
		t.Fatalf("headless note = %q", note)
	}
}

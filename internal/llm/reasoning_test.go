package llm

import (
	"encoding/json"
	"testing"
)

func TestTrimStaleReasoning(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "a1", Reasoning: "thought one", ReasoningItems: []json.RawMessage{json.RawMessage(`{"a":1}`)}},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "a2", Reasoning: "thought two", ReasoningItems: []json.RawMessage{json.RawMessage(`{"b":2}`)}},
	}
	trimmed := TrimStaleReasoning(msgs)
	if trimmed[1].Reasoning != "" || trimmed[1].ReasoningItems != nil {
		t.Error("older reasoning should be dropped")
	}
	if trimmed[3].Reasoning != "thought two" || len(trimmed[3].ReasoningItems) != 1 {
		t.Error("the newest reasoning must be kept")
	}
	// the original messages are untouched: the session keeps the full text
	if msgs[1].Reasoning != "thought one" {
		t.Error("the source messages were modified")
	}
}

func TestTrimStaleReasoningWithoutAssistant(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "hi"}}
	trimmed := TrimStaleReasoning(msgs)
	if len(trimmed) != 1 || trimmed[0].Content != "hi" {
		t.Errorf("trimmed = %+v", trimmed)
	}
}

func TestReasoningEffort(t *testing.T) {
	cases := map[string]string{
		"":        "",
		"off":     "minimal",
		"none":    "minimal",
		"low":     "low",
		"medium":  "medium",
		"default": "medium",
		"high":    "high",
		"bogus":   "",
	}
	for in, want := range cases {
		if got := reasoningEffort(in); got != want {
			t.Errorf("reasoningEffort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestThinkingBudget(t *testing.T) {
	if b, ok := thinkingBudget("off", 4096); !ok || b != 0 {
		t.Errorf("off = %d ok=%v", b, ok)
	}
	if b, ok := thinkingBudget("low", 4096); !ok || b != 2048 {
		t.Errorf("low = %d ok=%v", b, ok)
	}
	if _, ok := thinkingBudget("", 4096); ok {
		t.Error("an empty level should not set a budget")
	}
}

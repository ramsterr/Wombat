package llm

import "testing"

func TestNormalizeToolCallsKeepsValidBlocks(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "a", Name: "read"}, {ID: "b", Name: "grep"}}},
		{Role: "tool", ToolCallID: "a", Name: "read", Content: "one"},
		{Role: "tool", ToolCallID: "b", Name: "grep", Content: "two"},
		{Role: "assistant", Content: "done"},
	}
	got := NormalizeToolCalls(msgs)
	if len(got) != len(msgs) {
		t.Fatalf("length = %d, want %d", len(got), len(msgs))
	}
	for i := range got {
		if got[i].Role != msgs[i].Role || got[i].ToolCallID != msgs[i].ToolCallID {
			t.Errorf("message %d changed: %+v", i, got[i])
		}
	}
}

func TestNormalizeToolCallsFillsMissingResults(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "a", Name: "read"}, {ID: "b", Name: "grep"}}},
		{Role: "tool", ToolCallID: "a", Name: "read", Content: "one"},
	}
	got := NormalizeToolCalls(msgs)
	if len(got) != 4 {
		t.Fatalf("messages = %d, want 4: %+v", len(got), got)
	}
	if got[2].ToolCallID != "a" || got[3].ToolCallID != "b" {
		t.Fatalf("tool order = %q, %q", got[2].ToolCallID, got[3].ToolCallID)
	}
	if got[3].Content != interruptedToolResult {
		t.Fatalf("missing result = %q", got[3].Content)
	}
}

func TestNormalizeToolCallsMovesMediaAfterAllResults(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "a", Name: "read"}, {ID: "b", Name: "read"}}},
		{Role: "tool", ToolCallID: "a", Name: "read", Content: "one"},
		{Role: "user", Content: "look at this", Attachments: []Attachment{{Kind: "image", Name: "x.png", Path: "x.png"}}},
		{Role: "tool", ToolCallID: "b", Name: "read", Content: "two"},
	}
	got := NormalizeToolCalls(msgs)
	if len(got) != 5 {
		t.Fatalf("messages = %d, want 5: %+v", len(got), got)
	}
	if got[2].Role != "tool" || got[2].ToolCallID != "a" || got[2].Content != "one" {
		t.Fatalf("first result = %+v", got[2])
	}
	// The real result for b arrived after the media message, so a synthetic
	// result is placed before the media message to keep the block valid.
	if got[3].Role != "tool" || got[3].ToolCallID != "b" {
		t.Fatalf("missing result not repaired: %+v", got[3])
	}
	if got[4].Role != "user" || len(got[4].Attachments) != 1 {
		t.Fatalf("media message moved: %+v", got[4])
	}
}

func TestNormalizeToolCallsRepairsInterruptedTail(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "a", Name: "read"}, {ID: "b", Name: "grep"}}},
	}
	got := NormalizeToolCalls(msgs)
	if len(got) != 4 {
		t.Fatalf("messages = %d, want 4", len(got))
	}
	if got[2].Role != "tool" || got[3].Role != "tool" {
		t.Fatalf("tail = %+v", got)
	}
}

func TestNormalizeToolCallsDropsOrphans(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "go"},
		{Role: "tool", ToolCallID: "ghost", Name: "read", Content: "stale"},
		{Role: "assistant", Content: "done"},
	}
	got := NormalizeToolCalls(msgs)
	if len(got) != 2 || got[0].Role != "user" || got[1].Role != "assistant" {
		t.Fatalf("orphan survived: %+v", got)
	}
}

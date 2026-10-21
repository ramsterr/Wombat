package agent

import (
	"context"
	"testing"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
)

func TestCompactShortHistoryDoesNotPanic(t *testing.T) {
	sess, err := session.Create(t.TempDir(), "mock", "mock", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.Append(llm.Message{Role: "user", Content: "hello"})
	_ = sess.Append(llm.Message{Role: "assistant", Content: "hi"})
	prov := &fakeProvider{}
	a := &Agent{Provider: prov, Model: "mock", Sess: sess}
	if err := a.CompactNow(context.Background()); err != nil {
		t.Fatalf("compact should be a no-op on a short history: %v", err)
	}
	if prov.step != 0 {
		t.Fatalf("short history should not call the summarizer, got %d calls", prov.step)
	}
}

func TestHistoryTokensCountsRetainedReasoning(t *testing.T) {
	sess, err := session.Create(t.TempDir(), "mock", "mock", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.Append(llm.Message{Role: "user", Content: "one"})
	_ = sess.Append(llm.Message{Role: "assistant", Content: "first", Reasoning: "a long first thought"})
	_ = sess.Append(llm.Message{Role: "user", Content: "two"})
	_ = sess.Append(llm.Message{Role: "assistant", Content: "second", Reasoning: "another thought"})
	a := &Agent{Sess: sess, TrimReasoning: false}
	withReasoning := a.historyTokens()
	a.TrimReasoning = true
	trimmed := a.historyTokens()
	if withReasoning <= trimmed {
		t.Fatalf("retained reasoning should count towards the context: full=%d trimmed=%d", withReasoning, trimmed)
	}
}

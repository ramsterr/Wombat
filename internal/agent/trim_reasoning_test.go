package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

type reasoningProvider struct {
	calls    int
	requests [][]llm.Message
}

func (p *reasoningProvider) Name() string { return "mock" }
func (p *reasoningProvider) ListModels(context.Context) ([]string, error) {
	return []string{"mock"}, nil
}
func (p *reasoningProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	p.calls++
	p.requests = append(p.requests, req.Messages)
	for _, m := range req.Messages {
		if m.Reasoning == "old note" {
			return &llm.Result{Content: "ok"}, nil
		}
	}
	return nil, errors.New("opencode-go: http 400: The reasoning_content in the thinking mode must be passed back to the API")
}

func TestAgentKeepsReasoningWhenProviderRequiresIt(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.CreateAt(t.TempDir(), dir, "mock", "mock", "t")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []llm.Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "a1", Reasoning: "old note"},
		{Role: "user", Content: "two"},
		{Role: "assistant", Content: "a2"},
	} {
		if err := sess.Append(m); err != nil {
			t.Fatal(err)
		}
	}
	prov := &reasoningProvider{}
	a := &Agent{
		Provider:      prov,
		Model:         "mock",
		Tools:         tools.Registry(),
		Env:           &tools.Env{Dir: dir, Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} }},
		Sess:          sess,
		TrimReasoning: true,
	}
	var notices []string
	a.Hooks.OnNotice = func(s string) { notices = append(notices, s) }
	if err := a.Run(context.Background(), "three"); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", prov.calls)
	}
	if !strings.Contains(notices[0], "requires older reasoning") {
		t.Fatalf("notices = %v", notices)
	}
	if a.TrimReasoning {
		t.Fatal("trimming was not disabled after the provider required reasoning")
	}
	found := false
	for _, m := range prov.requests[1] {
		if m.Reasoning == "old note" {
			found = true
		}
	}
	if !found {
		t.Fatal("the retry did not include the older reasoning")
	}
}

func TestAgentTrimReasoningOffKeepsAllOldReasoning(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.CreateAt(t.TempDir(), dir, "mock", "mock", "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(llm.Message{Role: "user", Content: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(llm.Message{Role: "assistant", Content: "a1", Reasoning: "old note"}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(llm.Message{Role: "user", Content: "between"}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(llm.Message{Role: "assistant", Content: "a2"}); err != nil {
		t.Fatal(err)
	}
	prov := &reasoningProvider{}
	a := &Agent{
		Provider:      prov,
		Model:         "mock",
		Tools:         tools.Registry(),
		Env:           &tools.Env{Dir: dir, Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} }},
		Sess:          sess,
		TrimReasoning: false,
	}
	if err := a.Run(context.Background(), "two"); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", prov.calls)
	}
	found := false
	for _, m := range prov.requests[0] {
		if m.Reasoning == "old note" {
			found = true
		}
	}
	if !found {
		t.Fatal("trim_reasoning=false did not keep the old reasoning")
	}
}

func TestHistoryTokensCountsOnlySentReasoning(t *testing.T) {
	sess, err := session.CreateAt(t.TempDir(), t.TempDir(), "mock", "mock", "t")
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("reasoning words ", 200)
	msgs := []llm.Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "a1", Reasoning: long},
		{Role: "user", Content: "two"},
		{Role: "assistant", Content: "a2", Reasoning: long},
	}
	for _, m := range msgs {
		if err := sess.Append(m); err != nil {
			t.Fatal(err)
		}
	}
	a := &Agent{Sess: sess, TrimReasoning: true}
	withTrim := a.historyTokens()
	a.TrimReasoning = false
	withoutTrim := a.historyTokens()
	if withoutTrim <= withTrim {
		t.Fatalf("full history = %d, trimmed = %d; expected full > trimmed", withoutTrim, withTrim)
	}
}

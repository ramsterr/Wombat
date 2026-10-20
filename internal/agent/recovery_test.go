package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

type invalidOnceProvider struct {
	calls    int
	requests [][]llm.Message
}

func (p *invalidOnceProvider) Name() string { return "mock" }
func (p *invalidOnceProvider) ListModels(context.Context) ([]string, error) {
	return []string{"mock"}, nil
}
func (p *invalidOnceProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	p.calls++
	p.requests = append(p.requests, req.Messages)
	if p.calls == 1 {
		return nil, &llm.HTTPError{Provider: "mock", StatusCode: 400, Body: `{"error":{"type":"invalid_request_error"}}`}
	}
	return &llm.Result{Content: "recovered"}, nil
}

func testAgentWithSession(t *testing.T, prov llm.Provider, sess *session.Session) (*Agent, *tools.Env) {
	t.Helper()
	env := &tools.Env{Dir: sess.Meta.Cwd, Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} }}
	a := &Agent{Provider: prov, Model: "mock", Tools: tools.Registry(), Env: env, Sess: sess}
	return a, env
}

func TestAgentDropsPoisonedHistoryOnInvalidRequest(t *testing.T) {
	sess, err := session.CreateAt(t.TempDir(), t.TempDir(), "mock", "mock", "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(llm.Message{Role: "user", Content: "poisoned old turn"}); err != nil {
		t.Fatal(err)
	}
	prov := &invalidOnceProvider{}
	a, _ := testAgentWithSession(t, prov, sess)
	var notices []string
	a.Hooks.OnNotice = func(s string) { notices = append(notices, s) }
	if err := a.Run(context.Background(), "new task"); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", prov.calls)
	}
	for _, m := range prov.requests[1] {
		if strings.Contains(m.Content, "poisoned old turn") {
			t.Fatalf("fresh retry still carried the poisoned history: %+v", m)
		}
	}
	for _, m := range sess.Messages {
		if strings.Contains(m.Content, "poisoned old turn") {
			t.Fatalf("session still carries the poisoned history: %+v", m)
		}
	}
	if len(notices) == 0 || !strings.Contains(notices[0], "fresh context") {
		t.Fatalf("notices = %v", notices)
	}
	backups, _ := filepath.Glob(sess.Path() + ".recovered*.bak")
	if len(backups) == 0 {
		t.Fatal("no backup of the dropped conversation was written")
	}
}

type alwaysInvalidProvider struct {
	calls    int
	requests [][]llm.Message
}

func (p *alwaysInvalidProvider) Name() string { return "mock" }
func (p *alwaysInvalidProvider) ListModels(context.Context) ([]string, error) {
	return []string{"mock"}, nil
}
func (p *alwaysInvalidProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	p.calls++
	p.requests = append(p.requests, req.Messages)
	return nil, &llm.HTTPError{Provider: "mock", StatusCode: 400, Body: `{"error":{"type":"invalid_request_error"}}`}
}

func TestAgentDropsFailedPromptWhenFreshContextAlsoFails(t *testing.T) {
	sess, err := session.CreateAt(t.TempDir(), t.TempDir(), "mock", "mock", "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(llm.Message{Role: "user", Content: "good old turn"}); err != nil {
		t.Fatal(err)
	}
	prov := &alwaysInvalidProvider{}
	a, _ := testAgentWithSession(t, prov, sess)
	err = a.Run(context.Background(), "the bad new task")
	if err == nil {
		t.Fatal("expected an error")
	}
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", prov.calls)
	}
	for _, m := range sess.Messages {
		if strings.Contains(m.Content, "the bad new task") {
			t.Fatalf("failed prompt was left in the session: %+v", m)
		}
	}
	found := false
	for _, m := range sess.Messages {
		if strings.Contains(m.Content, "good old turn") {
			found = true
		}
	}
	if !found {
		t.Fatal("the last good history was lost")
	}
}

type successProvider struct {
	calls    int
	requests [][]llm.Message
}

func (p *successProvider) Name() string { return "mock" }
func (p *successProvider) ListModels(context.Context) ([]string, error) {
	return []string{"mock"}, nil
}
func (p *successProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	p.calls++
	p.requests = append(p.requests, req.Messages)
	return &llm.Result{Content: "ok"}, nil
}

func TestNextPromptWorksAfterFailedTurn(t *testing.T) {
	sess, err := session.CreateAt(t.TempDir(), t.TempDir(), "mock", "mock", "t")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := testAgentWithSession(t, &alwaysInvalidProvider{}, sess)
	if err := a.Run(context.Background(), "the prompt that fails"); err == nil {
		t.Fatal("expected the first run to fail")
	}
	for _, m := range sess.Messages {
		if strings.Contains(m.Content, "the prompt that fails") {
			t.Fatalf("failed prompt survived the recovery: %+v", m)
		}
	}
	prov := &successProvider{}
	a.Provider = prov
	if err := a.Run(context.Background(), "the next prompt"); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", prov.calls)
	}
	found := false
	for _, m := range prov.requests[0] {
		if strings.Contains(m.Content, "the next prompt") {
			found = true
		}
		if strings.Contains(m.Content, "the prompt that fails") {
			t.Fatalf("the failed prompt reached the next request: %+v", m)
		}
	}
	if !found {
		t.Fatal("the next prompt did not reach the provider")
	}
}

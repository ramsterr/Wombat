package agent

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

type turnRetryProvider struct {
	calls int
}

func (p *turnRetryProvider) Name() string { return "turn-retry" }
func (p *turnRetryProvider) ListModels(context.Context) ([]string, error) {
	return nil, nil
}
func (p *turnRetryProvider) Chat(context.Context, llm.Request, func(string)) (*llm.Result, error) {
	p.calls++
	if p.calls == 1 {
		return nil, &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
	}
	return &llm.Result{Content: "recovered"}, nil
}

func TestAgentRetriesAfterNetworkError(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	dir := t.TempDir()
	sess, err := session.Create(dir, "fake", "model", "retry")
	if err != nil {
		t.Fatal(err)
	}
	prov := &turnRetryProvider{}
	var resets atomic.Int64
	a := &Agent{
		Provider: prov,
		Model:    "model",
		Tools:    tools.Registry(),
		Sess:     sess,
		MaxSteps: 4,
		Env:      &tools.Env{Dir: dir, Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} }},
		Hooks: Hooks{
			OnReset: func() { resets.Add(1) },
		},
	}
	if err := a.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", prov.calls)
	}
	if resets.Load() != 1 {
		t.Fatalf("reset calls = %d, want 1", resets.Load())
	}
	found := false
	for _, m := range sess.Messages {
		if m.Role == "user" && strings.Contains(m.Content, "Retry the last request") {
			found = true
		}
	}
	if !found {
		t.Fatal("the retry message was not added to the session")
	}
}

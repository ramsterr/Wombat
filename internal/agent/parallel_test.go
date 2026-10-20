package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

// slowRead is a read tool that takes a fixed amount of time, so the test can
// show that independent reads overlap.
type slowRead struct {
	delay time.Duration
	mu    sync.Mutex
	calls int
}

func (t *slowRead) Name() string  { return "read" }
func (t *slowRead) Class() string { return "read" }
func (t *slowRead) Description() string {
	return "a slow read"
}
func (t *slowRead) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *slowRead) Run(context.Context, json.RawMessage, *tools.Env) (string, error) {
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	time.Sleep(t.delay)
	return "done", nil
}

type multiToolProvider struct {
	calls int
}

func (p *multiToolProvider) Name() string { return "mock" }
func (p *multiToolProvider) ListModels(context.Context) ([]string, error) {
	return []string{"mock"}, nil
}
func (p *multiToolProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	p.calls++
	if p.calls == 1 {
		calls := make([]llm.ToolCall, 0, 4)
		for i := 0; i < 4; i++ {
			calls = append(calls, llm.ToolCall{
				ID:        fmt.Sprintf("c%d", i),
				Name:      "read",
				Arguments: fmt.Sprintf(`{"path":"f%d.txt"}`, i),
			})
		}
		return &llm.Result{ToolCalls: calls}, nil
	}
	return &llm.Result{Content: "done"}, nil
}

// TestToolCallsRunInParallel checks that independent read calls overlap
// instead of running one after another.
func TestToolCallsRunInParallel(t *testing.T) {
	sess, err := session.CreateAt(t.TempDir(), t.TempDir(), "mock", "mock", "t")
	if err != nil {
		t.Fatal(err)
	}
	read := &slowRead{delay: 200 * time.Millisecond}
	prov := &multiToolProvider{}
	a := &Agent{
		Provider: prov,
		Model:    "mock",
		Tools:    []tools.Tool{read},
		Env:      &tools.Env{Dir: t.TempDir()},
		Sess:     sess,
		MaxSteps: 3,
	}
	start := time.Now()
	if err := a.Run(context.Background(), "read four files"); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if read.calls != 4 {
		t.Fatalf("calls = %d", read.calls)
	}
	// sequential would be 4 * 200ms = 800ms
	if elapsed > 600*time.Millisecond {
		t.Errorf("four reads took %s, they did not run in parallel", elapsed)
	}
	t.Logf("four 200ms reads took %s", elapsed.Round(time.Millisecond))
}

type repeatProvider struct{ calls int }

func (p *repeatProvider) Name() string { return "mock" }
func (p *repeatProvider) ListModels(context.Context) ([]string, error) {
	return []string{"mock"}, nil
}
func (p *repeatProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	p.calls++
	if p.calls <= 5 {
		return &llm.Result{ToolCalls: []llm.ToolCall{
			{ID: fmt.Sprintf("c%d", p.calls), Name: "read", Arguments: `{"path":"same.txt"}`},
		}}, nil
	}
	return &llm.Result{Content: "done"}, nil
}

// TestRepeatedCallsAreStopped checks the doom-loop guard.
func TestRepeatedCallsAreStopped(t *testing.T) {
	sess, err := session.CreateAt(t.TempDir(), t.TempDir(), "mock", "mock", "t")
	if err != nil {
		t.Fatal(err)
	}
	read := &slowRead{delay: time.Millisecond}
	prov := &repeatProvider{}
	a := &Agent{
		Provider: prov,
		Model:    "mock",
		Tools:    []tools.Tool{read},
		Env:      &tools.Env{Dir: t.TempDir()},
		Sess:     sess,
		MaxSteps: 8,
	}
	if err := a.Run(context.Background(), "read the same file over and over"); err != nil {
		t.Fatal(err)
	}
	if read.calls > maxRepeatedToolCall {
		t.Errorf("the tool ran %d times, the loop was not stopped", read.calls)
	}
	found := false
	for _, m := range sess.Messages {
		if m.Role == "tool" && len(m.Content) > 0 && strings.Contains(m.Content, "Stop repeating it") {
			found = true
		}
	}
	if !found {
		t.Error("the model was not told to stop repeating the call")
	}
}

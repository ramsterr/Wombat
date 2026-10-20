package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

type fakeProvider struct {
	step int
	reqs []llm.Request
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) ListModels(context.Context) ([]string, error) {
	return []string{"fake-model"}, nil
}

func (f *fakeProvider) Chat(_ context.Context, req llm.Request, onDelta func(string)) (*llm.Result, error) {
	f.reqs = append(f.reqs, req)
	f.step++
	switch f.step {
	case 1:
		return &llm.Result{
			Reasoning:      "plan",
			ReasoningItems: []json.RawMessage{json.RawMessage(`{"type":"reasoning","encrypted_content":"enc"}`)},
			ToolCalls:      []llm.ToolCall{{ID: "c1", Name: "write", Arguments: `{"path":"hello.txt","content":"hi"}`}},
			Usage:          llm.Usage{In: 10, Out: 5},
		}, nil
	case 2:
		if onDelta != nil {
			onDelta("done")
		}
		return &llm.Result{Content: "done", Usage: llm.Usage{In: 20, Out: 3, Cached: 4}}, nil
	default:
		return nil, fmt.Errorf("unexpected provider call %d", f.step)
	}
}

func newTestAgent(t *testing.T, permit bool) (*Agent, *fakeProvider, string, *[]string, *[]int) {
	t.Helper()
	t.Setenv("WOMBAT_HOME", t.TempDir())
	workdir := t.TempDir()
	sess, err := session.Create(workdir, "fake", "fake-model", "test")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeProvider{}
	var texts []string
	var usage []int
	a := &Agent{
		Provider: fake,
		Model:    "fake-model",
		Tools:    tools.Registry(),
		Env: &tools.Env{
			Dir:    workdir,
			Permit: func(kind, detail string) tools.Decision { return tools.Decision{Allow: permit} },
		},
		Sess:     sess,
		MaxSteps: 8,
		Hooks: Hooks{
			OnText:  func(s string) { texts = append(texts, s) },
			OnUsage: func(u llm.Usage, history int) { usage = append(usage, u.In+u.Out) },
		},
	}
	return a, fake, workdir, &texts, &usage
}

func TestAgentToolLoop(t *testing.T) {
	a, fake, workdir, texts, usage := newTestAgent(t, true)
	if err := a.Run(context.Background(), "write hello"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(workdir, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hi" {
		t.Fatalf("file content = %q", data)
	}
	if strings.Join(*texts, "") != "done" {
		t.Fatalf("texts = %v", *texts)
	}
	if len(*usage) != 2 || (*usage)[0] != 15 || (*usage)[1] != 23 {
		t.Fatalf("usage = %v", *usage)
	}
	stats := a.Usage()
	if stats.Calls != 2 || stats.In != 30 || stats.Out != 8 || stats.Cached != 4 {
		t.Fatalf("aggregated usage = %+v", stats)
	}
	if stats.Total() != 38 || stats.Duration <= 0 {
		t.Fatalf("aggregated totals = %+v", stats)
	}

	msgs := a.Sess.Messages
	if len(msgs) != 5 {
		t.Fatalf("session messages = %d, want 5", len(msgs))
	}
	if msgs[0].Role != "user" || !strings.Contains(msgs[0].Content, "Today:") {
		t.Errorf("first message should be startup context: %+v", msgs[0])
	}
	if msgs[1].Role != "user" || msgs[1].Content != "write hello" {
		t.Errorf("second message should be user input: %+v", msgs[1])
	}
	if msgs[2].Role != "assistant" || len(msgs[2].ToolCalls) != 1 {
		t.Errorf("third message should carry the tool call: %+v", msgs[2])
	}
	if msgs[2].Reasoning != "plan" || len(msgs[2].ReasoningItems) != 1 {
		t.Errorf("reasoning should be persisted with the tool call: %+v", msgs[2])
	}
	if msgs[3].Role != "tool" || msgs[3].ToolCallID != "c1" || !strings.Contains(msgs[3].Content, "hello.txt") {
		t.Errorf("fourth message should be the tool result: %+v", msgs[3])
	}
	if msgs[4].Role != "assistant" || msgs[4].Content != "done" {
		t.Errorf("fifth message should be the final answer: %+v", msgs[4])
	}

	if len(fake.reqs) != 2 {
		t.Fatalf("provider calls = %d", len(fake.reqs))
	}
	if len(fake.reqs[1].Messages) < 3 || fake.reqs[1].Messages[2].Reasoning != "plan" {
		t.Errorf("second request must replay the tool-call reasoning: %+v", fake.reqs[1].Messages)
	}
	if len(fake.reqs[0].Messages) != 2 {
		t.Errorf("first request messages = %d, want 2", len(fake.reqs[0].Messages))
	}
	if len(fake.reqs[1].Messages) != 4 {
		t.Errorf("second request messages = %d, want 4", len(fake.reqs[1].Messages))
	}
	if fake.reqs[1].Messages[2].Role != "assistant" || len(fake.reqs[1].Messages[2].ToolCalls) != 1 {
		t.Errorf("second request should include the assistant tool call")
	}
	if fake.reqs[1].Messages[3].Role != "tool" {
		t.Errorf("second request should include the tool result")
	}
	if len(fake.reqs[0].Tools) == 0 {
		t.Errorf("tools should be advertised to the provider")
	}
}

func TestAgentSystemExtraRunStartAndUsageStats(t *testing.T) {
	a, fake, _, _, _ := newTestAgent(t, true)
	a.SystemExtra = func() string { return "EXTRA_MARKER" }
	started := 0
	a.RunStart = func() { started++ }
	if err := a.Run(context.Background(), "write hello"); err != nil {
		t.Fatal(err)
	}
	if started != 1 {
		t.Fatalf("RunStart calls = %d", started)
	}
	if len(fake.reqs) < 2 {
		t.Fatalf("provider calls = %d", len(fake.reqs))
	}
	if !strings.Contains(fake.reqs[0].System, "EXTRA_MARKER") || !strings.Contains(fake.reqs[1].System, "EXTRA_MARKER") {
		t.Fatalf("system extra missing from requests")
	}
	before := a.Usage()
	a.AddUsageStats(UsageStats{Calls: 2, In: 9, Cached: 1, Out: 4})
	after := a.Usage()
	if after.Calls != before.Calls+2 || after.In != before.In+9 || after.Cached != before.Cached+1 || after.Out != before.Out+4 {
		t.Fatalf("usage after AddUsageStats = %+v (before %+v)", after, before)
	}
}

func TestAgentPermissionDenied(t *testing.T) {
	a, fake, workdir, _, _ := newTestAgent(t, false)
	if err := a.Run(context.Background(), "write hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workdir, "hello.txt")); !os.IsNotExist(err) {
		t.Fatalf("file should not exist when permission denied")
	}

	msgs := a.Sess.Messages
	if len(msgs) != 5 {
		t.Fatalf("session messages = %d", len(msgs))
	}
	if msgs[3].Role != "tool" || !strings.Contains(msgs[3].Content, "permission denied") {
		t.Fatalf("tool result should report denial: %+v", msgs[3])
	}
	if !strings.Contains(fake.reqs[1].Messages[3].Content, "permission denied") {
		t.Errorf("provider should see the denial in the next request")
	}
}

func TestAgentStopsAfterMaxSteps(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	sess, err := session.Create(t.TempDir(), "fake", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	looper := &loopProvider{}
	a := &Agent{
		Provider: looper,
		Model:    "m",
		Tools:    tools.Registry(),
		Env: &tools.Env{
			Dir:    t.TempDir(),
			Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} },
		},
		Sess:     sess,
		MaxSteps: 3,
	}
	start := time.Now()
	err = a.Run(context.Background(), "loop forever")
	if err == nil || !strings.Contains(err.Error(), "3 steps") {
		t.Fatalf("expected max-steps error, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("loop took too long")
	}
	if looper.calls != 3 {
		t.Fatalf("provider calls = %d, want 3", looper.calls)
	}
}

type loopProvider struct{ calls int }

func (l *loopProvider) Name() string { return "loop" }
func (l *loopProvider) ListModels(context.Context) ([]string, error) {
	return nil, nil
}
func (l *loopProvider) Chat(context.Context, llm.Request, func(string)) (*llm.Result, error) {
	l.calls++
	return &llm.Result{
		ToolCalls: []llm.ToolCall{{ID: fmt.Sprintf("c%d", l.calls), Name: "bash", Arguments: `{"command":"true"}`}},
	}, nil
}

type restrictedProvider struct{ calls int }

func (r *restrictedProvider) Name() string { return "restricted" }
func (r *restrictedProvider) ListModels(context.Context) ([]string, error) {
	return nil, nil
}
func (r *restrictedProvider) Chat(context.Context, llm.Request, func(string)) (*llm.Result, error) {
	r.calls++
	if r.calls == 1 {
		return &llm.Result{
			ToolCalls: []llm.ToolCall{{
				ID:        "c1",
				Name:      "bash",
				Arguments: `{"command":"touch pwned"}`,
			}},
		}, nil
	}
	return &llm.Result{Content: "done"}, nil
}

func TestAgentRejectsUnregisteredTool(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	workdir := t.TempDir()
	sess, err := session.Create(workdir, "restricted", "m", "test")
	if err != nil {
		t.Fatal(err)
	}
	prov := &restrictedProvider{}
	a := &Agent{
		Provider: prov,
		Model:    "m",
		Tools:    []tools.Tool{&tools.Read{}},
		Env: &tools.Env{
			Dir:    workdir,
			Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} },
		},
		Sess:     sess,
		MaxSteps: 4,
	}
	if err := a.Run(context.Background(), "do it"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workdir, "pwned")); !os.IsNotExist(err) {
		t.Fatalf("unregistered bash tool was executed")
	}
	if len(a.Sess.Messages) != 5 {
		t.Fatalf("messages = %d, want 5", len(a.Sess.Messages))
	}
	if got := a.Sess.Messages[3].Content; !strings.Contains(got, `unknown tool "bash"`) {
		t.Fatalf("tool result = %q", got)
	}
}

func TestTurnTokenBudgetStopsBeforeTool(t *testing.T) {
	a, _, workdir, _, _ := newTestAgent(t, true)
	a.MaxTurnTokens = 10
	err := a.Run(context.Background(), "write hello")
	if err == nil || !strings.Contains(err.Error(), "budget exhausted") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(workdir, "hello.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("tool should not run after budget exhaustion")
	}
}

func TestToolCallBudget(t *testing.T) {
	a, _, workdir, _, _ := newTestAgent(t, true)
	a.MaxToolCalls = 0
	a.MaxTurnTokens = 1000
	a.MaxToolCalls = 1
	err := a.Run(context.Background(), "write hello")
	if err != nil {
		t.Fatalf("first tool call should run: %v", err)
	}
	data, statErr := os.ReadFile(filepath.Join(workdir, "hello.txt"))
	if statErr != nil || string(data) != "hi" {
		t.Fatalf("hello.txt = %q err=%v", data, statErr)
	}
}

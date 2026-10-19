package agent

import (
	"context"
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
			ToolCalls: []llm.ToolCall{{ID: "c1", Name: "write", Arguments: `{"path":"hello.txt","content":"hi"}`}},
			Usage:     llm.Usage{In: 10, Out: 5},
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
	if msgs[3].Role != "tool" || msgs[3].ToolCallID != "c1" || !strings.Contains(msgs[3].Content, "hello.txt") {
		t.Errorf("fourth message should be the tool result: %+v", msgs[3])
	}
	if msgs[4].Role != "assistant" || msgs[4].Content != "done" {
		t.Errorf("fifth message should be the final answer: %+v", msgs[4])
	}

	if len(fake.reqs) != 2 {
		t.Fatalf("provider calls = %d", len(fake.reqs))
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

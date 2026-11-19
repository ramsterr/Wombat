package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tokens"
	"github.com/ramsterr/Wombat/internal/tools"
)

// scriptProvider plays a fixed sequence of model results and records every
// request it saw.
type scriptProvider struct {
	steps []scriptStep
	reqs  []llm.Request
}

type scriptStep struct {
	result *llm.Result
	err    error
}

func (p *scriptProvider) Name() string { return "script" }
func (p *scriptProvider) ListModels(context.Context) ([]string, error) {
	return []string{"script-model"}, nil
}
func (p *scriptProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	snapshot := req
	snapshot.Messages = append([]llm.Message(nil), req.Messages...)
	p.reqs = append(p.reqs, snapshot)
	i := len(p.reqs) - 1
	if i < len(p.steps) {
		if p.steps[i].err != nil {
			return nil, p.steps[i].err
		}
		return p.steps[i].result, nil
	}
	return &llm.Result{Content: "done"}, nil
}

func toolCall(id, name, args string) llm.ToolCall {
	return llm.ToolCall{ID: id, Name: name, Arguments: args}
}

// TestSystemPromptStableAcrossSteps proves the P0 cache fix: the system
// prompt is byte-stable while the todo list changes, and the volatile state
// rides in a trailing message instead.
func TestSystemPromptStableAcrossSteps(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	dir := t.TempDir()
	sess, err := session.Create(dir, "script", "script-model", "stable")
	if err != nil {
		t.Fatal(err)
	}
	prov := &scriptProvider{steps: []scriptStep{
		{result: &llm.Result{ToolCalls: []llm.ToolCall{toolCall("t1", "todowrite", `{"todos":[{"content":"first task","status":"completed"}]}`)}}},
		{result: &llm.Result{ToolCalls: []llm.ToolCall{toolCall("t2", "todowrite", `{"todos":[{"content":"first task","status":"completed"},{"content":"second task","status":"in_progress"}]}`)}}},
		{result: &llm.Result{Content: "done"}},
	}}
	a := &Agent{
		Provider: prov,
		Model:    "script-model",
		Tools:    tools.Registry(),
		Env: &tools.Env{
			Dir:    dir,
			Permit: func(kind, detail string) tools.Decision { return tools.Decision{Allow: true} },
		},
		Sess:     sess,
		MaxSteps: 8,
	}
	if err := a.Run(context.Background(), "plan something"); err != nil {
		t.Fatal(err)
	}
	if len(prov.reqs) != 3 {
		t.Fatalf("provider calls = %d, want 3", len(prov.reqs))
	}
	for i := 1; i < len(prov.reqs); i++ {
		if prov.reqs[i].System != prov.reqs[0].System {
			t.Fatalf("system prompt changed between step %d and step 1 (prompt cache would be lost)", i+1)
		}
	}
	// The todo state rides in the trailing message, not the system prompt.
	for i, req := range prov.reqs {
		if strings.Contains(req.System, "Current todo list") {
			t.Fatalf("step %d: todo state leaked into the system prompt", i+1)
		}
	}
	// Step 2 sees the list written by step 1; step 3 sees step 2's list.
	last := lastMessage(prov.reqs[1].Messages)
	if last.Role != "user" || !strings.Contains(last.Content, "[Task state]") || !strings.Contains(last.Content, "first task") {
		t.Fatalf("step 2 trailing state message wrong: %+v", last)
	}
	last = lastMessage(prov.reqs[2].Messages)
	if last.Role != "user" || !strings.Contains(last.Content, "[Task state]") || !strings.Contains(last.Content, "second task") {
		t.Fatalf("step 3 trailing state message wrong: %+v", last)
	}
}

// TestTokenCalibrationScalesHistoryEstimate proves the measured-usage
// feedback loop: a provider reporting 4x the heuristic estimate moves the
// history estimate to roughly 4x.
func TestTokenCalibrationScalesHistoryEstimate(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	dir := t.TempDir()
	sess, err := session.Create(dir, "script", "script-model", "calibrate")
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("read this code carefully ", 400)
	if err := sess.Append(llm.Message{Role: "user", Content: long}); err != nil {
		t.Fatal(err)
	}
	a := &Agent{
		Provider: &scriptProvider{steps: []scriptStep{
			{result: &llm.Result{Content: "ok", Usage: llm.Usage{In: 0}}},
		}},
		Model:    "script-model",
		Sess:     sess,
		Env:      &tools.Env{Dir: dir},
		MaxSteps: 8,
	}
	// The in-loop estimate is system + history before the first reply.
	est := tokens.Estimate(buildSystem(dir)) + a.historyTokens() + 8
	if est < 500 {
		t.Fatalf("test needs a larger estimate, got %d", est)
	}
	a.Provider = &scriptProvider{steps: []scriptStep{
		{result: &llm.Result{Content: "ok", Usage: llm.Usage{In: 4 * est}}},
	}}
	if err := a.Run(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	naive := (&Agent{Sess: sess}).historyTokens()
	scaled := a.historyTokens()
	if scaled < naive*2 {
		t.Fatalf("calibration did not move the estimate: naive=%d scaled=%d", naive, scaled)
	}
	if scaled > naive*4+64 {
		t.Fatalf("calibration overshot the clamp: naive=%d scaled=%d", naive, scaled)
	}
}

// TestPruneToolOutputsMasksWithRerunHint proves masking-first compaction:
// old tool outputs are replaced by a stub that says how to get them back,
// and failure output stays verbatim.
func TestPruneToolOutputsMasksWithRerunHint(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	sess, err := session.Create(t.TempDir(), "script", "script-model", "mask")
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", 120000) // ~30k estimated tokens
	msgs := []llm.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c1", "bash", `{"command":"ls -la"}`)}},
		{Role: "tool", ToolCallID: "c1", Name: "bash", Content: big},
		{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c0", "bash", `{"command":"make test"}`)}},
		{Role: "tool", ToolCallID: "c0", Name: "bash", Content: "error: " + big},
		{Role: "user", Content: "next"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c2", "grep", `{"pattern":"x"}`)}},
		{Role: "tool", ToolCallID: "c2", Name: "grep", Content: big},
		{Role: "user", Content: "final"},
	}
	for _, m := range msgs {
		if err := sess.Append(m); err != nil {
			t.Fatal(err)
		}
	}
	a := &Agent{Sess: sess, ContextWindow: 100000} // protect budget = 20k
	if a.pruneToolOutputs() <= 0 {
		t.Fatal("prune should reclaim from a 90k-token tool history")
	}
	masked := sess.Messages[2].Content
	if !strings.Contains(masked, "[elided tool output") {
		t.Fatalf("old tool output was not masked: %.80s", masked)
	}
	if !strings.Contains(masked, "re-run bash") || !strings.Contains(masked, "ls -la") {
		t.Fatalf("mask stub must say how to re-run the call: %.120s", masked)
	}
	if got := sess.Messages[4].Content; !strings.HasPrefix(got, "error:") || len(got) != len("error: ")+len(big) {
		t.Fatal("failure output must stay verbatim")
	}
}

// TestSteerMessageJoinsBetweenSteps proves mid-run steering: a message
// queued while the model is working is appended after the current tool
// batch and is visible to the next request.
func TestSteerMessageJoinsBetweenSteps(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	dir := t.TempDir()
	sess, err := session.Create(dir, "script", "script-model", "steer")
	if err != nil {
		t.Fatal(err)
	}
	prov := &scriptProvider{steps: []scriptStep{
		{result: &llm.Result{ToolCalls: []llm.ToolCall{toolCall("g1", "glob", `{"pattern":"*.go"}`)}}},
		{result: &llm.Result{Content: "done"}},
	}}
	wrapped := &steeringProvider{prov: prov}
	a := &Agent{
		Provider: wrapped,
		Model:    "script-model",
		Tools:    tools.Registry(),
		Env: &tools.Env{
			Dir:    dir,
			Permit: func(kind, detail string) tools.Decision { return tools.Decision{Allow: true} },
		},
		Sess:     sess,
		MaxSteps: 8,
	}
	wrapped.agent = a
	if err := a.Run(context.Background(), "explore"); err != nil {
		t.Fatal(err)
	}
	if len(prov.reqs) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(prov.reqs))
	}
	// The steering message landed after the tool result, before the last reply.
	steerIdx := -1
	toolIdx := -1
	for i, m := range sess.Messages {
		if m.Role == "tool" && m.ToolCallID == "g1" {
			toolIdx = i
		}
		if m.Role == "user" && strings.Contains(m.Content, "use postgres") {
			steerIdx = i
		}
	}
	if steerIdx < 0 {
		t.Fatal("steering message never joined the session")
	}
	if toolIdx < 0 || steerIdx < toolIdx {
		t.Fatalf("steering message at %d must come after the tool result at %d", steerIdx, toolIdx)
	}
	found := false
	for _, m := range prov.reqs[1].Messages {
		if m.Role == "user" && strings.Contains(m.Content, "use postgres") {
			found = true
		}
	}
	if !found {
		t.Fatal("the second request never saw the steering message")
	}
}

// steeringProvider queues a steering message through the agent while the
// first model call runs, the way the TUI does from another goroutine.
type steeringProvider struct {
	prov  *scriptProvider
	agent *Agent
	once  sync.Once
}

func (p *steeringProvider) Name() string                                 { return "steer" }
func (p *steeringProvider) ListModels(context.Context) ([]string, error) { return []string{"m"}, nil }
func (p *steeringProvider) Chat(ctx context.Context, req llm.Request, d func(string)) (*llm.Result, error) {
	p.once.Do(func() { p.agent.Steer("use postgres for the database") })
	return p.prov.Chat(ctx, req, d)
}

// TestTurnRetryDelayProvesBackoff proves the retry policy: Retry-After
// wins exactly, plain errors grow exponentially, and everything is capped.
func TestTurnRetryDelayProvesBackoff(t *testing.T) {
	got := turnRetryDelay(1, &llm.HTTPError{RetryAfter: 7 * time.Second})
	if got != 7*time.Second {
		t.Fatalf("Retry-After must win: got %v", got)
	}
	// A cap keeps a hostile Retry-After from stalling the run forever.
	got = turnRetryDelay(1, &llm.HTTPError{RetryAfter: 5 * time.Minute})
	if got != retryMaxDelay {
		t.Fatalf("Retry-After must be capped at %v: got %v", retryMaxDelay, got)
	}
	err := &llm.HTTPError{StatusCode: 500}
	for attempt, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second} {
		d := turnRetryDelay(attempt, err)
		if d < want/2 || d >= want {
			t.Fatalf("attempt %d: backoff %v outside [%v, %v)", attempt, d, want/2, want)
		}
	}
	d := turnRetryDelay(10, err)
	if d < retryMaxDelay/2 || d >= retryMaxDelay {
		t.Fatalf("backoff must cap at %v: got %v", retryMaxDelay, d)
	}
}

func lastMessage(msgs []llm.Message) llm.Message {
	return msgs[len(msgs)-1]
}

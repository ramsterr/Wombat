package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

// pickyProvider rejects any request whose messages contain an image, the way
// a text-only model does.
type pickyProvider struct {
	calls     int
	rejected  int
	lastError error
}

func (p *pickyProvider) Name() string { return "mock" }
func (p *pickyProvider) ListModels(context.Context) ([]string, error) {
	return []string{"mock"}, nil
}
func (p *pickyProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	p.calls++
	for _, m := range req.Messages {
		if len(m.Attachments) > 0 {
			p.rejected++
			p.lastError = &llm.HTTPError{
				Provider:   "mock",
				StatusCode: 400,
				Body:       "[invalid_value] Beam supports text input only. This conversation contains an image.",
			}
			return nil, p.lastError
		}
	}
	return &llm.Result{Content: "ok"}, nil
}

// TestSessionRecoversFromRejectedRequest checks that a request the provider
// refuses is repaired, and that the next prompt works instead of failing the
// same way.
func TestSessionRecoversFromRejectedRequest(t *testing.T) {
	sess, err := session.Create(t.TempDir(), "mock", "deepseek-v4-flash", "")
	if err != nil {
		t.Fatal(err)
	}
	prov := &pickyProvider{}
	a := &Agent{
		Provider: prov,
		Model:    "deepseek-v4-flash",
		Tools:    tools.Registry(),
		Env: &tools.Env{
			Dir:    t.TempDir(),
			Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} },
		},
		Sess: sess,
	}
	// put an image in the history, the way a previous turn would have
	if err := sess.Append(llm.Message{
		Role:        "user",
		Content:     "here is the picture",
		Attachments: []llm.Attachment{{Kind: "image", MIME: "image/png", Name: "p.png", Path: "/tmp/p.png"}},
	}); err != nil {
		t.Fatal(err)
	}

	err = a.Run(context.Background(), "carry on")
	if err != nil {
		t.Fatalf("the run should recover from the rejected request: %v", err)
	}
	if prov.rejected == 0 {
		t.Fatal("the provider should have rejected the first request")
	}
	// the image must be gone from the history
	for _, m := range sess.Messages {
		if len(m.Attachments) > 0 {
			t.Fatal("the image was not removed from the history")
		}
	}
	// and a later prompt must work
	if err := a.Run(context.Background(), "still there?"); err != nil {
		t.Fatalf("the next prompt should work: %v", err)
	}
}

// TestUnansweredToolCallIsDropped checks the repair of a broken tool pair.
func TestUnansweredToolCallIsDropped(t *testing.T) {
	sess, err := session.Create(t.TempDir(), "mock", "gpt-5", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(llm.Message{
		Role:      "assistant",
		ToolCalls: []llm.ToolCall{{ID: "t1", Name: "read", Arguments: `{"path":"x"}`}},
	}); err != nil {
		t.Fatal(err)
	}
	a := &Agent{Provider: &pickyProvider{}, Model: "gpt-5", Sess: sess}
	repaired, note := a.repairHistory()
	if !repaired {
		t.Fatal("the unanswered tool call should have been repaired")
	}
	if !strings.Contains(note, "dropped") {
		t.Errorf("note = %q", note)
	}
	for _, m := range sess.Messages {
		if len(m.ToolCalls) > 0 {
			t.Fatal("the unfinished tool call is still in the history")
		}
	}
}

func TestInvalidRequestDetection(t *testing.T) {
	if !llm.InvalidRequest(&llm.HTTPError{StatusCode: 400}) {
		t.Error("a 400 should be an invalid request")
	}
	if !llm.InvalidRequest(errors.New("invalid_value: something")) {
		t.Error("invalid_value should be an invalid request")
	}
	if llm.InvalidRequest(errors.New("connection refused")) {
		t.Error("a network error is not an invalid request")
	}
	if llm.InvalidRequest(nil) {
		t.Error("nil is not an invalid request")
	}
	fmt.Println("classification ok")
}

type reasoningProvider struct {
	calls    int
	rejected int
}

func (p *reasoningProvider) Name() string { return "reasoning-mock" }
func (p *reasoningProvider) ListModels(context.Context) ([]string, error) {
	return []string{"reasoning-mock"}, nil
}
func (p *reasoningProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	p.calls++
	for _, m := range req.Messages {
		if m.Role == "assistant" && strings.TrimSpace(m.Reasoning) == "" && (m.Content != "" || len(m.ToolCalls) > 0) {
			p.rejected++
			return nil, &llm.HTTPError{
				Provider:   "mock",
				StatusCode: 400,
				Body:       `{"error":{"type":"invalid_request_error","message":"The reasoning_content in the thinking mode must be passed back to the API."}}`,
			}
		}
	}
	return &llm.Result{Content: "ok"}, nil
}

func TestSessionRepairsMissingReasoning(t *testing.T) {
	sess, err := session.Create(t.TempDir(), "reasoning-mock", "deepseek-v4.1-flash", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(llm.Message{Role: "user", Content: "run it"}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(llm.Message{
		Role: "assistant", Content: "calling", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"a"}`}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(llm.Message{Role: "tool", ToolCallID: "c1", Name: "read", Content: "contents"}); err != nil {
		t.Fatal(err)
	}
	prov := &reasoningProvider{}
	a := &Agent{
		Provider: prov,
		Model:    "deepseek-v4.1-flash",
		Tools:    tools.Registry(),
		Env: &tools.Env{
			Dir:    t.TempDir(),
			Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} },
		},
		Sess: sess,
	}
	if err := a.Run(context.Background(), "continue"); err != nil {
		t.Fatalf("run should recover: %v", err)
	}
	if prov.rejected == 0 || prov.calls < 2 {
		t.Fatalf("provider was not retried after repair: calls=%d rejected=%d", prov.calls, prov.rejected)
	}
	for _, m := range sess.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 && m.Reasoning == "" {
			t.Fatalf("missing-reasoning turn is still in history: %+v", m)
		}
	}
}

func TestReasoningRejectionDisablesTrimming(t *testing.T) {
	sess, err := session.Create(t.TempDir(), "reasoning-mock", "deepseek-v4.1-flash", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(llm.Message{Role: "user", Content: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(llm.Message{Role: "assistant", Content: "old answer", Reasoning: "old thoughts"}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(llm.Message{Role: "user", Content: "second"}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(llm.Message{Role: "assistant", Content: "newer answer", Reasoning: "newer thoughts"}); err != nil {
		t.Fatal(err)
	}
	prov := &reasoningProvider{}
	a := &Agent{
		Provider:      prov,
		Model:         "deepseek-v4.1-flash",
		Tools:         tools.Registry(),
		Env:           &tools.Env{Dir: t.TempDir(), Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} }},
		Sess:          sess,
		TrimReasoning: true,
	}
	if err := a.Run(context.Background(), "third"); err != nil {
		t.Fatalf("run should recover: %v", err)
	}
	if a.TrimReasoning {
		t.Fatal("reasoning rejection should disable trimming for the session")
	}
}

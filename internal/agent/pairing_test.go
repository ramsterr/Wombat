package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

// mediaProvider returns two tool calls in one turn: one reads a file, the
// other reads an image, which the agent hands back to the model.
type mediaProvider struct {
	calls int
}

func (p *mediaProvider) Name() string { return "mock" }
func (p *mediaProvider) ListModels(context.Context) ([]string, error) {
	return []string{"mock"}, nil
}
func (p *mediaProvider) Chat(_ context.Context, _ llm.Request, _ func(string)) (*llm.Result, error) {
	p.calls++
	if p.calls == 1 {
		return &llm.Result{ToolCalls: []llm.ToolCall{
			{ID: "t1", Name: "read", Arguments: `{"path":"notes.txt"}`},
			{ID: "t2", Name: "read", Arguments: `{"path":"pic.png"}`},
		}}, nil
	}
	return &llm.Result{Content: "done"}, nil
}

// TestMediaDoesNotBreakToolPairing checks that a file the agent produced is
// attached after every tool result of the turn. A user message in the middle
// of a tool_call/tool_result pair makes the API reject the request.
func TestMediaDoesNotBreakToolPairing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := session.Create(t.TempDir(), "mock", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	prov := &mediaProvider{}
	a := &Agent{
		Provider: prov,
		Model:    "m",
		Tools:    tools.Registry(),
		Env: &tools.Env{
			Dir:    dir,
			Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} },
		},
		Sess: sess,
	}
	if err := a.Run(context.Background(), "look at the picture"); err != nil {
		t.Fatal(err)
	}

	// every tool_call must be answered by a tool message, in order, before
	// any other message appears.
	pending := map[string]bool{}
	for _, m := range sess.Messages {
		switch m.Role {
		case "assistant":
			for _, tc := range m.ToolCalls {
				pending[tc.ID] = true
			}
		case "tool":
			if !pending[m.ToolCallID] {
				t.Fatalf("a tool message for %q does not follow its tool call", m.ToolCallID)
			}
			delete(pending, m.ToolCallID)
		default:
			if len(pending) > 0 {
				t.Fatalf("a %s message interrupts %d unanswered tool calls", m.Role, len(pending))
			}
		}
	}
	if len(pending) > 0 {
		t.Fatalf("%d tool calls were never answered", len(pending))
	}

	// the image must still reach the model
	found := false
	for _, m := range sess.Messages {
		for _, at := range m.Attachments {
			if at.Kind == "image" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("the image never reached the model")
	}
	b, _ := json.Marshal(sess.Messages)
	t.Logf("session: %s", b)
}

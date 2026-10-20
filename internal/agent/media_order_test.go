package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

type twoReadsProvider struct {
	calls int
	seen  [][]llm.Message
}

func (p *twoReadsProvider) Name() string { return "mock" }
func (p *twoReadsProvider) ListModels(context.Context) ([]string, error) {
	return []string{"mock"}, nil
}
func (p *twoReadsProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	p.seen = append(p.seen, req.Messages)
	p.calls++
	if p.calls == 1 {
		return &llm.Result{ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "read", Arguments: `{"path":"cat.png"}`},
			{ID: "2", Name: "read", Arguments: `{"path":"note.txt"}`},
		}}, nil
	}
	return &llm.Result{Content: "done"}, nil
}

// TestMediaAttachmentFollowsAllToolResults checks that an image handed back
// by one tool does not put a user message between the tool results of the
// same assistant turn.
func TestMediaAttachmentFollowsAllToolResults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cat.png"), tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := session.CreateAt(t.TempDir(), dir, "mock", "mock", "t")
	if err != nil {
		t.Fatal(err)
	}
	prov := &twoReadsProvider{}
	a := &Agent{
		Provider: prov,
		Model:    "mock",
		Tools:    tools.Registry(),
		Env: &tools.Env{Dir: dir, Permit: func(string, string) tools.Decision {
			return tools.Decision{Allow: true}
		}},
		Sess: sess,
	}
	if err := a.Run(context.Background(), "look at both"); err != nil {
		t.Fatal(err)
	}

	msgs := sess.Messages
	idx := -1
	for i, m := range msgs {
		if m.Role == "assistant" && len(m.ToolCalls) == 2 {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("assistant tool-call turn not found")
	}
	if idx+3 >= len(msgs) {
		t.Fatalf("not enough messages after tool calls: %+v", msgs[idx:])
	}
	if msgs[idx+1].Role != "tool" || msgs[idx+2].Role != "tool" {
		t.Fatalf("tool results are not adjacent: %+v", msgs[idx:idx+4])
	}
	if msgs[idx+3].Role != "user" || len(msgs[idx+3].Attachments) == 0 {
		t.Fatalf("media message is not after the tool results: %+v", msgs[idx:idx+4])
	}

	// The provider must also see the same valid order.
	if len(prov.seen) < 2 {
		t.Fatal("provider did not get a second request")
	}
	req := prov.seen[1]
	for i, m := range req {
		if m.Role == "user" && len(m.Attachments) > 0 {
			if i < 2 || req[i-1].Role != "tool" || req[i-2].Role != "tool" {
				t.Fatalf("request has media before all tool results: %+v", req)
			}
		}
	}
}

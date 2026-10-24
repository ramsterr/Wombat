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

// tinyPNG is a 1x1 PNG, enough to exercise the image path.
var tinyPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13, 'I', 'H', 'D', 'R',
	0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89, 0, 0, 0, 13,
	'I', 'D', 'A', 'T', 0x78, 0x9c, 0x63, 0, 1, 0, 0, 5, 0, 1, 0x0d, 0x0a, 0x2d,
	0xb4, 0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

type imageProvider struct {
	seen  []llm.Message
	calls int
}

func (p *imageProvider) Name() string { return "mock" }
func (p *imageProvider) ListModels(context.Context) ([]string, error) {
	return []string{"mock"}, nil
}
func (p *imageProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	p.seen = req.Messages
	p.calls++
	if p.calls == 1 {
		return &llm.Result{ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "read", Arguments: `{"path":"cat.png"}`},
		}}, nil
	}
	return &llm.Result{Content: "I looked at the cat."}, nil
}

// TestImageReachesTheModel checks that reading an image hands it to the model
// as an attachment, the way a pasted image arrives.
func TestImageReachesTheModel(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "cat.png")
	if err := os.WriteFile(img, tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := session.CreateAt(t.TempDir(), dir, "mock", "mock", "t")
	if err != nil {
		t.Fatal(err)
	}
	prov := &imageProvider{}
	a := &Agent{
		Provider: prov,
		Model:    "gpt-5",
		Tools:    tools.Registry(),
		Env: &tools.Env{Dir: dir, Permit: func(string, string) tools.Decision {
			return tools.Decision{Allow: true}
		}},
		Sess:     sess,
		MaxSteps: 4,
	}
	if err := a.Run(context.Background(), "look at the cat"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range prov.seen {
		for _, at := range m.Attachments {
			if at.Kind != "image" || at.Name != "cat.png" {
				continue
			}
			if _, err := llm.LoadAttachment(at); err != nil {
				t.Fatalf("attachment cannot be loaded: %v", err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("the image never reached the model")
	}
}

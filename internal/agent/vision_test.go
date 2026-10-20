package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

type imageRejectProvider struct {
	calls    int
	requests [][]llm.Message
}

func (p *imageRejectProvider) Name() string { return "mock" }
func (p *imageRejectProvider) ListModels(context.Context) ([]string, error) {
	return []string{"beam-free"}, nil
}
func (p *imageRejectProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	p.calls++
	p.requests = append(p.requests, req.Messages)
	if hasImage(req.Messages) {
		return nil, errors.New("opencode-go: http 400: Beam supports text input only. This conversation contains an image.")
	}
	return &llm.Result{Content: "text only ok"}, nil
}

func hasImage(msgs []llm.Message) bool {
	for _, m := range msgs {
		for _, at := range m.Attachments {
			if at.Kind == "image" {
				return true
			}
		}
	}
	return false
}

func TestAgentRetriesWithoutImagesForTextOnlyModel(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.CreateAt(t.TempDir(), dir, "mock", "beam-free", "t")
	if err != nil {
		t.Fatal(err)
	}
	prov := &imageRejectProvider{}
	env := &tools.Env{Dir: dir, Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} }}
	a := &Agent{Provider: prov, Model: "beam-free", Tools: tools.Registry(), Env: env, Sess: sess}
	err = a.RunWithAttachments(context.Background(), "look", []llm.Attachment{{Kind: "image", Name: "cat.png", Path: dir + "/cat.png"}})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 || len(prov.requests) != 2 {
		t.Fatalf("provider calls = %d", prov.calls)
	}
	if !hasImage(prov.requests[0]) {
		t.Fatal("the first request should have carried the image")
	}
	if hasImage(prov.requests[1]) {
		t.Fatal("the retry still carried an image")
	}
	if !a.NoVision || !env.NoImages {
		t.Fatal("the agent did not switch to text-only mode")
	}
	found := false
	for _, m := range prov.requests[1] {
		if strings.Contains(m.Content, "attachment omitted") {
			found = true
		}
	}
	if !found {
		t.Fatal("the model was not told the image was omitted")
	}
}

func TestAgentSkipsImagesWhenNoVision(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.CreateAt(t.TempDir(), dir, "mock", "beam-free", "t")
	if err != nil {
		t.Fatal(err)
	}
	prov := &imageRejectProvider{}
	a := &Agent{Provider: prov, Model: "beam-free", Tools: tools.Registry(),
		Env:  &tools.Env{Dir: dir, Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} }, NoImages: true},
		Sess: sess, NoVision: true}
	if err := a.RunWithAttachments(context.Background(), "look", []llm.Attachment{{Kind: "image", Name: "cat.png", Path: dir + "/cat.png"}}); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", prov.calls)
	}
	for _, m := range prov.requests[0] {
		if len(m.Attachments) > 0 {
			t.Fatalf("image sent to a text-only model: %+v", m)
		}
	}
}

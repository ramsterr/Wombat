package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/tools"
)

type capabilityProvider struct {
	calls      int
	sawBrowser bool
}

func (p *capabilityProvider) Name() string { return "capability-fake" }
func (p *capabilityProvider) ListModels(context.Context) ([]string, error) {
	return []string{"capability-fake"}, nil
}
func (p *capabilityProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	p.calls++
	if p.calls == 1 {
		return &llm.Result{
			ToolCalls: []llm.ToolCall{{
				ID:        "cap1",
				Name:      "request_capability",
				Arguments: `{"capability":"web","reason":"need a page"}`,
			}},
			Usage: llm.Usage{In: 10, Out: 5},
		}, nil
	}
	for _, tool := range req.Tools {
		if tool.Name == "browser" {
			p.sawBrowser = true
		}
	}
	return &llm.Result{Content: "done", Usage: llm.Usage{In: 20, Out: 2}}, nil
}

func TestAgentLoadsCapabilityTools(t *testing.T) {
	a, _, _, _, _ := newTestAgent(t, true)
	provider := &capabilityProvider{}
	a.Provider = provider
	a.Tools = tools.Registry()
	a.Env.SetCapabilityLoader(func(name string) ([]tools.Tool, error) {
		if name != "web" {
			return nil, fmt.Errorf("unexpected capability %q", name)
		}
		return []tools.Tool{&tools.Browser{}}, nil
	}, a.Tools)
	if err := a.Run(context.Background(), "load web"); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	if !provider.sawBrowser {
		t.Fatal("second request did not include the loaded browser tool")
	}
	raw, _ := json.Marshal(map[string]string{"capability": "web", "reason": "again"})
	if _, err := (&tools.RequestCapability{}).Run(context.Background(), raw, a.Env); err == nil {
		t.Fatal("loading the same capability twice should fail")
	}
}

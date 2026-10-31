package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func names(tools []Tool) map[string]bool {
	out := map[string]bool{}
	for _, t := range tools {
		if t != nil {
			out[t.Name()] = true
		}
	}
	return out
}

func TestResolveCapabilitiesAuto(t *testing.T) {
	caps := ResolveCapabilities("auto", "check https://example.com and fix the page", nil, true)
	if !HasCapability(caps, CapCore) || !HasCapability(caps, CapEdit) || !HasCapability(caps, CapWeb) {
		t.Fatalf("caps = %v, want core+edit+web", caps)
	}
	if !HasCapability(caps, CapOrchestrate) {
		t.Fatalf("auto with orchestrator should include orchestrate: %v", caps)
	}
	off := ResolveCapabilities("auto", "check https://example.com", map[string]string{"web": "off"}, true)
	if HasCapability(off, CapWeb) {
		t.Fatalf("web should be off: %v", off)
	}
	explicit := ResolveCapabilities("minimal", "test and build", nil, true)
	if HasCapability(explicit, CapVerify) || HasCapability(explicit, CapWeb) || HasCapability(explicit, CapOrchestrate) {
		t.Fatalf("minimal should stay core-only: %v", explicit)
	}
}

func TestRegistryDedup(t *testing.T) {
	all := names(Registry())
	if len(all) != 18 {
		t.Fatalf("registry has %d tools, want 18: %v", len(all), all)
	}
	for _, removed := range []string{
		"multi_edit", "replace", "rename", "refactor", "symbols",
		"semantic_search", "git_status", "git_diff", "git_log",
		"git_show", "git_blame", "imagegen", "verify_site", "batch", "jobs",
	} {
		if all[removed] {
			t.Errorf("registry still contains removed tool %q", removed)
		}
	}
	coding := names(FilterTools(Registry(), []Capability{CapCore, CapEdit}, false, false))
	if len(coding) != 9 {
		t.Fatalf("coding profile has %d tools, want 9: %v", len(coding), coding)
	}
}

func TestFilterTools(t *testing.T) {
	all := RegistryWithQuestion()
	coding := FilterTools(all, []Capability{CapCore, CapEdit}, false, true)
	got := names(coding)
	for _, want := range []string{"read", "write", "edit", "bash", "glob", "grep", "apply_patch", "tool_search", "request_capability"} {
		if !got[want] {
			t.Errorf("coding profile missing %q", want)
		}
	}
	for _, unwanted := range []string{"browser", "imagegen", "question", "git_status", "lsp"} {
		if got[unwanted] {
			t.Errorf("coding profile should not include %q", unwanted)
		}
	}
	interactive := FilterTools(all, []Capability{CapCore}, true, false)
	if !names(interactive)["question"] {
		t.Error("interactive profile should include question")
	}
	web := names(FilterTools(all, []Capability{CapCore, CapWeb}, false, false))
	if !web["browser"] || !web["websearch"] || !web["webfetch"] {
		t.Errorf("web capability missing tools: %v", web)
	}
}

func TestToolsForCapability(t *testing.T) {
	all := Registry()
	tools, err := ToolsForCapability(all, "web")
	if err != nil {
		t.Fatal(err)
	}
	got := names(tools)
	for _, want := range []string{"browser", "websearch", "webfetch"} {
		if !got[want] {
			t.Errorf("web capability missing %q", want)
		}
	}
	if _, err := ToolsForCapability(all, "imagegen"); err == nil {
		t.Error("imagegen must not be a loadable capability")
	}
}

func TestCapabilityNote(t *testing.T) {
	note := CapabilityNote([]Capability{CapCore, CapEdit})
	for _, want := range []string{"web", "semantic", "verify", "media", "request_capability"} {
		if !strings.Contains(note, want) {
			t.Errorf("note missing %q: %s", want, note)
		}
	}
	if CapabilityNote([]Capability{CapFull}) != "" {
		t.Error("full profile should not show an optional-capability note")
	}
}

func TestSearchCapabilities(t *testing.T) {
	for query, want := range map[string]Capability{
		"browser": CapWeb,
		"verify":  CapVerify,
		"symbol":  CapSemantic,
		"image":   CapMedia,
	} {
		matches := SearchCapabilities(query)
		found := false
		for _, match := range matches {
			if match.Name == want {
				found = true
			}
		}
		if !found {
			t.Errorf("search %q = %v, want %s", query, matches, want)
		}
	}
}

func TestRequestCapabilityLoadsOnce(t *testing.T) {
	env := &Env{Dir: t.TempDir()}
	env.SetCapabilityLoader(func(name string) ([]Tool, error) {
		if name != "web" {
			return nil, context.Canceled
		}
		return []Tool{&Browser{}}, nil
	}, []Tool{&Read{}})
	n, err := env.RequestCapability("web")
	if err != nil || n != 1 {
		t.Fatalf("RequestCapability = %d, %v", n, err)
	}
	pending := env.TakePendingTools()
	if len(pending) != 1 || pending[0].Name() != "browser" {
		t.Fatalf("pending = %v", pending)
	}
	if len(env.TakePendingTools()) != 0 {
		t.Fatal("pending tools should be cleared")
	}
	if !env.CapabilityLoaded("web") {
		t.Fatal("web should be marked loaded")
	}
	if _, err := env.RequestCapability("web"); err == nil {
		t.Fatal("loading the same capability twice should fail")
	}
}

func TestRequestCapabilityTool(t *testing.T) {
	env := &Env{Dir: t.TempDir()}
	env.SetCapabilityLoader(func(string) ([]Tool, error) {
		return []Tool{&Browser{}}, nil
	}, nil)
	raw, _ := json.Marshal(map[string]string{"capability": "web", "reason": "inspect page"})
	out, err := (&RequestCapability{}).Run(context.Background(), raw, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Loaded capability") {
		t.Fatalf("out = %q", out)
	}
}

func TestToolSearchTool(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{"query": "browser"})
	out, err := (&ToolSearch{}).Run(context.Background(), raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "web") || !strings.Contains(out, "browser") {
		t.Fatalf("out = %q", out)
	}
}

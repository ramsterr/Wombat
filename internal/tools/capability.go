package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

type capabilityState struct {
	mu      sync.Mutex
	loader  func(string) ([]Tool, error)
	pending []Tool
	loaded  map[string]bool
	names   map[string]bool
}

// SetCapabilityLoader installs the loader used by request_capability. initial
// is the tool set already declared to the model; duplicates are skipped.
func (e *Env) SetCapabilityLoader(loader func(string) ([]Tool, error), initial []Tool) {
	if e == nil {
		return
	}
	state := &capabilityState{
		loader: loader,
		loaded: map[string]bool{},
		names:  map[string]bool{},
	}
	for _, tool := range initial {
		if tool != nil {
			state.names[tool.Name()] = true
		}
	}
	e.capState = state
}

// RequestCapability loads an optional capability once. The tools are queued;
// the agent appends them to its tool list before the next model call.
func (e *Env) RequestCapability(name string) (int, error) {
	if e == nil || e.capState == nil || e.capState.loader == nil {
		return 0, fmt.Errorf("capability loading is unavailable in this session")
	}
	state := e.capState
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return 0, fmt.Errorf("capability name is required")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.loaded[key] {
		return 0, fmt.Errorf("capability %q is already loaded", key)
	}
	loaded, err := state.loader(key)
	if err != nil {
		return 0, err
	}
	added := 0
	for _, tool := range loaded {
		if tool == nil || state.names[tool.Name()] {
			continue
		}
		state.pending = append(state.pending, tool)
		state.names[tool.Name()] = true
		added++
	}
	state.loaded[key] = true
	return added, nil
}

// TakePendingTools returns and clears tools requested since the last call.
func (e *Env) TakePendingTools() []Tool {
	if e == nil || e.capState == nil {
		return nil
	}
	state := e.capState
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.pending) == 0 {
		return nil
	}
	out := state.pending
	state.pending = nil
	return out
}

// CapabilityLoaded reports whether the model has already requested name.
func (e *Env) CapabilityLoaded(name string) bool {
	if e == nil || e.capState == nil {
		return false
	}
	state := e.capState
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.loaded[strings.ToLower(strings.TrimSpace(name))]
}

// RequestCapability is the model-facing capability loader.
type RequestCapability struct{}

func (t *RequestCapability) Name() string  { return "request_capability" }
func (t *RequestCapability) Class() string { return "read" }
func (t *RequestCapability) Description() string {
	return "Load an optional capability when the task needs tools that are not currently available. Available: web (browser/web search/fetch), semantic (LSP/symbols), git (status/diff), verify (tests/build/lint), media (image search). Use tool_search if you are unsure which one. Load a capability once; its tools are available on your next message."
}
func (t *RequestCapability) Schema() map[string]any {
	return obj(map[string]any{
		"capability": map[string]any{
			"type":        "string",
			"enum":        CapabilityNames(),
			"description": "optional capability to load",
		},
		"reason": desc("why this capability is needed for the current task"),
	}, "capability", "reason")
}
func (t *RequestCapability) Run(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
	var a struct {
		Capability string `json:"capability"`
		Reason     string `json:"reason"`
	}
	if err := decodeInto(raw, &a); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if strings.TrimSpace(a.Reason) == "" {
		return "", fmt.Errorf("reason is required")
	}
	added, err := env.RequestCapability(a.Capability)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Loaded capability %q (%d new tools). They are available on your next message. Reason: %s", a.Capability, added, strings.TrimSpace(a.Reason)), nil
}
func (t *RequestCapability) AllowedInWorkers() bool { return false }

package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ramsterr/Wombat/internal/modelinfo"
)

// openCode routes models on the OpenCode Zen / Go gateways to the protocol
// each model is served on: OpenAI chat completions, Anthropic messages,
// OpenAI responses, or Google generateContent. Models are matched by family;
// unknown models start on chat completions and fall back to the other
// protocols when the gateway reports a protocol mismatch.
type openCode struct {
	name     string
	base     string
	key      string
	http     *http.Client
	mu       sync.Mutex
	resolved map[string]string
}

func newOpenCode(name, base, key string) *openCode {
	return &openCode{
		name:     name,
		base:     strings.TrimRight(base, "/"),
		key:      key,
		http:     &http.Client{Timeout: 2 * time.Minute},
		resolved: map[string]string{},
	}
}

func (o *openCode) Name() string { return o.name }

var openCodeFamily = []struct{ prefix, protocol string }{
	{"claude-", "messages"},
	{"minimax-", "messages"},
	{"qwen3.5-plus", "messages"},
	{"qwen3.6-plus", "messages"},
	{"qwen3.7-max", "messages"},
	{"qwen3.7-plus", "messages"},
	{"qwen3.8-flash", "messages"},
	{"gpt-", "responses"},
	{"grok", "responses"},
	{"muse-spark", "responses"},
	{"gemini-", "google"},
}

// openCodeModelOverrides fixes models whose protocol differs from their
// family. The gateway returns a 500 rather than a protocol-mismatch error when
// one of these is sent to the wrong endpoint.
var openCodeModelOverrides = map[string]string{
	"minimax-m3.1-flash": "openai",
}

// openCodeProviderOverrides fixes models whose protocol differs by gateway.
var openCodeProviderOverrides = map[string]string{
	"opencode-go/qwen3.8-max": "messages",
}

func classifyOpenCodeModel(provider, model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	p := strings.ToLower(strings.TrimSpace(provider))
	if protocol, ok := openCodeProviderOverrides[p+"/"+m]; ok {
		return protocol
	}
	if protocol, ok := openCodeModelOverrides[m]; ok {
		return protocol
	}
	for _, f := range openCodeFamily {
		if strings.HasPrefix(m, f.prefix) {
			return f.protocol
		}
	}
	if info, ok := modelinfo.Lookup(provider, model); ok && info.Protocol != "" {
		return info.Protocol
	}
	return "openai"
}

func (o *openCode) protocolOrder(model string) []string {
	o.mu.Lock()
	known, ok := o.resolved[model]
	o.mu.Unlock()
	if ok {
		return []string{known}
	}
	first := classifyOpenCodeModel(o.name, model)
	order := []string{first}
	for _, p := range []string{"openai", "messages", "responses", "google"} {
		if p != first {
			order = append(order, p)
		}
	}
	return order
}

func (o *openCode) remember(model, protocol string) {
	o.mu.Lock()
	o.resolved[model] = protocol
	o.mu.Unlock()
}

func isProtocolMismatch(err error) bool {
	s := err.Error()
	return strings.Contains(s, "ModelProtocolUnsupported") ||
		strings.Contains(s, "does not support this protocol")
}

func (o *openCode) headers(sessionID string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("User-Agent", UserAgent)
		if sessionID != "" {
			r.Header.Set("x-opencode-session", sessionID)
		}
	}
}

func (o *openCode) member(protocol, sessionID string) Provider {
	hdrs := o.headers(sessionID)
	switch protocol {
	case "messages":
		p := newAnthropic(o.name, o.base+"/anthropic", o.key)
		p.prepare = hdrs
		return p
	case "responses":
		p := newResponses(o.name, o.base+"/openai/v1", o.key)
		p.prepare = hdrs
		return p
	case "google":
		p := newGoogle(o.name, o.base+"/google", o.key)
		p.prepare = hdrs
		return p
	default:
		p := newOpenAI(o.name, o.base+"/openai/v1", o.key, 0)
		p.prepare = hdrs
		return p
	}
}

func (o *openCode) Chat(ctx context.Context, req Request, onDelta func(string)) (*Result, error) {
	var lastErr error
	for _, protocol := range o.protocolOrder(req.Model) {
		res, err := o.member(protocol, req.SessionID).Chat(ctx, req, onDelta)
		if err == nil {
			o.remember(req.Model, protocol)
			return res, nil
		}
		lastErr = err
		if !isProtocolMismatch(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (o *openCode) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.base+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if o.key != "" {
		req.Header.Set("Authorization", "Bearer "+o.key)
	}
	o.headers("")(req)
	resp, err := o.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(o.name, resp)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		models = append(models, m.ID)
	}
	sort.Strings(models)
	return models, nil
}

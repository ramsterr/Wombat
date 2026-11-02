package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ramsterr/Wombat/internal/config"
)

func TestGoogleChatStream(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-x:streamGenerateContent" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.RawQuery != "alt=sse" {
			t.Errorf("query = %q, want alt=sse without credentials", r.URL.RawQuery)
		}
		if r.Header.Get("x-goog-api-key") != "gk" {
			t.Errorf("missing x-goog-api-key")
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`data: {"candidates":[{"content":{"parts":[{"text":"Plan","thought":true}]}}]}`,
			`data: {"candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}`,
			`data: {"candidates":[{"content":{"parts":[{"text":"lo"}]}}]}`,
			`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"bash","args":{"command":"ls"}},"thoughtSignature":"sig123"}]}}]}`,
			`data: {"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":8,"cachedContentTokenCount":5}}`,
			``,
		}
		for _, c := range chunks {
			io.WriteString(w, c+"\n\n")
		}
	}))
	defer srv.Close()

	p := newGoogle("test", srv.URL, "gk")
	var deltas, thoughts string
	res, err := p.Chat(context.Background(), Request{
		Model:  "gemini-x",
		System: "sys",
		Messages: []Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "bash", Arguments: `{"command":"ls"}`}}},
			{Role: "tool", ToolCallID: "c1", Name: "bash", Content: "a.txt"},
		},
		Tools:       []Tool{{Name: "bash", Description: "d", Schema: map[string]any{"type": "object"}}},
		OnReasoning: func(s string) { thoughts += s },
	}, func(s string) { deltas += s })
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "Hello" || deltas != "Hello" || thoughts != "Plan" || res.Reasoning != "Plan" {
		t.Fatalf("content=%q deltas=%q thoughts=%q", res.Content, deltas, thoughts)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "bash" || res.ToolCalls[0].Arguments != `{"command":"ls"}` {
		t.Fatalf("tool calls = %+v", res.ToolCalls)
	}
	if res.ToolCalls[0].ID == "" {
		t.Errorf("tool call id should be synthesized")
	}
	if res.ToolCalls[0].Signature != "sig123" {
		t.Errorf("thought signature = %q", res.ToolCalls[0].Signature)
	}
	if res.Usage.In != 20 || res.Usage.Out != 8 || res.Usage.Cached != 5 {
		t.Fatalf("usage = %+v", res.Usage)
	}
	if res.StopReason != "stop" {
		t.Errorf("stop reason = %q", res.StopReason)
	}

	sys, _ := gotBody["systemInstruction"].(map[string]any)
	parts, _ := sys["parts"].([]any)
	p0, _ := parts[0].(map[string]any)
	if p0["text"] != "sys" {
		t.Errorf("systemInstruction = %+v", sys)
	}
	contents, _ := gotBody["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("contents = %d", len(contents))
	}
	asst, _ := contents[1].(map[string]any)
	if asst["role"] != "model" {
		t.Errorf("assistant role = %v", asst["role"])
	}
	aparts, _ := asst["parts"].([]any)
	fc, _ := aparts[0].(map[string]any)["functionCall"].(map[string]any)
	if fc["name"] != "bash" {
		t.Errorf("functionCall = %+v", fc)
	}
	if _, ok := fc["args"].(map[string]any); !ok {
		t.Errorf("google args must be an object, got %T", fc["args"])
	}
	toolMsg, _ := contents[2].(map[string]any)
	tparts, _ := toolMsg["parts"].([]any)
	fr, _ := tparts[0].(map[string]any)["functionResponse"].(map[string]any)
	if fr["name"] != "bash" {
		t.Errorf("functionResponse = %+v", fr)
	}
	tools, _ := gotBody["tools"].([]any)
	t0, _ := tools[0].(map[string]any)
	if _, ok := t0["functionDeclarations"].([]any); !ok {
		t.Errorf("tools mapping = %+v", t0)
	}
}

func TestGoogleProviderFromConfig(t *testing.T) {
	var gotPath, gotQuery, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotKey = r.Header.Get("x-goog-api-key")
		io.WriteString(w, `{"models":[{"name":"models/gemini-2.5-pro"},{"name":"models/gemini-2.5-flash"}]}`)
	}))
	defer srv.Close()

	p, err := New("gemini", &config.Provider{Kind: "google", BaseURL: srv.URL}, "gk")
	if err != nil {
		t.Fatal(err)
	}
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1beta/models" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery != "" {
		t.Errorf("query = %q, want no credentials in the URL", gotQuery)
	}
	if gotKey != "gk" {
		t.Errorf("x-goog-api-key = %q", gotKey)
	}
	if len(models) != 2 || models[0] != "gemini-2.5-flash" || models[1] != "gemini-2.5-pro" {
		t.Fatalf("models = %v", models)
	}
}

func TestGoogleModelPathIsEscaped(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]}}]}\n\n")
	}))
	defer srv.Close()

	p := newGoogle("gemini", srv.URL, "gk")
	if _, err := p.Chat(context.Background(), Request{Model: "gemini/x?key=injected"}, nil); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1beta/models/gemini%2Fx%3Fkey=injected:streamGenerateContent" {
		t.Errorf("model path not escaped: %q", gotPath)
	}
	if gotQuery != "alt=sse" {
		t.Errorf("query = %q, want alt=sse only", gotQuery)
	}
}

func TestGoogleRefusesCrossOriginRedirect(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "" {
			leaked.Store(true)
		}
		io.WriteString(w, `{"models":[]}`)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer source.Close()

	p := newGoogle("gemini", source.URL, "gk")
	if _, err := p.ListModels(context.Background()); err == nil {
		t.Fatal("expected the cross-host redirect to be refused")
	}
	if leaked.Load() {
		t.Fatal("API key leaked to the redirect target")
	}
}

func TestGoogleThoughtSignatureReplay(t *testing.T) {
	contents, err := (&google{}).mapContents([]Message{{
		Role:      "assistant",
		ToolCalls: []ToolCall{{ID: "c1", Name: "bash", Arguments: `{"command":"ls"}`, Signature: "sig123"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	parts := contents[0]["parts"].([]map[string]any)
	if parts[0]["thoughtSignature"] != "sig123" {
		t.Fatalf("parts = %#v", parts)
	}
}

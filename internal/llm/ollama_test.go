package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaChatStream(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/x-ndjson")
		lines := []string{
			`{"message":{"role":"assistant","content":"","thinking":"Let me"},"done":false}`,
			`{"message":{"role":"assistant","content":"Hi"},"done":false}`,
			`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"glob","arguments":{"pattern":"*.go"}}}]},"done":false}`,
			`{"message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":30,"eval_count":7}`,
		}
		for _, l := range lines {
			io.WriteString(w, l+"\n")
		}
	}))
	defer srv.Close()

	p := newOllama("test", srv.URL, 0)
	var deltas, thoughts string
	res, err := p.Chat(context.Background(), Request{
		Model: "llama3",
		Messages: []Message{
			{Role: "user", Content: "find go files"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "glob", Arguments: `{"pattern":"*.go"}`}}},
			{Role: "tool", ToolCallID: "c1", Name: "glob", Content: "main.go"},
		},
		Tools:       []Tool{{Name: "glob", Description: "d", Schema: map[string]any{"type": "object"}}},
		OnReasoning: func(s string) { thoughts += s },
	}, func(s string) { deltas += s })
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "Hi" || deltas != "Hi" || thoughts != "Let me" {
		t.Fatalf("content=%q deltas=%q thoughts=%q", res.Content, deltas, thoughts)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d", len(res.ToolCalls))
	}
	tc := res.ToolCalls[0]
	if tc.Name != "glob" || tc.Arguments != `{"pattern":"*.go"}` {
		t.Fatalf("bad tool call: %+v", tc)
	}
	if tc.ID == "" {
		t.Errorf("tool call id should be synthesized")
	}
	if res.Usage.In != 30 || res.Usage.Out != 7 {
		t.Fatalf("usage = %+v", res.Usage)
	}

	opts, _ := gotBody["options"].(map[string]any)
	if opts["num_ctx"] != float64(32768) {
		t.Errorf("num_ctx = %v", opts["num_ctx"])
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %d", len(msgs))
	}
	asst, _ := msgs[1].(map[string]any)
	tcs, _ := asst["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("assistant tool_calls missing")
	}
	fn, _ := tcs[0].(map[string]any)["function"].(map[string]any)
	if _, ok := fn["arguments"].(map[string]any); !ok {
		t.Errorf("ollama args must be an object, got %T", fn["arguments"])
	}
	toolMsg, _ := msgs[2].(map[string]any)
	if toolMsg["tool_name"] != "glob" {
		t.Errorf("tool message must carry tool_name, got %v", toolMsg["tool_name"])
	}
}

func TestOllamaListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %s", r.URL.Path)
		}
		io.WriteString(w, `{"models":[{"name":"b:latest"},{"name":"a:latest"}]}`)
	}))
	defer srv.Close()
	p := newOllama("test", srv.URL, 4096)
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "a:latest" {
		t.Fatalf("models = %v", models)
	}
}

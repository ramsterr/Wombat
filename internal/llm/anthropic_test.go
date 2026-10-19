package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnthropicChatStream(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "ak" {
			t.Errorf("missing x-api-key")
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Errorf("missing anthropic-version")
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"cache_read_input_tokens\":50,\"cache_creation_input_tokens\":10}}}",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"read\"}}",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\"}}",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"a.txt\\\"}\"}}",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\"}}",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"Done\"}}",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":42}}",
			"",
		}
		for _, e := range events {
			io.WriteString(w, e+"\n\n")
		}
	}))
	defer srv.Close()

	p := newAnthropic("test", srv.URL, "ak")
	var deltas string
	res, err := p.Chat(context.Background(), Request{
		Model:  "claude-x",
		System: "sys",
		Messages: []Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "toolu_1", Name: "read", Arguments: `{"path":"a.txt"}`}}},
			{Role: "tool", ToolCallID: "toolu_1", Name: "read", Content: "data"},
		},
		Tools: []Tool{{Name: "read", Description: "d", Schema: map[string]any{"type": "object"}}, {Name: "bash", Description: "d", Schema: map[string]any{"type": "object"}}},
	}, func(s string) { deltas += s })
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "Done" || deltas != "Done" {
		t.Fatalf("content=%q deltas=%q", res.Content, deltas)
	}
	if res.Usage.In != 160 || res.Usage.Cached != 50 || res.Usage.Out != 42 {
		t.Fatalf("usage = %+v", res.Usage)
	}
	if res.StopReason != "tool_use" {
		t.Fatalf("stop reason = %q", res.StopReason)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "read" || res.ToolCalls[0].Arguments != `{"path":"a.txt"}` {
		t.Fatalf("tool calls = %+v", res.ToolCalls)
	}

	sys, _ := gotBody["system"].([]any)
	if len(sys) != 1 {
		t.Fatalf("system blocks = %d", len(sys))
	}
	sm, _ := sys[0].(map[string]any)
	cc, _ := sm["cache_control"].(map[string]any)
	if cc["type"] != "ephemeral" {
		t.Errorf("system cache_control missing")
	}
	tools, _ := gotBody["tools"].([]any)
	last, _ := tools[len(tools)-1].(map[string]any)
	cc2, _ := last["cache_control"].(map[string]any)
	if cc2["type"] != "ephemeral" {
		t.Errorf("last tool missing cache_control")
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("mapped messages = %d, want 3", len(msgs))
	}
	toolResultMsg, _ := msgs[2].(map[string]any)
	if toolResultMsg["role"] != "user" {
		t.Errorf("tool results must be mapped to user role")
	}
}

func TestAnthropicListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %s", r.URL.Path)
		}
		io.WriteString(w, `{"data":[{"id":"claude-b"},{"id":"claude-a"}]}`)
	}))
	defer srv.Close()
	p := newAnthropic("test", srv.URL, "ak")
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "claude-a" {
		t.Fatalf("models = %v", models)
	}
}

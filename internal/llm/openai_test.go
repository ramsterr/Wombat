package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIChatStream(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing auth header")
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		lines := []string{
			`data: {"choices":[{"delta":{"reasoning_content":"Think"}}]}`,
			`data: {"choices":[{"delta":{"reasoning":"Think"}}]}`,
			`data: {"choices":[{"delta":{"content":"Hel"}}]}`,
			`data: {"choices":[{"delta":{"content":"lo"}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"bash","arguments":"{\"command\":"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]}}]}`,
			`data: {"choices":[{"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4}}}`,
			`data: [DONE]`,
			``,
		}
		io.WriteString(w, strings.Join(lines, "\n"))
	}))
	defer srv.Close()

	p := newOpenAI("test", srv.URL, "test-key", 0)
	var deltas, thoughts string
	res, err := p.Chat(context.Background(), Request{
		Model:  "m",
		System: "sys",
		Messages: []Message{
			{Role: "user", Content: "hi"},
		},
		Tools:       []Tool{{Name: "bash", Description: "d", Schema: map[string]any{"type": "object"}}},
		OnReasoning: func(s string) { thoughts += s },
	}, func(s string) { deltas += s })
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "Hello" || deltas != "Hello" || thoughts != "Think" || res.Reasoning != "Think" {
		t.Fatalf("content=%q deltas=%q thoughts=%q reasoning=%q", res.Content, deltas, thoughts, res.Reasoning)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d", len(res.ToolCalls))
	}
	tc := res.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "bash" || tc.Arguments != `{"command":"ls"}` {
		t.Fatalf("bad tool call: %+v", tc)
	}
	if res.Usage.In != 10 || res.Usage.Out != 5 || res.Usage.Cached != 4 {
		t.Fatalf("bad usage: %+v", res.Usage)
	}
	so, _ := gotBody["stream_options"].(map[string]any)
	if so["include_usage"] != true {
		t.Errorf("stream_options missing include_usage")
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (system+user)", len(msgs))
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Errorf("first message role = %v", first["role"])
	}
}

func TestOpenAIMessageMapping(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	p := newOpenAI("test", srv.URL, "", 0)
	_, err := p.Chat(context.Background(), Request{
		Model: "m",
		Messages: []Message{
			{Role: "user", Content: "q"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"a"}`}}},
			{Role: "tool", ToolCallID: "c1", Name: "read", Content: "file body"},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
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
	if fn["name"] != "read" {
		t.Errorf("function name = %v", fn["name"])
	}
	toolMsg, _ := msgs[2].(map[string]any)
	if toolMsg["tool_call_id"] != "c1" {
		t.Errorf("tool_call_id = %v", toolMsg["tool_call_id"])
	}
}

func TestOpenAIHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"bad key"}}`)
	}))
	defer srv.Close()
	p := newOpenAI("test", srv.URL, "x", 0)
	_, err := p.Chat(context.Background(), Request{Model: "m"}, nil)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 error, got %v", err)
	}
}

func TestOpenAIListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %s", r.URL.Path)
		}
		io.WriteString(w, `{"data":[{"id":"b"},{"id":"a"}]}`)
	}))
	defer srv.Close()
	p := newOpenAI("test", srv.URL, "", 0)
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "a" || models[1] != "b" {
		t.Fatalf("models = %v", models)
	}
}

func TestOpenAIReasoningReplay(t *testing.T) {
	msgs, err := (&openAI{}).mapMessages(Request{Messages: []Message{{
		Role:      "assistant",
		Reasoning: "because",
		ToolCalls: []ToolCall{{ID: "c1", Name: "bash", Arguments: "{}"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if msgs[0].ReasoningContent != "because" {
		t.Fatalf("reasoning_content = %q", msgs[0].ReasoningContent)
	}
	raw, _ := json.Marshal(msgs[0])
	if !strings.Contains(string(raw), `"reasoning_content":"because"`) {
		t.Fatalf("reasoning_content missing from payload: %s", raw)
	}
}

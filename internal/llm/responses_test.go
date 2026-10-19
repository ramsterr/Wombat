package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResponsesTextStream(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("missing bearer auth")
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"Think"}`,
			`event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"delta":"Hel"}`,
			`event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"delta":"lo"}`,
			`event: response.completed
data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","id":"msg_1","content":[{"type":"output_text","text":"Hello"}]}],"usage":{"input_tokens":11,"output_tokens":5,"input_tokens_details":{"cached_tokens":7}}}}`,
			``,
		}
		for _, e := range events {
			io.WriteString(w, e+"\n\n")
		}
	}))
	defer srv.Close()

	p := newResponses("test", srv.URL, "k")
	var deltas, thoughts string
	res, err := p.Chat(context.Background(), Request{
		Model:       "gpt-x",
		System:      "sys",
		Messages:    []Message{{Role: "user", Content: "hi"}},
		OnReasoning: func(s string) { thoughts += s },
	}, func(s string) { deltas += s })
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "Hello" || deltas != "Hello" || thoughts != "Think" || res.Reasoning != "Think" {
		t.Fatalf("content=%q deltas=%q thoughts=%q", res.Content, deltas, thoughts)
	}
	if res.Usage.In != 11 || res.Usage.Out != 5 || res.Usage.Cached != 7 {
		t.Fatalf("usage = %+v", res.Usage)
	}
	if gotBody["instructions"] != "sys" {
		t.Errorf("instructions = %v", gotBody["instructions"])
	}
	input, _ := gotBody["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("input items = %d", len(input))
	}
	item, _ := input[0].(map[string]any)
	if item["role"] != "user" || item["content"] != "hi" {
		t.Errorf("input item = %+v", item)
	}
}

func TestResponsesFunctionCallStream(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","status":"in_progress","name":"bash","call_id":"call_abc","arguments":""}}`,
			`event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"{\"command\":\"echo hi\"}"}`,
			`event: response.function_call_arguments.done
data: {"type":"response.function_call_arguments.done","output_index":0,"item_id":"fc_1","arguments":"{\"command\":\"echo hi\"}","name":"bash"}`,
			`event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","status":"completed","name":"bash","call_id":"call_abc","arguments":"{\"command\":\"echo hi\"}"}}`,
			`event: response.completed
data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"reasoning","id":"rs_1"},{"type":"function_call","id":"fc_1","call_id":"call_abc","name":"bash","arguments":"{\"command\":\"echo hi\"}"}],"usage":{"input_tokens":289,"output_tokens":81,"input_tokens_details":{"cached_tokens":128}}}}`,
			``,
		}
		for _, e := range events {
			io.WriteString(w, e+"\n\n")
		}
	}))
	defer srv.Close()

	p := newResponses("test", srv.URL, "k")
	res, err := p.Chat(context.Background(), Request{
		Model: "grok-x",
		Messages: []Message{
			{Role: "user", Content: "run it"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_prev", Name: "bash", Arguments: `{"command":"ls"}`}}},
			{Role: "tool", ToolCallID: "call_prev", Name: "bash", Content: "a.txt"},
		},
		Tools: []Tool{{Name: "bash", Description: "d", Schema: map[string]any{"type": "object"}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d", len(res.ToolCalls))
	}
	tc := res.ToolCalls[0]
	if tc.ID != "call_abc" || tc.Name != "bash" || tc.Arguments != `{"command":"echo hi"}` {
		t.Fatalf("bad tool call: %+v", tc)
	}
	if res.Usage.In != 289 || res.Usage.Out != 81 || res.Usage.Cached != 128 {
		t.Fatalf("usage = %+v", res.Usage)
	}

	input, _ := gotBody["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("input items = %d", len(input))
	}
	fc, _ := input[1].(map[string]any)
	if fc["type"] != "function_call" || fc["call_id"] != "call_prev" {
		t.Errorf("assistant tool call mapping = %+v", fc)
	}
	fo, _ := input[2].(map[string]any)
	if fo["type"] != "function_call_output" || fo["call_id"] != "call_prev" || fo["output"] != "a.txt" {
		t.Errorf("tool output mapping = %+v", fo)
	}
	tools, _ := gotBody["tools"].([]any)
	tool0, _ := tools[0].(map[string]any)
	if tool0["type"] != "function" || tool0["name"] != "bash" {
		t.Errorf("tools mapping = %+v", tool0)
	}
}

func TestResponsesErrorEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"message\":\"boom\"}}}\n\n")
	}))
	defer srv.Close()
	p := newResponses("test", srv.URL, "")
	_, err := p.Chat(context.Background(), Request{Model: "m"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

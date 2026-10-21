package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClassifyOpenCodeModel(t *testing.T) {
	cases := map[string]string{
		"claude-haiku-5-5":    "messages",
		"minimax-m3":          "messages",
		"qwen3.7-plus":        "messages",
		"qwen3.8-flash":       "messages",
		"qwen3.8-max":         "openai",
		"gpt-6-luna":          "responses",
		"grok-4.6":            "responses",
		"muse-spark-1.3":      "responses",
		"gemini-3-flash":      "google",
		"glm-5.2":             "openai",
		"kimi-k3":             "openai",
		"deepseek-v4.1-flash": "openai",
	}
	for model, want := range cases {
		if got := classifyOpenCodeModel("opencode", model); got != want {
			t.Errorf("classify(%s) = %s, want %s", model, got, want)
		}
	}
}

func TestOpenCodeProtocolFallback(t *testing.T) {
	var chatHits, msgHits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openai/v1/chat/completions":
			chatHits.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"type":"ModelProtocolUnsupported","message":"Model does not support this protocol."}`)
		case "/anthropic/v1/messages":
			msgHits.Add(1)
			if r.Header.Get("x-api-key") != "ok-key" {
				t.Errorf("messages request missing x-api-key")
			}
			if r.Header.Get("x-opencode-session") != "sess-1" {
				t.Errorf("messages request missing session header")
			}
			if r.Header.Get("User-Agent") == "" {
				t.Errorf("missing user agent")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3}}}\n\n")
			io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n")
			io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := newOpenCode("oc", srv.URL, "ok-key")
	req := Request{Model: "mystery-1", SessionID: "sess-1"}

	res, err := p.Chat(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "ok" {
		t.Fatalf("content = %q", res.Content)
	}
	if chatHits.Load() != 1 || msgHits.Load() != 1 {
		t.Fatalf("hits chat=%d messages=%d", chatHits.Load(), msgHits.Load())
	}

	res, err = p.Chat(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "ok" {
		t.Fatalf("second call content = %q", res.Content)
	}
	if chatHits.Load() != 1 {
		t.Errorf("resolved protocol should skip chat/completions on later calls")
	}
	if msgHits.Load() != 2 {
		t.Errorf("messages hits = %d, want 2", msgHits.Load())
	}
}

func TestOpenCodeNoFallbackOnRealErrors(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"error":{"message":"Model access is disabled"}}`)
	}))
	defer srv.Close()
	p := newOpenCode("oc", srv.URL, "k")
	_, err := p.Chat(context.Background(), Request{Model: "gpt-x"}, nil)
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("err = %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("non-protocol errors must not trigger fallback, hits = %d", hits.Load())
	}
}

func TestOpenCodeListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("missing bearer")
		}
		io.WriteString(w, `{"data":[{"id":"b"},{"id":"a"}]}`)
	}))
	defer srv.Close()
	p := newOpenCode("oc", srv.URL, "k")
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "a" {
		t.Fatalf("models = %v", models)
	}
}

func TestOpenCodeRoutesKnownFamilies(t *testing.T) {
	var gotPath atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		switch r.URL.Path {
		case "/openai/v1/responses":
			io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
		case "/openai/v1/chat/completions":
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := newOpenCode("oc", srv.URL, "k")
	if _, err := p.Chat(context.Background(), Request{Model: "grok-4.6"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := gotPath.Load(); got != "/openai/v1/responses" {
		t.Errorf("grok routed to %v", got)
	}
	if _, err := p.Chat(context.Background(), Request{Model: "glm-5.2"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := gotPath.Load(); got != "/openai/v1/chat/completions" {
		t.Errorf("glm routed to %v", got)
	}
}

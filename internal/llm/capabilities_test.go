package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ramsterr/Wombat/internal/modelinfo"
)

func stubCatalogue(t *testing.T, lookup func(string, string) (modelinfo.Info, bool)) {
	t.Helper()
	old := catalogueLookup
	catalogueLookup = lookup
	t.Cleanup(func() { catalogueLookup = old })
}

func TestReasoningEnabledCatalogueWins(t *testing.T) {
	stubCatalogue(t, func(provider, model string) (modelinfo.Info, bool) {
		return modelinfo.Info{Reasoning: true}, true
	})
	if !reasoningEnabled("p", "m", func(string) bool { return false }) {
		t.Fatal("catalogue reasoning=true must enable reasoning")
	}

	stubCatalogue(t, func(provider, model string) (modelinfo.Info, bool) {
		return modelinfo.Info{Reasoning: false}, true
	})
	if reasoningEnabled("p", "m", func(string) bool { return true }) {
		t.Fatal("catalogue reasoning=false must disable reasoning")
	}
}

func TestReasoningEnabledFallsBackWhenUnknown(t *testing.T) {
	stubCatalogue(t, func(provider, model string) (modelinfo.Info, bool) {
		return modelinfo.Info{}, false
	})
	if !reasoningEnabled("p", "m", func(string) bool { return true }) {
		t.Fatal("unknown model must use the fallback")
	}
	if reasoningEnabled("p", "m", func(string) bool { return false }) {
		t.Fatal("unknown model must use the fallback")
	}
}

// TestResponsesHonoursCatalogueReasoning locks in that a model the provider
// says cannot reason never gets reasoning parameters, even when its name
// looks like a reasoning model.
func TestResponsesHonoursCatalogueReasoning(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{}}}\n\n")
	}))
	defer srv.Close()

	stubCatalogue(t, func(provider, model string) (modelinfo.Info, bool) {
		return modelinfo.Info{Reasoning: false}, true
	})
	p := newResponses("test", srv.URL, "k")
	if _, err := p.Chat(context.Background(), Request{
		Model:    "gpt-5",
		Messages: []Message{{Role: "user", Content: "hi"}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := gotBody["reasoning"]; ok {
		t.Fatalf("reasoning must not be sent to a non-reasoning model: %v", gotBody["reasoning"])
	}
	if _, ok := gotBody["include"]; ok {
		t.Fatal("encrypted reasoning include must not be sent")
	}
}

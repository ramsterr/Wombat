package embeddings

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ramsterr/Wombat/internal/config"
)

func TestLocalIsDeterministicAndNormalized(t *testing.T) {
	a := Local("authenticate the user with a token")
	b := Local("authenticate the user with a token")
	if len(a) != localDim {
		t.Fatalf("dim = %d, want %d", len(a), localDim)
	}
	var norm float64
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("vector differs at %d", i)
		}
		norm += float64(a[i]) * float64(a[i])
	}
	if math.Abs(norm-1) > 1e-5 {
		t.Fatalf("norm = %f, want 1", norm)
	}
	if Local("")[0] != 0 {
		t.Fatal("empty text should produce a zero vector")
	}
}

func TestLocalRelatedTextScoresHigher(t *testing.T) {
	query := Local("user login")
	related := Local("authenticate the account")
	unrelated := Local("render the CSS grid")
	if cosine(query, related) <= cosine(query, unrelated) {
		t.Fatalf("related = %f, unrelated = %f", cosine(query, related), cosine(query, unrelated))
	}
}

func TestEmbedBatchesAndPreservesOrder(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("missing bearer")
		}
		requests++
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "embed-1" {
			t.Errorf("model = %q", body.Model)
		}
		data := make([]map[string]any, len(body.Input))
		for i, in := range body.Input {
			data[i] = map[string]any{"index": i, "embedding": []float32{float32(len(in)), 1}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "embed-1", HTTP: srv.Client()})
	inputs := make([]string, maxBatch+3)
	for i := range inputs {
		inputs[i] = "x"
	}
	vecs, err := c.Embed(context.Background(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != len(inputs) {
		t.Fatalf("vectors = %d, want %d", len(vecs), len(inputs))
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	if vecs[0][0] != 1 || vecs[len(vecs)-1][0] != 1 {
		t.Fatalf("vectors out of order: %v", vecs)
	}
}

func TestEmbedReportsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"no such model"}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Model: "bad", HTTP: srv.Client()})
	if _, err := c.Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestFromConfig(t *testing.T) {
	t.Setenv("WOMBAT_EMBED_PROVIDER", "")
	t.Setenv("WOMBAT_EMBED_MODEL", "")
	cfg := config.Defaults()
	cfg.Providers["lmstudio"].BaseURL = "http://127.0.0.1:1234/v1"
	cfg.EmbeddingProvider = "lmstudio"
	cfg.EmbeddingModel = "local-embed"
	c := FromConfig(cfg)
	if c == nil || c.Model() != "local-embed" {
		t.Fatalf("client = %+v", c)
	}
	cfg.EmbeddingProvider = ""
	if FromConfig(cfg) != nil {
		t.Fatal("no provider must yield nil")
	}
	cfg.EmbeddingProvider = "ollama"
	cfg.EmbeddingModel = ""
	t.Setenv("WOMBAT_EMBED_MODEL", "custom-embed")
	if c := FromConfig(cfg); c == nil || c.Model() != "custom-embed" {
		t.Fatalf("env model not used: %+v", c)
	}
}

func cosine(a, b []float32) float64 {
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}

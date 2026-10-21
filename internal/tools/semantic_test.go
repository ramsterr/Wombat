package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ramsterr/Wombat/internal/embeddings"
)

func semanticWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("auth.go", `package demo

// authenticateUser checks the login credentials for an account.
func authenticateUser(name, password string) bool {
	return name != "" && password != ""
}
`)
	write("layout.go", `package demo

// layoutGrid renders the responsive css grid.
func layoutGrid() string {
	return "grid"
}
`)
	write(".env", "SECRET_TOKEN=do-not-index\n")
	return dir
}

func TestSemanticSearchFindsByMeaning(t *testing.T) {
	dir := semanticWorkspace(t)
	env := testEnv(dir)
	env.IndexDir = t.TempDir()

	out := run(t, &SemanticSearch{}, env, `{"query":"login account credentials"}`)
	if !strings.Contains(out, "auth.go") {
		t.Fatalf("auth query did not find auth.go:\n%s", out)
	}
	if strings.Contains(out, ".env") {
		t.Fatalf("secret file was indexed:\n%s", out)
	}

	out = run(t, &SemanticSearch{}, env, `{"query":"responsive css layout"}`)
	if !strings.Contains(out, "layout.go") {
		t.Fatalf("layout query did not find layout.go:\n%s", out)
	}

	entries, err := os.ReadDir(env.IndexDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("index cache = %v, %v", entries, err)
	}
}

func TestSemanticSearchReindexesChangedFiles(t *testing.T) {
	dir := semanticWorkspace(t)
	env := testEnv(dir)
	env.IndexDir = t.TempDir()

	if out := run(t, &SemanticSearch{}, env, `{"query":"sourdough starter"}`); strings.Contains(out, "auth.go") {
		t.Fatalf("unexpected match before edit:\n%s", out)
	}
	path := filepath.Join(dir, "auth.go")
	if err := os.WriteFile(path, []byte("package demo\n\n// sourdoughStarter feeds the bread starter.\nfunc sourdoughStarter() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	out := run(t, &SemanticSearch{}, env, `{"query":"sourdough bread starter"}`)
	if !strings.Contains(out, "auth.go") {
		t.Fatalf("changed file was not reindexed:\n%s", out)
	}
}

func TestSemanticSearchFallsBackWhenProviderFails(t *testing.T) {
	dir := semanticWorkspace(t)
	env := testEnv(dir)
	env.IndexDir = t.TempDir()
	env.Embeddings = embeddings.New(embeddings.Config{
		BaseURL: "http://127.0.0.1:1/v1",
		Model:   "unreachable",
		HTTP:    &http.Client{Timeout: 300 * time.Millisecond},
	})
	out := run(t, &SemanticSearch{}, env, `{"query":"login credentials"}`)
	if !strings.Contains(out, "offline embedder") || !strings.Contains(out, "auth.go") {
		t.Fatalf("fallback output:\n%s", out)
	}
}

func TestSemanticSearchNeedsQuery(t *testing.T) {
	env := testEnv(t.TempDir())
	env.IndexDir = t.TempDir()
	if _, err := (&SemanticSearch{}).Run(t.Context(), []byte(`{"query":" "}`), env); err == nil {
		t.Fatal("expected an error for an empty query")
	}
}

func TestSemanticSearchUsesConfiguredEmbeddings(t *testing.T) {
	dir := semanticWorkspace(t)
	env := testEnv(dir)
	env.IndexDir = t.TempDir()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		data := make([]map[string]any, len(body.Input))
		for i, text := range body.Input {
			data[i] = map[string]any{"index": i, "embedding": embeddings.Local(text)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()
	env.Embeddings = embeddings.New(embeddings.Config{
		BaseURL: srv.URL + "/v1",
		Model:   "fake-embed",
		HTTP:    srv.Client(),
	})

	out := run(t, &SemanticSearch{}, env, `{"query":"login account credentials"}`)
	if !strings.Contains(out, "remote:fake-embed") || !strings.Contains(out, "auth.go") {
		t.Fatalf("remote search output:\n%s", out)
	}
	first := calls
	out = run(t, &SemanticSearch{}, env, `{"query":"responsive css layout"}`)
	if !strings.Contains(out, "layout.go") {
		t.Fatalf("second search output:\n%s", out)
	}
	if calls-first != 1 {
		t.Fatalf("unchanged index should only embed the query: %d calls", calls-first)
	}
}

func TestRegistryHasSemanticAndRefactor(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range Registry() {
		names[tool.Name()] = true
	}
	for _, want := range []string{"refactor", "semantic_search"} {
		if !names[want] {
			t.Errorf("%s is not registered", want)
		}
	}
}

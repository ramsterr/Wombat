package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWebFetchExtractsMainContentAndCaches(t *testing.T) {
	clearWebCache()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `<html><body><nav>menu</nav><main><article><h1>Useful title</h1><p>Useful body.</p></article></main><footer>footer</footer><script>noise()</script></body></html>`)
	}))
	defer srv.Close()
	env := &Env{Dir: t.TempDir(), Permit: func(string, string) Decision { return Decision{Allow: true} }}
	tool := &WebFetch{}
	raw, _ := json.Marshal(webFetchArgs{URL: srv.URL})
	out, err := tool.Run(context.Background(), raw, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Useful title") || !strings.Contains(out, "Useful body") {
		t.Fatalf("main content missing: %q", out)
	}
	if strings.Contains(out, "menu") || strings.Contains(out, "footer") || strings.Contains(out, "noise") {
		t.Fatalf("chrome leaked into content: %q", out)
	}
	if _, err := tool.Run(context.Background(), raw, env); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("second fetch should use cache, hits=%d", hits.Load())
	}
}

func TestWebFetchRetriesConnectionReset(t *testing.T) {
	clearWebCache()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			if hj, ok := w.(http.Hijacker); ok {
				conn, _, err := hj.Hijack()
				if err == nil {
					_ = conn.Close()
					return
				}
			}
		}
		fmt.Fprint(w, "recovered body")
	}))
	defer srv.Close()
	env := &Env{Dir: t.TempDir(), Permit: func(string, string) Decision { return Decision{Allow: true} }}
	raw, _ := json.Marshal(webFetchArgs{URL: srv.URL})
	out, err := (&WebFetch{}).Run(context.Background(), raw, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "recovered body") || hits.Load() < 2 {
		t.Fatalf("out=%q hits=%d", out, hits.Load())
	}
}

func TestWebFetchJSONAndMeta(t *testing.T) {
	clearWebCache()
	jsonSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"a":1,"b":[2,3]}`)
	}))
	defer jsonSrv.Close()
	env := &Env{Dir: t.TempDir(), Permit: func(string, string) Decision { return Decision{Allow: true} }}
	raw, _ := json.Marshal(webFetchArgs{URL: jsonSrv.URL})
	out, err := (&WebFetch{}).Run(context.Background(), raw, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\"a\": 1") {
		t.Fatalf("JSON was not pretty-printed: %q", out)
	}

	htmlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>Real title</title><meta name="description" content="Real description"></head><body><main><p>Body text.</p></main></body></html>`)
	}))
	defer htmlSrv.Close()
	raw, _ = json.Marshal(webFetchArgs{URL: htmlSrv.URL})
	out, err = (&WebFetch{}).Run(context.Background(), raw, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Title: Real title") || !strings.Contains(out, "Description: Real description") || !strings.Contains(out, "Body text") {
		t.Fatalf("HTML metadata missing: %q", out)
	}
}

func TestWebFetchPDFNote(t *testing.T) {
	clearWebCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		fmt.Fprint(w, "%PDF-1.4 test")
	}))
	defer srv.Close()
	env := &Env{Dir: t.TempDir(), Permit: func(string, string) Decision { return Decision{Allow: true} }}
	raw, _ := json.Marshal(webFetchArgs{URL: srv.URL})
	out, err := (&WebFetch{}).Run(context.Background(), raw, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "PDF") {
		t.Fatalf("PDF note missing: %q", out)
	}
}

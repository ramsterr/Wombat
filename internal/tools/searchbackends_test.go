package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestSearchBingRSSParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<?xml version="1.0"?><rss><channel>
<item><title>Go docs</title><link>https://go.dev/doc?utm_source=x</link><description>Official documentation</description></item>
<item><title>Second</title><link>https://example.com/b</link><description>Another result</description></item>
</channel></rss>`)
	}))
	defer srv.Close()
	old := bingRSSURL
	bingRSSURL = srv.URL + "/?q="
	defer func() { bingRSSURL = old }()

	results, err := searchBingRSS(context.Background(), "go", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Title != "Go docs" || results[0].URL != "https://go.dev/doc?utm_source=x" {
		t.Fatalf("results = %+v", results)
	}
}

func TestSearchWebRacesAndCaches(t *testing.T) {
	for _, key := range []string{"BRAVE_API_KEY", "TAVILY_API_KEY", "SERPER_API_KEY"} {
		t.Setenv(key, "")
	}
	clearSearchCache()
	var mu sync.Mutex
	bingHits, ddgHits := 0, 0
	bing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		bingHits++
		mu.Unlock()
		fmt.Fprint(w, `<rss><channel><item><title>Bing</title><link>https://example.com/a?utm_medium=x</link><description>Bing result</description></item></channel></rss>`)
	}))
	defer bing.Close()
	ddg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ddgHits++
		mu.Unlock()
		fmt.Fprint(w, `<a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fb">DDG</a><a class="result__snippet">DDG result</a>`)
	}))
	defer ddg.Close()
	oldBing, oldDDG := bingRSSURL, ddgHTMLURL
	bingRSSURL, ddgHTMLURL = bing.URL+"/?q=", ddg.URL+"/?q="
	defer func() { bingRSSURL, ddgHTMLURL = oldBing, oldDDG }()

	results, err := searchWeb(context.Background(), "test query", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	foundCanonical := false
	for _, result := range results {
		if result.URL == "https://example.com/a" {
			foundCanonical = true
		}
		if strings.Contains(result.URL, "utm_") {
			t.Fatalf("tracking parameter not removed: %q", result.URL)
		}
	}
	if !foundCanonical {
		t.Fatalf("canonical Bing URL missing: %+v", results)
	}
	results, err = searchWeb(context.Background(), "test query", 8)
	if err != nil || len(results) != 2 {
		t.Fatalf("cached search = %+v, %v", results, err)
	}
	mu.Lock()
	b, d := bingHits, ddgHits
	mu.Unlock()
	if b != 1 || d != 1 {
		t.Fatalf("second search should hit cache: bing=%d ddg=%d", b, d)
	}
}

func TestSerperParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") != "k" {
			t.Errorf("missing key")
		}
		fmt.Fprint(w, `{"organic":[{"title":"One","link":"https://one.example","snippet":"first"}]}`)
	}))
	defer srv.Close()
	old := serperAPIURL
	serperAPIURL = srv.URL
	defer func() { serperAPIURL = old }()
	results, err := searchSerper(context.Background(), "k", "q", 5)
	if err != nil || len(results) != 1 || !strings.Contains(results[0].Title, "One") {
		t.Fatalf("results=%+v err=%v", results, err)
	}
}

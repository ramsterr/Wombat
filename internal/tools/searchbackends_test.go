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
	for _, key := range []string{"BRAVE_API_KEY", "TAVILY_API_KEY", "SERPER_API_KEY", "EXA_API_KEY"} {
		t.Setenv(key, "")
	}
	oldExa, oldTavily := exaMCPURL, tavilyAPIURL
	exaMCPURL, tavilyAPIURL = "", ""
	defer func() { exaMCPURL, tavilyAPIURL = oldExa, oldTavily }()
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
	if len(results) == 0 {
		t.Fatal("no results")
	}
	for _, result := range results {
		if strings.Contains(result.URL, "utm_") {
			t.Fatalf("tracking parameter not removed: %q", result.URL)
		}
	}
	mu.Lock()
	before := bingHits + ddgHits
	mu.Unlock()
	results, err = searchWeb(context.Background(), "test query", 8)
	if err != nil || len(results) == 0 {
		t.Fatalf("cached search = %+v, %v", results, err)
	}
	mu.Lock()
	after := bingHits + ddgHits
	mu.Unlock()
	if after != before {
		t.Fatalf("second search should hit cache: before=%d after=%d", before, after)
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

func TestSearchExaParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("missing content type")
		}
		fmt.Fprint(w, "event: message\ndata: {\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"Title: One\\nURL: https://one.example\\nHighlights:\\nfirst hit\\n\\n---\\n\\nTitle: Two\\nURL: https://two.example\\nText:\\nsecond hit\"}]}}\n\n")
	}))
	defer srv.Close()
	old := exaMCPURL
	exaMCPURL = srv.URL
	defer func() { exaMCPURL = old }()
	results, err := searchExa(context.Background(), "", "query", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].URL != "https://one.example" || !strings.Contains(results[1].Snippet, "second") {
		t.Fatalf("results = %+v", results)
	}
}

func TestRankSearchFiltersPoisonedResults(t *testing.T) {
	batches := []searchBatch{{engine: "test", results: []SearchResult{
		{Title: "scikit-learn: machine learning in Python", URL: "https://scikit-learn.org/stable/", Snippet: "Official scikit-learn documentation and GSoC information."},
		{Title: "Hot porn videos", URL: "https://pornhub.com/view", Snippet: "unrelated adult spam"},
		{Title: "Best essay writing service 2026", URL: "https://essay-spam.example/order", Snippet: "buy an assignment"},
		{Title: "A random blog", URL: "https://random.example/post", Snippet: "some notes"},
	}}}
	got := rankSearchResults("scikit-learn GSoC", batches, 8)
	if len(got) == 0 {
		t.Fatal("no results")
	}
	if got[0].URL != "https://scikit-learn.org/stable/" {
		t.Fatalf("top result = %+v", got[0])
	}
	for _, r := range got {
		if strings.Contains(r.URL, "pornhub") || strings.Contains(r.URL, "essay-spam") {
			t.Fatalf("poisoned result survived: %+v", r)
		}
	}
}

func TestRankSearchPrefersTrustedAndCrossEngine(t *testing.T) {
	good := SearchResult{Title: "GitHub - scikit-learn/scikit-learn", URL: "https://github.com/scikit-learn/scikit-learn", Snippet: "Official repository and GSoC information."}
	blog := SearchResult{Title: "My notes on scikit-learn", URL: "https://notes.example/scikit", Snippet: "scikit-learn notes"}
	batches := []searchBatch{
		{engine: "a", results: []SearchResult{blog, good}},
		{engine: "b", results: []SearchResult{good}},
	}
	got := rankSearchResults("scikit-learn GSoC", batches, 8)
	if len(got) < 2 {
		t.Fatalf("results = %+v", got)
	}
	if got[0].URL != good.URL {
		t.Fatalf("trusted/cross-engine result was not first: %+v", got)
	}
}

func TestRankSearchKeepsDomainsDiverse(t *testing.T) {
	var res []SearchResult
	for i := 0; i < 6; i++ {
		res = append(res, SearchResult{Title: "go docs", URL: "https://go.dev/doc/" + string(rune('a'+i)), Snippet: "go documentation"})
	}
	res = append(res, SearchResult{Title: "Go blog", URL: "https://blog.golang.org/one", Snippet: "go documentation"})
	got := rankSearchResults("go docs", []searchBatch{{results: res}}, 8)
	perHost := map[string]int{}
	for _, r := range got {
		perHost[hostnameOf(r.URL)]++
	}
	if perHost["go.dev"] > 2 {
		t.Fatalf("too many results from one domain: %+v", perHost)
	}
}

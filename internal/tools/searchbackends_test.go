package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
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

	results, err := searchBingRSS(context.Background(), searchRequest{Text: "go", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Title != "Go docs" || results[0].URL != "https://go.dev/doc?utm_source=x" {
		t.Fatalf("results = %+v", results)
	}
}

func TestSearchBingRSSRetriesTransientFailure(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		n := hits
		mu.Unlock()
		if n == 1 {
			http.Error(w, "busy", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `<rss><channel><item><title>After retry</title><link>https://example.com/a</link><description>ok</description></item></channel></rss>`)
	}))
	defer srv.Close()
	old := bingRSSURL
	bingRSSURL = srv.URL + "/?q="
	defer func() { bingRSSURL = old }()

	results, err := searchBingRSS(context.Background(), searchRequest{Text: "go", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v", results)
	}
	mu.Lock()
	got := hits
	mu.Unlock()
	if got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
}

func TestSearchWebRacesAndCaches(t *testing.T) {
	for _, key := range []string{"BRAVE_API_KEY", "TAVILY_API_KEY", "SERPER_API_KEY", "EXA_API_KEY"} {
		t.Setenv(key, "")
	}
	oldExa, oldTavily, oldMojeek := exaMCPURL, tavilyAPIURL, mojeekRSSURL
	exaMCPURL, tavilyAPIURL, mojeekRSSURL = "", "", ""
	defer func() { exaMCPURL, tavilyAPIURL, mojeekRSSURL = oldExa, oldTavily, oldMojeek }()
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

	opts := searchOptions{Queries: []string{"test query"}, Limit: 8, Depth: "fast"}
	results, warnings, err := searchWeb(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatalf("no results (warnings %v)", warnings)
	}
	for _, result := range results {
		if strings.Contains(result.URL, "utm_") {
			t.Fatalf("tracking parameter not removed: %q", result.URL)
		}
	}
	mu.Lock()
	before := bingHits + ddgHits
	mu.Unlock()
	results, _, err = searchWeb(context.Background(), opts)
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

func TestSearchWebReportsPartialFailure(t *testing.T) {
	for _, key := range []string{"BRAVE_API_KEY", "TAVILY_API_KEY", "SERPER_API_KEY", "EXA_API_KEY"} {
		t.Setenv(key, "")
	}
	oldExa, oldTavily, oldMojeek := exaMCPURL, tavilyAPIURL, mojeekRSSURL
	exaMCPURL, tavilyAPIURL, mojeekRSSURL = "", "", ""
	defer func() { exaMCPURL, tavilyAPIURL, mojeekRSSURL = oldExa, oldTavily, oldMojeek }()
	clearSearchCache()

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a class="result__a" href="https://example.com/ok">DDG</a><a class="result__snippet">still works</a>`)
	}))
	defer good.Close()
	oldBing, oldDDG := bingRSSURL, ddgHTMLURL
	bingRSSURL, ddgHTMLURL = bad.URL+"/?q=", good.URL+"/?q="
	defer func() { bingRSSURL, ddgHTMLURL = oldBing, oldDDG }()

	results, warnings, err := searchWeb(context.Background(), searchOptions{
		Queries: []string{"test query"}, Limit: 8, Depth: "fast",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("working backend results were discarded")
	}
	if !strings.Contains(strings.Join(warnings, " "), "bing") {
		t.Fatalf("warnings = %v, want bing failure", warnings)
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
	results, err := searchSerper(context.Background(), "k", searchRequest{Text: "q", Limit: 5})
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
	results, err := searchExa(context.Background(), "", searchRequest{Text: "query", Limit: 5})
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

func TestRankSearchRewardsExaAndEnginePosition(t *testing.T) {
	exaHit := SearchResult{Title: "Guide to alpha", URL: "https://one.example/a", Snippet: "alpha guide", engine: "exa", rank: 1}
	ddgHit := SearchResult{Title: "Guide to alpha", URL: "https://two.example/b", Snippet: "alpha guide", engine: "duckduckgo", rank: 1}
	got := rankSearchResults("alpha guide", []searchBatch{
		{engine: "exa", results: []SearchResult{exaHit}},
		{engine: "duckduckgo", results: []SearchResult{ddgHit}},
	}, 2)
	if len(got) != 2 || got[0].URL != exaHit.URL {
		t.Fatalf("exa result was not preferred: %+v", got)
	}

	first := SearchResult{Title: "Alpha guide", URL: "https://three.example/a", Snippet: "alpha guide", engine: "duckduckgo", rank: 1}
	second := SearchResult{Title: "Alpha guide", URL: "https://four.example/b", Snippet: "alpha guide", engine: "duckduckgo", rank: 2}
	got = rankSearchResults("alpha guide", []searchBatch{{engine: "duckduckgo", results: []SearchResult{first, second}}}, 2)
	if len(got) != 2 || got[0].URL != first.URL {
		t.Fatalf("engine position was ignored: %+v", got)
	}
}

func TestRankSearchFreshnessDropsOldResults(t *testing.T) {
	old := SearchResult{Title: "Alpha old", URL: "https://old.example/a", Snippet: "alpha", Published: "2020-01-01"}
	fresh := SearchResult{Title: "Alpha new", URL: "https://new.example/a", Snippet: "alpha", Published: time.Now().Format("2006-01-02")}
	got := rankSearchResultsWithOptions("alpha", []searchBatch{{results: []SearchResult{old, fresh}}}, 8, rankOptions{
		Freshness: "month", MaxPerHost: 3,
	})
	if len(got) != 1 || got[0].URL != fresh.URL {
		t.Fatalf("freshness filter = %+v", got)
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
	if perHost["go.dev"] > 3 {
		t.Fatalf("too many results from one domain: %+v", perHost)
	}
	if perHost["go.dev"] == 0 || perHost["blog.golang.org"] == 0 {
		t.Fatalf("two-pass selection dropped a domain: %+v", perHost)
	}
}

func TestExtractPageMetadata(t *testing.T) {
	doc := `<html><head>
<title>Fallback title</title>
<meta property="og:title" content="Open Graph title">
<meta name="description" content="A useful description">
<meta name="author" content="Jane Doe">
<meta property="article:published_time" content="2025-03-04T10:00:00Z">
</head><body>body</body></html>`
	md := extractPageMetadata(doc)
	if md.Title != "Open Graph title" || md.Description != "A useful description" {
		t.Fatalf("metadata = %+v", md)
	}
	if md.Author != "Jane Doe" || md.Published != "2025-03-04" {
		t.Fatalf("metadata = %+v", md)
	}
}

func TestEnrichSearchResultsAddsExcerptAndMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head>
<title>Go concurrency</title>
<meta name="author" content="Jane Doe">
<meta property="article:published_time" content="2025-03-04">
</head><body><article>
<p>Goroutines and channels are the core of Go concurrency. A goroutine is a lightweight thread managed by the Go runtime, and channels let goroutines communicate safely.</p>
</article></body></html>`)
	}))
	defer srv.Close()

	results := []SearchResult{{Title: "Go concurrency", URL: srv.URL + "/guide"}}
	got := enrichSearchResults(context.Background(), results, "goroutines channels", "auto")
	if len(got) != 1 {
		t.Fatalf("results = %+v", got)
	}
	if !strings.Contains(got[0].Excerpt, "goroutines") {
		t.Fatalf("excerpt = %q", got[0].Excerpt)
	}
	if got[0].Author != "Jane Doe" || got[0].Published != "2025-03-04" {
		t.Fatalf("metadata = %+v", got[0])
	}
}

func TestWebSearchToolOutputsPageMetadata(t *testing.T) {
	for _, key := range []string{"BRAVE_API_KEY", "TAVILY_API_KEY", "SERPER_API_KEY", "EXA_API_KEY"} {
		t.Setenv(key, "")
	}
	oldExa, oldTavily, oldMojeek := exaMCPURL, tavilyAPIURL, mojeekRSSURL
	exaMCPURL, tavilyAPIURL, mojeekRSSURL = "", "", ""
	defer func() { exaMCPURL, tavilyAPIURL, mojeekRSSURL = oldExa, oldTavily, oldMojeek }()
	clearSearchCache()

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>Go concurrency</title>
<meta name="author" content="Jane Doe">
<meta property="article:published_time" content="2025-03-04">
</head><body><article><p>Goroutines and channels are the core of Go concurrency.</p></article></body></html>`)
	}))
	defer page.Close()
	bing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<rss><channel><item><title>Go concurrency</title><link>%s/guide</link><description>goroutines and channels</description></item></channel></rss>`, page.URL)
	}))
	defer bing.Close()
	oldBing, oldDDG := bingRSSURL, ddgHTMLURL
	bingRSSURL, ddgHTMLURL = bing.URL+"/?q=", ""
	defer func() { bingRSSURL, ddgHTMLURL = oldBing, oldDDG }()

	out, err := (&WebSearch{}).Run(context.Background(), json.RawMessage(`{"query":"goroutines channels","limit":3}`), &Env{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Go concurrency", "page:", "published: 2025-03-04", "author: Jane Doe", "goroutines"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestWebSearchToolRejectsBadDepth(t *testing.T) {
	_, err := (&WebSearch{}).Run(context.Background(), json.RawMessage(`{"query":"go","depth":"wide"}`), &Env{})
	if err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchBravePaginationAndFilters(t *testing.T) {
	var got map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		fmt.Fprint(w, `{"web":{"results":[{"title":"One","url":"https://one.example","description":"first"}]}}`)
	}))
	defer srv.Close()
	old := braveAPIURL
	braveAPIURL = srv.URL + "/res/v1/web/search?q="
	defer func() { braveAPIURL = old }()

	results, err := searchBrave(context.Background(), "k", searchRequest{
		Text: "go", Limit: 5, Page: 2, Language: "en", Region: "us", Freshness: "week",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "One" {
		t.Fatalf("results = %+v", results)
	}
	for key, want := range map[string]string{
		"count": "5", "offset": "1", "search_lang": "en", "country": "US", "freshness": "pw",
	} {
		if got[key][0] != want {
			t.Fatalf("%s = %q, want %q (query %v)", key, got[key][0], want, got)
		}
	}
}

func TestSearchMetadataPlaceholdersAreDropped(t *testing.T) {
	for _, value := range []string{"N/A", "n/a", "unknown", "-"} {
		if got := normalizeDate(value); got != "" {
			t.Fatalf("normalizeDate(%q) = %q, want empty", value, got)
		}
		if got := cleanMetaValue(value); got != "" {
			t.Fatalf("cleanMetaValue(%q) = %q, want empty", value, got)
		}
	}
}

package tools

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/browser"
)

func TestBrowserToolScreenshotAndInteraction(t *testing.T) {
	if _, ok := browser.FindExecutable(); !ok {
		t.Skip("no Chromium browser installed")
	}
	page := `<!doctype html><html><head><title>Wombat UI</title></head><body>
<button id="go" onclick="document.getElementById('out').textContent='clicked'">Go</button>
<input id="name"><div id="out">waiting</div>
</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()
	dir := t.TempDir()
	env := &Env{
		Dir:              dir,
		Permit:           func(string, string) Decision { return Decision{Allow: true} },
		AllowScreenshots: true,
	}
	env.SetSpillDir(filepath.Join(dir, ".tmp"))
	var media []Media
	env.SetMediaSink(&media)
	defer env.CloseBrowser()

	out := run(t, &Browser{}, env, `{"action":"open","url":`+quoteJSON(server.URL)+`}`)
	if !strings.Contains(out, "Wombat UI") {
		t.Fatalf("open = %q", out)
	}
	run(t, &Browser{}, env, `{"action":"click","selector":"#go"}`)
	if got := run(t, &Browser{}, env, `{"action":"text","selector":"#out"}`); got != "clicked" {
		t.Fatalf("text = %q", got)
	}
	run(t, &Browser{}, env, `{"action":"type","selector":"#name","text":"crab"}`)
	if got := run(t, &Browser{}, env, `{"action":"eval","expression":"document.querySelector('#name').value"}`); got != `"crab"` {
		t.Fatalf("eval = %q", got)
	}
	shot := run(t, &Browser{}, env, `{"action":"screenshot","path":"shots/home.png"}`)
	if !strings.Contains(shot, "screenshot (") {
		t.Fatalf("screenshot = %q", shot)
	}
	if len(media) != 1 || media[0].MIME != "image/png" || !bytes.HasPrefix(media[0].Data, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("media = %+v", media)
	}
	if _, err := os.Stat(filepath.Join(dir, "shots")); !os.IsNotExist(err) {
		t.Fatalf("a screenshot wrote to disk: %v", err)
	}
	if got := run(t, &Browser{}, env, `{"action":"resize","width":390,"height":844}`); got != "viewport is 390x844" {
		t.Fatalf("resize = %q", got)
	}
	run(t, &Browser{}, env, `{"action":"close"}`)
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestBrowserScriptBatchesActionsAndCompactsAssertions(t *testing.T) {
	if _, ok := browser.FindExecutable(); !ok {
		t.Skip("no Chromium browser installed")
	}
	page := `<!doctype html><html><head><title>Batch</title><link rel="icon" href="data:,"></head><body>
<button id="go" onclick="document.getElementById('out').textContent='clicked'">Go</button>
<div id="out">waiting</div>
</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()
	env := testEnv(t.TempDir())
	defer env.CloseBrowser()
	args := `{"action":"script","steps":[
		{"action":"open","url":` + quoteJSON(server.URL) + `},
		{"action":"click","selector":"#go"},
		{"action":"eval","expression":"(() => ({ok: document.querySelector('#out').textContent === 'clicked', passed: 1, total: 1, noise: 'drop me'}))()"},
		{"action":"text","selector":"#out","quiet":true}
	]}`
	out := run(t, &Browser{}, env, args)
	if !strings.HasPrefix(out, "script: 4 steps, 1 assertions, 0 failed") {
		t.Fatalf("script = %q", out)
	}
	if !strings.Contains(out, `"ok":true`) || strings.Contains(out, "noise") {
		t.Fatalf("assertion was not compacted: %q", out)
	}
	if strings.Contains(out, "step 4 text") {
		t.Fatalf("quiet text leaked into output: %q", out)
	}
	failed := `{"action":"script","steps":[
		{"action":"eval","expression":"({ok: false, failures: ['button missing']})"}
	]}`
	if err := runErr(t, &Browser{}, env, failed); !strings.Contains(err.Error(), "1 failed") || !strings.Contains(err.Error(), "button missing") {
		t.Fatalf("failure = %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.Dir, "shots")); !os.IsNotExist(err) {
		t.Fatalf("script wrote screenshots to disk: %v", err)
	}
	run(t, &Browser{}, env, `{"action":"close"}`)
}

func TestBrowserSkipsScreenshotsForNonVisionModels(t *testing.T) {
	env := testEnv(t.TempDir())
	env.VisionKnown = true
	env.Vision = false
	out := run(t, &Browser{}, env, `{"action":"screenshot"}`)
	if !strings.Contains(out, "screenshot skipped") {
		t.Fatalf("screenshot output = %q", out)
	}
}

func TestBrowserScreenshotsCanBeDisabled(t *testing.T) {
	env := testEnv(t.TempDir())
	out := run(t, &Browser{}, env, `{"action":"screenshot"}`)
	if !strings.Contains(out, "screenshots are disabled") {
		t.Fatalf("screenshot output = %q", out)
	}
}

func TestBrowserSnapshotReturnsStructuredPage(t *testing.T) {
	if _, ok := browser.FindExecutable(); !ok {
		t.Skip("no Chromium browser installed")
	}
	page := `<!doctype html><html lang="en"><head><title>Snapshot</title><link rel="icon" href="data:,"></head>
<body><h1>Wombat</h1><a href="/next">Next</a><button aria-label="Open menu">Menu</button>
<img src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==" alt="dot">
<div style="position:absolute;left:0;width:1800px;height:20px">wide</div></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()
	env := testEnv(t.TempDir())
	defer env.CloseBrowser()
	run(t, &Browser{}, env, `{"action":"open","url":`+quoteJSON(server.URL)+`}`)
	out := run(t, &Browser{}, env, `{"action":"snapshot"}`)
	for _, want := range []string{"snapshot:", `"title": "Snapshot"`, `"level": 1`, `"text": "Wombat`, `"href": "/next"`, `"alt": "dot"`, `"overflow"`} {
		if !strings.Contains(out, want) {
			t.Errorf("snapshot missing %q:\n%s", want, out[:min(len(out), 4000)])
		}
	}
	if strings.Contains(out, "\x89PNG") {
		t.Error("snapshot should not contain image bytes")
	}
	run(t, &Browser{}, env, `{"action":"close"}`)
}

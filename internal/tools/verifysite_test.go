package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ramsterr/Wombat/internal/browser"
)

func TestVerifySiteAuditsCachesAndReportsFailures(t *testing.T) {
	if _, ok := browser.FindExecutable(); !ok {
		t.Skip("no Chromium browser installed")
	}
	if !nodeAvailable() {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("style.css", `*{box-sizing:border-box} body{margin:0;font-family:sans-serif} .wrap{width:min(100% - 32px,960px);margin:0 auto}`)
	write("app.js", `document.documentElement.dataset.ready = "yes";`)
	write("logo.svg", `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`)
	page := func(title, body string) string {
		return `<!doctype html><html><head><title>` + title + `</title>
<link rel="icon" href="data:,"><link rel="stylesheet" href="style.css"></head>
<body><div class="wrap"><img src="logo.svg" alt=""><h1>` + title + `</h1>` + body + `</div><script src="app.js"></script></body></html>`
	}
	write("index.html", page("Home", `<a href="about.html">About</a>`))
	write("about.html", page("About", `<a href="index.html">Home</a><a href="#details">Details</a><p id="details">Details</p>`))
	write("scripts/check-site.mjs", `console.log("site check ok");`)

	env := testEnv(dir)
	defer env.CloseBrowser()
	out := run(t, &VerifySite{}, env, `{}`)
	var report siteReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("report = %q: %v", out, err)
	}
	if len(report.Warnings) > 0 && report.Checked == 0 {
		t.Skipf("browser unavailable: %v", report.Warnings)
	}
	if !report.OK || len(report.Failures) > 0 {
		t.Fatalf("initial report = %s", out)
	}
	if report.Pages != 2 || report.Checked != 2 || report.Cached != 0 {
		t.Fatalf("initial counts = %+v", report)
	}
	if report.Script != "scripts/check-site.mjs" {
		t.Fatalf("script = %q", report.Script)
	}

	out = run(t, &VerifySite{}, env, `{}`)
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.Checked != 0 || report.Cached != 2 {
		t.Fatalf("cached counts = %+v", report)
	}

	write("about.html", page("About", `<a href="index.html">Home</a><a href="#missing">Broken</a><img src="missing.png" alt="">`))
	out = run(t, &VerifySite{}, env, `{}`)
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.OK || len(report.Failures) == 0 {
		t.Fatalf("expected failures, got %s", out)
	}
	if report.Checked != 1 || report.Cached != 1 {
		t.Fatalf("incremental counts = %+v", report)
	}
	if !strings.Contains(out, "missing.png") || !strings.Contains(out, "#missing") {
		t.Fatalf("failure detail missing: %s", out)
	}

	out = run(t, &VerifySite{}, env, `{"fresh":true}`)
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.Checked != 2 || report.Cached != 0 {
		t.Fatalf("fresh counts = %+v", report)
	}
}

func TestSitePagesScopesToSession(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, at time.Time) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("<!doctype html><title>x</title>"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	write("index.html", now)
	write("old.html", old)
	write("nested/new.html", now)
	write("nested/old.html", old)

	pages, err := sitePages(dir, nil, 50, false, now.Add(-5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"index.html": true, "nested/new.html": true}
	if len(pages) != len(want) {
		t.Fatalf("pages = %v, want %v", pages, want)
	}
	for _, page := range pages {
		if !want[page] {
			t.Errorf("unexpected page %q", page)
		}
	}

	pages, err = sitePages(dir, nil, 50, false, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || pages[0] != "index.html" {
		t.Fatalf("fallback pages = %v, want root index.html", pages)
	}
}

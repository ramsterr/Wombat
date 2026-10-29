package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ramsterr/Wombat/internal/browser"
)

// VerifySite audits a static or local site in one call: it runs the project's
// own check script when it finds one, then checks the pages in Chromium for
// console errors, failed requests, broken links and anchors, missing assets,
// JavaScript syntax errors, and horizontal overflow at desktop and mobile
// widths. Page results are cached by file mtime so an unchanged page is not
// loaded again.
type VerifySite struct{}

type verifySiteArgs struct {
	URL          string   `json:"url"`
	Pages        []string `json:"pages"`
	Script       string   `json:"script"`
	NoScript     bool     `json:"no_script"`
	Fresh        bool     `json:"fresh"`
	All          bool     `json:"all"`
	TimeoutMS    int      `json:"timeout_ms"`
	DesktopWidth int      `json:"desktop_width"`
	MobileWidth  int      `json:"mobile_width"`
	MaxPages     int      `json:"max_pages"`
}

type siteFailure struct {
	Page   string `json:"page,omitempty"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

type siteReport struct {
	OK       bool          `json:"ok"`
	Pages    int           `json:"pages"`
	Checked  int           `json:"checked"`
	Cached   int           `json:"cached"`
	Script   string        `json:"script,omitempty"`
	Scope    string        `json:"scope,omitempty"`
	Warnings []string      `json:"warnings,omitempty"`
	Failures []siteFailure `json:"failures,omitempty"`
	Omitted  int           `json:"omitted,omitempty"`
	Hint     string        `json:"hint,omitempty"`
}

type siteCache struct {
	mu         sync.Mutex
	base       string
	pages      map[string]sitePageEntry
	scriptSig  string
	scriptOut  string
	scriptOK   bool
	scriptDone bool
}

type sitePageEntry struct {
	fingerprint string
	failures    []siteFailure
}

var verifySiteCaches sync.Map

func (t *VerifySite) Name() string  { return "verify_site" }
func (t *VerifySite) Class() string { return "bash" }
func (t *VerifySite) Description() string {
	return "Audit a static or local site in one call. Runs the project's check script when one is found, then loads each page in Chromium and reports console errors, failed network requests, broken local links and anchors, missing assets, JavaScript syntax errors, and horizontal overflow at desktop and mobile widths. By default only HTML pages changed during this session are audited, so unrelated sites in the workspace are ignored; pass pages for an explicit set or all=true to scan everything. Results are cached by file mtime; pass fresh=true to bypass the cache."
}
func (t *VerifySite) Schema() map[string]any {
	return obj(map[string]any{
		"url":           desc("optional base URL of an already-running preview; when omitted a temporary static server serves the working directory"),
		"pages":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "optional page paths to check; default is every .html file"},
		"script":        desc("optional project check script; default is scripts/check-site.mjs, check-site.js, or similar"),
		"no_script":     map[string]any{"type": "boolean", "description": "skip the project's check script"},
		"fresh":         map[string]any{"type": "boolean", "description": "ignore cached page and script results"},
		"all":           map[string]any{"type": "boolean", "description": "audit every HTML page in the workspace instead of only pages changed this session"},
		"timeout_ms":    map[string]any{"type": "integer", "description": "timeout for the project check script and each page load (default 120000 and 20000)"},
		"desktop_width": map[string]any{"type": "integer", "description": "desktop viewport width (default 1280)"},
		"mobile_width":  map[string]any{"type": "integer", "description": "mobile viewport width (default 390)"},
		"max_pages":     map[string]any{"type": "integer", "description": "maximum pages to audit (default 50)"},
	})
}

func (t *VerifySite) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[verifySiteArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if env == nil || env.Dir == "" {
		return "", fmt.Errorf("site verification needs a working directory")
	}
	if a.DesktopWidth <= 0 {
		a.DesktopWidth = 1280
	}
	if a.MobileWidth <= 0 {
		a.MobileWidth = 390
	}
	if a.MaxPages <= 0 {
		a.MaxPages = 50
	}
	if a.TimeoutMS <= 0 {
		a.TimeoutMS = 120000
	}
	root, err := filepath.Abs(env.Dir)
	if err != nil {
		return "", err
	}
	pages, err := sitePages(root, a.Pages, a.MaxPages, a.All, env.SessionStarted)
	if err != nil {
		return "", err
	}
	cacheAny, _ := verifySiteCaches.LoadOrStore(root, &siteCache{pages: map[string]sitePageEntry{}})
	cache := cacheAny.(*siteCache)
	report := siteReport{Pages: len(pages)}
	switch {
	case len(a.Pages) > 0:
		report.Scope = "explicit pages"
	case a.All || env.SessionStarted.IsZero():
		report.Scope = "all HTML pages"
	default:
		report.Scope = "pages changed this session"
	}
	seen := map[string]bool{}
	add := func(page, kind, detail string) {
		detail = compactLine(detail, 400)
		if detail == "" {
			detail = kind
		}
		key := page + "\x00" + kind + "\x00" + detail
		if seen[key] {
			return
		}
		seen[key] = true
		if len(report.Failures) >= 80 {
			report.Omitted++
			return
		}
		report.Failures = append(report.Failures, siteFailure{Page: page, Kind: kind, Detail: detail})
	}

	sig := siteSignature(root)
	scriptPath, scriptRel := findSiteScript(root, env, a.Script)
	if scriptPath != "" && !a.NoScript {
		report.Script = scriptRel
		cache.mu.Lock()
		cached := !a.Fresh && cache.scriptDone && cache.scriptSig == sig
		if cached {
			if !cache.scriptOK {
				add("", "script", cache.scriptOut)
			}
		}
		cache.mu.Unlock()
		if !cached {
			ok, out, runErr := runSiteScript(ctx, env, scriptPath, scriptRel, time.Duration(a.TimeoutMS)*time.Millisecond)
			if runErr != nil {
				report.Warnings = append(report.Warnings, runErr.Error())
			} else {
				cache.mu.Lock()
				cache.scriptSig = sig
				cache.scriptOK = ok
				cache.scriptOut = out
				cache.scriptDone = true
				cache.mu.Unlock()
				if !ok {
					add("", "script", out)
				}
			}
		}
	}

	base := strings.TrimRight(strings.TrimSpace(a.URL), "/")
	stop := func() {}
	if base == "" {
		base, stop, err = serveSite(root)
		if err != nil {
			report.Warnings = append(report.Warnings, "static server: "+err.Error())
		}
	}
	defer stop()

	if len(pages) > 0 && base != "" {
		session, berr := env.BrowserManager().Session(ctx)
		if berr != nil {
			report.Warnings = append(report.Warnings, "browser: "+berr.Error())
		} else if err := session.EnableAudit(ctx); err != nil {
			report.Warnings = append(report.Warnings, "audit setup: "+err.Error())
		} else {
			logicalBase := base
			if strings.TrimSpace(a.URL) == "" {
				logicalBase = "local"
			}
			cache.mu.Lock()
			if cache.base != logicalBase {
				cache.base = logicalBase
				cache.pages = map[string]sitePageEntry{}
			}
			cache.mu.Unlock()
			fetcher := &siteFetcher{client: &http.Client{Timeout: 10 * time.Second}, base: base, root: root, entries: map[string]siteHTTPEntry{}}
			nodeCheck := &siteNodeCheck{available: nodeAvailable()}
			for _, page := range pages {
				pageURL := base + "/" + strings.TrimPrefix(filepath.ToSlash(page), "/")
				if err := env.Check("network", pageURL); err != nil {
					add(page, "network", err.Error())
					continue
				}
				fp := pageFingerprint(root, page, logicalBase, a.DesktopWidth, a.MobileWidth)
				cache.mu.Lock()
				entry, ok := cache.pages[page]
				cache.mu.Unlock()
				if !a.Fresh && ok && entry.fingerprint == fp {
					report.Cached++
					for _, f := range entry.failures {
						add(f.Page, f.Kind, f.Detail)
					}
					continue
				}
				report.Checked++
				failures := auditPage(ctx, session, page, pageURL, root, a, fetcher, nodeCheck)
				for _, f := range failures {
					add(f.Page, f.Kind, f.Detail)
				}
				cache.mu.Lock()
				cache.pages[page] = sitePageEntry{fingerprint: fp, failures: failures}
				cache.mu.Unlock()
			}
		}
	}

	report.OK = len(report.Failures) == 0
	if !report.OK {
		report.Hint = "Fix the failures, then rerun; unchanged pages are cached."
	}
	data, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type siteHTTPEntry struct {
	status int
	body   string
	err    string
}

type siteFetcher struct {
	client  *http.Client
	base    string
	root    string
	entries map[string]siteHTTPEntry
}

func (f *siteFetcher) get(target string) siteHTTPEntry {
	if entry, ok := f.entries[target]; ok {
		return entry
	}
	entry := siteHTTPEntry{}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		entry.err = err.Error()
		f.entries[target] = entry
		return entry
	}
	resp, err := f.client.Do(req)
	if err != nil {
		entry.err = err.Error()
		f.entries[target] = entry
		return entry
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	entry.status = resp.StatusCode
	entry.body = string(body)
	f.entries[target] = entry
	return entry
}

type siteDeps struct {
	Links  []string `json:"links"`
	Assets []string `json:"assets"`
	IDs    []string `json:"ids"`
}

type siteAuditState struct {
	Errors []string `json:"errors"`
	Failed []string `json:"failed"`
}

type siteOverflow struct {
	Overflow  int      `json:"overflow"`
	Width     int      `json:"width"`
	Offenders []string `json:"offenders"`
}

func auditPage(ctx context.Context, session *browser.Session, page, pageURL, root string, a verifySiteArgs, fetcher *siteFetcher, nodeCheck *siteNodeCheck) []siteFailure {
	var failures []siteFailure
	add := func(kind, detail string) {
		failures = append(failures, siteFailure{Page: page, Kind: kind, Detail: compactLine(detail, 400)})
	}
	_ = session.Resize(ctx, a.DesktopWidth, 900)
	if _, err := session.Navigate(ctx, pageURL); err != nil {
		add("load", err.Error())
		return failures
	}
	raw, err := session.Eval(ctx, `(() => { const a = window.__wombatAudit || {}; return {errors: (a.errors || []).slice(0, 50), failed: (a.failed || []).slice(0, 50)}; })()`)
	if err == nil {
		var state siteAuditState
		if json.Unmarshal(raw, &state) == nil {
			for _, message := range state.Errors {
				if !strings.Contains(message, "favicon.ico") {
					add("console", message)
				}
			}
			for _, message := range state.Failed {
				if !strings.Contains(message, "favicon.ico") {
					add("network", message)
				}
			}
		}
	}
	raw, err = session.Eval(ctx, `(() => ({
		links: Array.from(document.querySelectorAll("a[href]"), e => e.getAttribute("href")),
		assets: Array.from(document.querySelectorAll("img[src],script[src],link[href],source[src],video[poster]"), e => e.getAttribute("src") || e.getAttribute("href") || e.getAttribute("poster")),
		ids: Array.from(document.querySelectorAll("[id]"), e => e.id)
	}))()`)
	var deps siteDeps
	if err == nil {
		_ = json.Unmarshal(raw, &deps)
	}
	checkOverflow := func(label string, width int) {
		raw, err := session.Eval(ctx, `(() => { const d = document.documentElement, b = document.body; const w = window.innerWidth; const over = Math.max(d ? d.scrollWidth : 0, b ? b.scrollWidth : 0) - w; const bad = []; if (over > 1) { for (const el of document.querySelectorAll("*")) { const r = el.getBoundingClientRect(); if (r.width > 0 && r.right > w + 1) { bad.push((el.tagName || "") + "." + String(el.className || "")); if (bad.length >= 3) break; } } } return {overflow: over, width: w, offenders: bad}; })()`)
		if err != nil {
			return
		}
		var over siteOverflow
		if json.Unmarshal(raw, &over) != nil || over.Overflow <= 1 {
			return
		}
		detail := fmt.Sprintf("%s viewport %dpx is %dpx wider", label, width, over.Overflow)
		if len(over.Offenders) > 0 {
			detail += ": " + strings.Join(over.Offenders, ", ")
		}
		add("overflow", detail)
	}
	checkOverflow("desktop", a.DesktopWidth)
	if err := session.Resize(ctx, a.MobileWidth, 844); err == nil {
		checkOverflow("mobile", a.MobileWidth)
	}

	pageParsed, _ := url.Parse(pageURL)
	ids := map[string]bool{}
	for _, id := range deps.IDs {
		ids[id] = true
	}
	checked := map[string]bool{}
	for _, href := range deps.Links {
		href = strings.TrimSpace(href)
		if href == "" || strings.HasPrefix(href, "javascript:") || strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "tel:") || strings.HasPrefix(href, "data:") {
			continue
		}
		if strings.HasPrefix(href, "#") {
			id := strings.TrimPrefix(href, "#")
			if id != "" && !ids[id] {
				add("anchor", "missing #"+id)
			}
			continue
		}
		ref, err := url.Parse(href)
		if err != nil {
			add("link", "bad URL: "+href)
			continue
		}
		target := pageParsed.ResolveReference(ref)
		if target.Host != pageParsed.Host || (target.Scheme != "http" && target.Scheme != "https") {
			continue
		}
		fragment := target.Fragment
		target.Fragment = ""
		key := target.String()
		if checked[key+"#"+fragment] {
			continue
		}
		checked[key+"#"+fragment] = true
		entry := fetcher.get(key)
		if entry.err != "" {
			add("link", key+": "+entry.err)
			continue
		}
		if entry.status >= 400 {
			add("link", fmt.Sprintf("%s: HTTP %d", key, entry.status))
			continue
		}
		if fragment != "" && !strings.Contains(entry.body, `id="`+fragment+`"`) && !strings.Contains(entry.body, `id='`+fragment+`'`) && !strings.Contains(entry.body, `name="`+fragment+`"`) {
			add("anchor", key+"#"+fragment+": fragment not found")
		}
	}
	for _, src := range deps.Assets {
		src = strings.TrimSpace(src)
		if src == "" || strings.HasPrefix(src, "data:") || strings.HasPrefix(src, "blob:") || strings.HasPrefix(src, "javascript:") {
			continue
		}
		ref, err := url.Parse(src)
		if err != nil {
			add("asset", "bad URL: "+src)
			continue
		}
		target := pageParsed.ResolveReference(ref)
		if target.Host != pageParsed.Host || (target.Scheme != "http" && target.Scheme != "https") {
			continue
		}
		target.Fragment = ""
		key := target.String()
		if checked[key] {
			continue
		}
		checked[key] = true
		entry := fetcher.get(key)
		if entry.err != "" {
			add("asset", key+": "+entry.err)
			continue
		}
		if entry.status >= 400 {
			add("asset", fmt.Sprintf("%s: HTTP %d", key, entry.status))
			continue
		}
		if file := localFilePath(root, target.Path); file != "" && strings.HasSuffix(strings.ToLower(file), ".js") {
			if failure := nodeCheck.check(ctx, file); failure != "" {
				add("syntax", failure)
			}
		}
	}
	return failures
}

func findSiteScript(root string, env *Env, explicit string) (string, string) {
	candidates := []string{}
	if strings.TrimSpace(explicit) != "" {
		candidates = append(candidates, explicit)
	} else {
		candidates = append(candidates,
			"scripts/check-site.mjs", "scripts/check-site.js",
			"scripts/check_site.mjs", "scripts/check_site.js",
			"check-site.mjs", "check-site.js",
		)
	}
	for _, candidate := range candidates {
		path, err := env.ResolvePath(candidate)
		if err != nil {
			continue
		}
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			rel, err := filepath.Rel(root, path)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			return path, filepath.ToSlash(rel)
		}
	}
	return "", ""
}

func runSiteScript(ctx context.Context, env *Env, path, rel string, timeout time.Duration) (bool, string, error) {
	if _, err := exec.LookPath("node"); err != nil {
		return false, "", errors.New("check script found but node is not installed")
	}
	command := "node " + shellQuote(rel)
	if err := env.checkPolicy("bash", command); err != nil {
		return false, "", err
	}
	if err := env.permitPreview("bash", "verify_site: "+command, ""); err != nil {
		return false, "", err
	}
	res := RunCommand(ctx, env, command, timeout)
	out := res.Display()
	if len(out) > 6000 {
		out = out[:6000] + "\n… output truncated …"
	}
	if out == "" {
		out = "check script returned no output"
	}
	return !res.Failed(), out, nil
}

func serveSite(root string) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", func() {}, err
	}
	server := &http.Server{Handler: http.FileServer(http.Dir(root))}
	go func() { _ = server.Serve(listener) }()
	stop := func() {
		_ = server.Close()
		_ = listener.Close()
	}
	return "http://" + listener.Addr().String(), stop, nil
}

func sitePages(root string, explicit []string, max int, all bool, since time.Time) ([]string, error) {
	seen := map[string]bool{}
	var pages []string
	add := func(rel string) {
		rel = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(rel)), "/")
		if rel == "" || rel == "." || strings.HasPrefix(rel, "..") || seen[rel] {
			return
		}
		seen[rel] = true
		pages = append(pages, rel)
	}
	if len(explicit) > 0 {
		for _, item := range explicit {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if u, err := url.Parse(item); err == nil && u.Path != "" && !strings.HasPrefix(item, "/") {
				item = u.Path
			}
			add(item)
		}
		sort.Strings(pages)
		if len(pages) > max {
			pages = pages[:max]
		}
		return pages, nil
	}
	cutoff := time.Time{}
	if !all && !since.IsZero() {
		// A small grace window catches files written just before the session
		// was created without pulling in older, unrelated sites.
		cutoff = since.Add(-5 * time.Minute)
	}
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if current != root && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			switch entry.Name() {
			case ".git", "node_modules", ".wombat", "vendor", "dist", "build", ".next", "coverage", ".cache":
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".html" && ext != ".htm" {
			return nil
		}
		if !cutoff.IsZero() {
			info, infoErr := entry.Info()
			if infoErr != nil || info.ModTime().Before(cutoff) {
				return nil
			}
		}
		rel, err := filepath.Rel(root, current)
		if err == nil {
			add(rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !cutoff.IsZero() && len(pages) == 0 {
		// With no changed pages, keep the audit useful by checking the
		// workspace's root page rather than walking unrelated projects.
		for _, name := range []string{"index.html", "index.htm"} {
			if st, statErr := os.Stat(filepath.Join(root, name)); statErr == nil && !st.IsDir() {
				add(name)
			}
		}
	}
	sort.Strings(pages)
	if len(pages) > max {
		pages = pages[:max]
	}
	return pages, nil
}

func pageFingerprint(root, page, base string, desktop, mobile int) string {
	file := filepath.Join(root, filepath.FromSlash(page))
	data, _ := os.ReadFile(file)
	h := fnv.New64a()
	fmt.Fprintf(h, "%s|%s|%d|%d|", page, base, desktop, mobile)
	h.Write(data)
	for _, ref := range localRefs(string(data)) {
		dep := filepath.Join(filepath.Dir(file), filepath.FromSlash(ref))
		if st, err := os.Stat(dep); err == nil {
			fmt.Fprintf(h, "%s|%d|%d|", ref, st.ModTime().UnixNano(), st.Size())
		}
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

var (
	refSourcePattern = regexp.MustCompile(`(?is)\b(?:src|poster)\s*=\s*["']([^"']+)["']`)
	refLinkPattern   = regexp.MustCompile(`(?is)<link\b[^>]*\bhref\s*=\s*["']([^"']+)["']`)
)

func localRefs(html string) []string {
	matches := append(refSourcePattern.FindAllStringSubmatch(html, -1), refLinkPattern.FindAllStringSubmatch(html, -1)...)
	seen := map[string]bool{}
	var refs []string
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		ref := strings.TrimSpace(match[1])
		if ref == "" || strings.HasPrefix(ref, "#") || strings.HasPrefix(ref, "//") || strings.Contains(ref, "://") || strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, "mailto:") || strings.HasPrefix(ref, "tel:") {
			continue
		}
		ref = strings.SplitN(ref, "?", 2)[0]
		ref = strings.SplitN(ref, "#", 2)[0]
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	return refs
}

func siteSignature(root string) string {
	h := fnv.New64a()
	files := 0
	_ = filepath.WalkDir(root, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", ".wombat", "vendor", "dist", "build", ".next", "coverage", ".cache":
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".html", ".htm", ".css", ".js", ".mjs", ".cjs", ".json", ".svg", ".md":
		default:
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, current)
		fmt.Fprintf(h, "%s|%d|%d|", filepath.ToSlash(rel), info.ModTime().UnixNano(), info.Size())
		files++
		if files > 20000 {
			return filepath.SkipAll
		}
		return nil
	})
	return strconv.FormatUint(h.Sum64(), 16)
}

func localFilePath(root, urlPath string) string {
	clean := path.Clean("/" + urlPath)
	if clean == "/" {
		return ""
	}
	file := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	abs, err := filepath.Abs(file)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") || strings.Contains(rel, "node_modules") {
		return ""
	}
	return abs
}

type siteNodeCheck struct {
	available bool
	mu        sync.Mutex
	seen      map[string]bool
}

func (c *siteNodeCheck) check(ctx context.Context, file string) string {
	if c == nil || !c.available {
		return ""
	}
	c.mu.Lock()
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	if c.seen[file] {
		c.mu.Unlock()
		return ""
	}
	c.seen[file] = true
	c.mu.Unlock()
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "node", "--check", file)
	out, err := cmd.CombinedOutput()
	if err != nil {
		message := compactLine(strings.TrimSpace(string(out)), 300)
		if message == "" {
			message = err.Error()
		}
		return filepath.Base(file) + ": " + message
	}
	return ""
}

func nodeAvailable() bool {
	_, err := exec.LookPath("node")
	return err == nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

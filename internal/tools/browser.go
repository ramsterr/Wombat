package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/browser"
)

// Browser drives a real Chromium page, so frontend work can be opened,
// inspected, clicked, and screenshotted instead of guessed at.
type Browser struct{}

type browserArgs struct {
	Action     string        `json:"action"`
	URL        string        `json:"url"`
	Selector   string        `json:"selector"`
	Text       string        `json:"text"`
	Key        string        `json:"key"`
	Expression string        `json:"expression"`
	FullPage   bool          `json:"full_page"`
	Width      int           `json:"width"`
	Height     int           `json:"height"`
	X          int           `json:"x"`
	Y          int           `json:"y"`
	TimeoutMS  int           `json:"timeout_ms"`
	Clear      *bool         `json:"clear"`
	Steps      []browserArgs `json:"steps"`
	Quiet      bool          `json:"quiet"`
	Verbose    bool          `json:"verbose"`
}

func (t *Browser) Name() string  { return "browser" }
func (t *Browser) Class() string { return "network" }
func (t *Browser) Description() string {
	return "Control a local Chromium browser: open a page, read its text, click, type, press keys, scroll, run JavaScript, resize the viewport, and take screenshots. Use action=snapshot for a compact text view of layout, headings, links, controls, images, overflow, and audit errors — cheaper and more general than a screenshot. Use action=script to run a whole batch (navigate, eval, click, snapshot, screenshot) in one call; it returns one compact line per assertion and full details only for failures, so use it instead of many separate calls. Write eval assertions as {ok, failures}; assertion JSON is compacted automatically. Screenshots are controlled by `browser_screenshots` (on by default), attached in memory, and never written to disk. Use eval assertions and verify_site for routine checks. After a screenshot, batch every visible fix before taking another. Use browser for local UI and web apps instead of guessing from HTML."
}
func (t *Browser) Schema() map[string]any {
	return obj(map[string]any{
		"action":     map[string]any{"type": "string", "enum": []string{"open", "navigate", "snapshot", "screenshot", "click", "type", "press", "scroll", "eval", "text", "wait", "resize", "back", "forward", "reload", "script", "close"}},
		"steps":      map[string]any{"type": "array", "description": "for action=script: the ordered browser steps to run in one call", "maxItems": 50, "items": map[string]any{"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string", "enum": []string{"open", "navigate", "snapshot", "screenshot", "click", "type", "press", "scroll", "eval", "text", "wait", "resize", "back", "forward", "reload"}}, "url": desc("URL for open or navigate"), "selector": desc("CSS selector"), "text": desc("text to type"), "key": desc("key to press"), "expression": desc("JavaScript expression for eval; return {ok, failures} for assertions"), "full_page": map[string]any{"type": "boolean"}, "width": map[string]any{"type": "integer"}, "height": map[string]any{"type": "integer"}, "timeout_ms": map[string]any{"type": "integer"}, "quiet": map[string]any{"type": "boolean", "description": "suppress this step's successful output"}, "verbose": map[string]any{"type": "boolean", "description": "include this step's successful output"}}, "required": []string{"action"}}},
		"quiet":      map[string]any{"type": "boolean", "description": "suppress successful step output in a script"},
		"verbose":    map[string]any{"type": "boolean", "description": "include every successful step output in a script"},
		"url":        desc("URL to open; http, https, file, and data URLs are supported"),
		"selector":   desc("CSS selector for click, type, text, scroll, or wait"),
		"text":       desc("text to type"),
		"key":        desc("key to press, such as Enter, Tab, Escape, ArrowDown, or a single character"),
		"expression": desc("JavaScript expression for eval"),
		"full_page":  map[string]any{"type": "boolean", "description": "capture the whole page instead of the viewport"},
		"width":      map[string]any{"type": "integer", "description": "viewport width for resize (default 1280)"},
		"height":     map[string]any{"type": "integer", "description": "viewport height for resize (default 900)"},
		"x":          map[string]any{"type": "integer", "description": "horizontal scroll delta"},
		"y":          map[string]any{"type": "integer", "description": "vertical scroll delta"},
		"timeout_ms": map[string]any{"type": "integer", "description": "wait timeout in milliseconds (default 5000)"},
		"clear":      map[string]any{"type": "boolean", "description": "clear the field before typing (default true)"},
	}, "action")
}

func (t *Browser) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[browserArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	action := strings.ToLower(strings.TrimSpace(a.Action))
	if action == "" {
		return "", fmt.Errorf("action is required")
	}
	if env == nil {
		return "", fmt.Errorf("browser is unavailable")
	}
	if action == "script" {
		return t.runScript(ctx, env, a)
	}
	if action == "close" {
		env.CloseBrowser()
		return "browser closed", nil
	}
	session, err := env.BrowserManager().Session(ctx)
	if err != nil {
		return "", err
	}
	return t.runAction(ctx, env, session, action, a)
}

func (t *Browser) runAction(ctx context.Context, env *Env, session *browser.Session, action string, a browserArgs) (string, error) {
	timeout := time.Duration(a.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	switch action {
	case "open", "navigate":
		target := strings.TrimSpace(a.URL)
		if target == "" {
			return "", fmt.Errorf("url is required")
		}
		if !strings.Contains(target, "://") && !strings.HasPrefix(target, "data:") {
			target = "https://" + target
		}
		if err := env.Check("network", target); err != nil {
			return "", err
		}
		info, err := session.Navigate(ctx, target)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("opened %s\n%s", info.URL, info.Title), nil
	case "snapshot":
		raw, err := session.Eval(ctx, pageSnapshotJS)
		if err != nil {
			return "", err
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return "snapshot: " + string(raw), nil
		}
		pretty, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return "snapshot: " + string(raw), nil
		}
		return "snapshot:\n" + string(pretty), nil
	case "screenshot":
		if !env.AllowScreenshots {
			return "screenshot skipped: browser screenshots are disabled; use DOM eval assertions or verify_site. Enable them with `wombat config set browser_screenshots true` if you need an image.", nil
		}
		if env.VisionKnown && !env.Vision {
			return "screenshot skipped: this model cannot see images; use eval assertions or text instead, and leave screenshots for a vision model.", nil
		}
		shot, err := session.Screenshot(ctx, a.FullPage)
		if err != nil {
			return "", err
		}
		width, height := 0, 0
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(shot)); err == nil {
			width, height = cfg.Width, cfg.Height
		}
		name := "screenshot.png"
		if width > 0 && height > 0 {
			name = fmt.Sprintf("screenshot-%dx%d.png", width, height)
		}
		env.AddMedia(Media{Data: shot, Size: int64(len(shot)), MIME: "image/png", Name: name})
		return fmt.Sprintf("screenshot (%dx%d, %d bytes); attached for you to look at. List every visible defect in this image, fix them all in one batch, then take at most one verification screenshot; do not screenshot after each individual fix.", width, height, len(shot)), nil
	case "click":
		if strings.TrimSpace(a.Selector) == "" {
			return "", fmt.Errorf("selector is required")
		}
		if err := session.Click(ctx, a.Selector); err != nil {
			return "", err
		}
		return "clicked " + a.Selector, nil
	case "type":
		if strings.TrimSpace(a.Selector) == "" {
			return "", fmt.Errorf("selector is required")
		}
		clear := true
		if a.Clear != nil {
			clear = *a.Clear
		}
		value, err := session.Type(ctx, a.Selector, a.Text, clear)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("typed into %s; value is %q", a.Selector, value), nil
	case "press":
		if a.Key == "" {
			return "", fmt.Errorf("key is required")
		}
		if err := session.Press(ctx, a.Key); err != nil {
			return "", err
		}
		return "pressed " + a.Key, nil
	case "scroll":
		if a.Selector == "" && a.X == 0 && a.Y == 0 {
			a.Y = 600
		}
		if err := session.Scroll(ctx, a.Selector, a.X, a.Y); err != nil {
			return "", err
		}
		return "scrolled", nil
	case "eval":
		if strings.TrimSpace(a.Expression) == "" {
			return "", fmt.Errorf("expression is required")
		}
		value, err := session.Eval(ctx, a.Expression)
		if err != nil {
			return "", err
		}
		if len(value) == 0 || string(value) == "null" {
			return "undefined", nil
		}
		if compact, ok := compactAssertion(string(value)); ok && !a.Verbose {
			return compact, nil
		}
		return Truncate(string(value), 0, 0, 32*1024), nil
	case "text":
		text, err := session.Text(ctx, a.Selector)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) == "" {
			return "(no text)", nil
		}
		return Truncate(text, 0, 0, 32*1024), nil
	case "wait":
		if a.Selector == "" {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(timeout):
				return fmt.Sprintf("waited %s", timeout), nil
			}
		}
		if err := session.Wait(ctx, a.Selector, timeout); err != nil {
			return "", err
		}
		return "found " + a.Selector, nil
	case "resize":
		width, height := a.Width, a.Height
		if width <= 0 {
			width = 1280
		}
		if height <= 0 {
			height = 900
		}
		if err := session.Resize(ctx, width, height); err != nil {
			return "", err
		}
		return fmt.Sprintf("viewport is %dx%d", width, height), nil
	case "back":
		info, err := session.Back(ctx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("opened %s\n%s", info.URL, info.Title), nil
	case "forward":
		info, err := session.Forward(ctx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("opened %s\n%s", info.URL, info.Title), nil
	case "reload":
		info, err := session.Reload(ctx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("reloaded %s\n%s", info.URL, info.Title), nil
	}
	return "", fmt.Errorf("action must be open, navigate, snapshot, screenshot, click, type, press, scroll, eval, text, wait, resize, back, forward, reload, script, or close")
}

// runScript executes several browser actions in one round-trip. Successful
// navigation and interaction output is suppressed unless verbose is set;
// passing assertions are one compact line, and failures include their detail.
func (t *Browser) runScript(ctx context.Context, env *Env, a browserArgs) (string, error) {
	if len(a.Steps) == 0 {
		return "", fmt.Errorf("script needs at least one step")
	}
	if len(a.Steps) > 50 {
		return "", fmt.Errorf("script takes at most 50 steps, got %d", len(a.Steps))
	}
	session, err := env.BrowserManager().Session(ctx)
	if err != nil {
		return "", err
	}
	var details []string
	failed, assertions := 0, 0
	for i, step := range a.Steps {
		action := strings.ToLower(strings.TrimSpace(step.Action))
		if action == "" {
			return "", fmt.Errorf("step %d: action is required", i+1)
		}
		if action == "script" || action == "close" {
			return "", fmt.Errorf("step %d: %s is not allowed inside a script", i+1, action)
		}
		step.Verbose = a.Verbose || step.Verbose
		step.Quiet = a.Quiet || step.Quiet
		verbose, quiet := step.Verbose, step.Quiet
		out, err := t.runAction(ctx, env, session, action, step)
		if err != nil {
			failed++
			details = append(details, fmt.Sprintf("step %d %s: %s", i+1, action, compactLine(err.Error(), 500)))
			continue
		}
		switch action {
		case "eval":
			compact, assertion := compactAssertion(out)
			if assertion {
				assertions++
				var parsed struct {
					OK *bool `json:"ok"`
				}
				_ = json.Unmarshal([]byte(compact), &parsed)
				if parsed.OK != nil && !*parsed.OK {
					failed++
					details = append(details, fmt.Sprintf("step %d eval: %s", i+1, compact))
				} else if !quiet {
					details = append(details, fmt.Sprintf("step %d eval: %s", i+1, compact))
				}
				continue
			}
			if !quiet {
				details = append(details, fmt.Sprintf("step %d eval: %s", i+1, compactLine(out, 500)))
			}
		case "text":
			if !quiet {
				details = append(details, fmt.Sprintf("step %d text: %s", i+1, compactLine(out, 500)))
			}
		case "snapshot":
			details = append(details, fmt.Sprintf("step %d snapshot: %s", i+1, compactLine(out, 2000)))
		case "screenshot":
			details = append(details, fmt.Sprintf("step %d screenshot: %s", i+1, out))
		default:
			if verbose {
				details = append(details, fmt.Sprintf("step %d %s: %s", i+1, action, compactLine(out, 300)))
			}
		}
	}
	header := fmt.Sprintf("script: %d steps, %d assertions, %d failed", len(a.Steps), assertions, failed)
	body := strings.Join(details, "\n")
	if failed > 0 {
		if body != "" {
			return "", fmt.Errorf("%s\n%s", header, body)
		}
		return "", fmt.Errorf("%s", header)
	}
	if body == "" {
		return header, nil
	}
	return header + "\n" + body, nil
}

// compactAssertion recognizes an eval result shaped like an assertion and
// keeps only the fields needed to decide whether it passed.
func compactAssertion(raw string) (string, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil || len(m) == 0 {
		return "", false
	}
	_, hasOK := m["ok"]
	_, hasFailures := m["failures"]
	_, hasErrors := m["errors"]
	if !hasOK && !hasFailures && !hasErrors {
		return "", false
	}
	out := make(map[string]any, 6)
	for _, key := range []string{"ok", "failures", "errors", "failed", "passed", "total", "count"} {
		value, ok := m[key]
		if !ok {
			continue
		}
		var decoded any
		if json.Unmarshal(value, &decoded) == nil {
			out[key] = decoded
		}
	}
	if _, ok := out["ok"]; !ok {
		out["ok"] = emptyJSONList(out["failures"]) && emptyJSONList(out["errors"])
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func emptyJSONList(v any) bool {
	if v == nil {
		return true
	}
	list, ok := v.([]any)
	return ok && len(list) == 0
}

func compactLine(s string, max int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if max > 0 && len(s) > max {
		return s[:max] + "..."
	}
	return s
}

const pageSnapshotJS = `(() => {
  const clean = (value, max) => String(value == null ? "" : value).replace(/\s+/g, " ").trim().slice(0, max || 120);
  const rect = (el) => {
    const r = el.getBoundingClientRect();
    return { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height) };
  };
  const sel = (el) => {
    if (!el || el.nodeType !== 1) return "";
    if (el.id) return "#" + el.id;
    const parts = [];
    let node = el;
    for (let i = 0; i < 3 && node && node.nodeType === 1; i++) {
      let part = node.tagName.toLowerCase();
      if (node.classList && node.classList.length) part += "." + Array.from(node.classList).slice(0, 2).join(".");
      parts.unshift(part);
      node = node.parentElement;
    }
    return parts.join(" > ");
  };
  const de = document.documentElement;
  const vw = window.innerWidth;
  const vh = window.innerHeight;
  const headings = Array.from(document.querySelectorAll("h1,h2,h3,[role=heading]")).slice(0, 25).map((el) => ({
    level: el.tagName.toLowerCase() === "h1" ? 1 : el.tagName.toLowerCase() === "h2" ? 2 : el.tagName.toLowerCase() === "h3" ? 3 : clean(el.getAttribute("aria-level"), 3),
    text: clean(el.innerText, 160),
    selector: sel(el)
  }));
  const links = Array.from(document.querySelectorAll("a[href]")).slice(0, 30).map((el) => ({
    text: clean(el.innerText || el.getAttribute("aria-label") || el.title, 100),
    href: clean(el.getAttribute("href"), 240),
    selector: sel(el)
  }));
  const controls = Array.from(document.querySelectorAll("button,input,select,textarea,[role=button],[role=link],[role=textbox]")).slice(0, 30).map((el) => ({
    tag: el.tagName.toLowerCase(),
    type: clean(el.getAttribute("type"), 20),
    name: clean(el.getAttribute("aria-label") || el.getAttribute("placeholder") || el.innerText || el.value, 100),
    selector: sel(el),
    disabled: !!el.disabled,
    checked: el.checked === true
  }));
  const images = Array.from(document.images).slice(0, 20).map((img) => ({
    src: clean(img.currentSrc || img.src, 240),
    alt: clean(img.alt, 160),
    loaded: img.complete && img.naturalWidth > 0,
    width: img.naturalWidth,
    height: img.naturalHeight
  }));
  const overflow = [];
  for (const el of document.querySelectorAll("body *")) {
    if (overflow.length >= 20) break;
    const style = getComputedStyle(el);
    if (style.display === "none" || style.visibility === "hidden") continue;
    const r = el.getBoundingClientRect();
    if (r.width <= 0 || r.height <= 0) continue;
    if (r.right > vw + 1 || r.left < -1) {
      overflow.push({ tag: el.tagName.toLowerCase(), selector: sel(el), left: Math.round(r.left), right: Math.round(r.right), width: Math.round(r.width) });
    }
  }
  const audit = window.__wombatAudit || { errors: [], failed: [] };
  const active = document.activeElement;
  return {
    url: location.href,
    title: document.title,
    lang: document.documentElement.lang || "",
    viewport: { width: vw, height: vh, dpr: window.devicePixelRatio || 1 },
    document: { scrollWidth: de.scrollWidth, clientWidth: de.clientWidth, scrollHeight: de.scrollHeight, clientHeight: de.clientHeight },
    activeElement: active ? { tag: active.tagName.toLowerCase(), selector: sel(active) } : null,
    headings: headings,
    links: links,
    controls: controls,
    images: images,
    overflow: overflow,
    audit: { errors: (audit.errors || []).slice(0, 20), failed: (audit.failed || []).slice(0, 20) },
    text: (document.body && document.body.innerText ? document.body.innerText : "").replace(/\s+/g, " ").trim().slice(0, 4000)
  };
})()`

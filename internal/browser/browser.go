// Package browser drives a local Chromium browser over the DevTools protocol.
// It gives the agent eyes and hands for frontend work: navigate, inspect,
// click, type, and take screenshots that can be attached to the conversation.
package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	root    string
	mu      sync.Mutex
	session *Session
	closed  bool
}

func NewManager(root string) *Manager {
	return &Manager{root: root}
}

func (m *Manager) Session(ctx context.Context) (*Session, error) {
	if m == nil {
		return nil, errors.New("browser support is unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("browser manager is closed")
	}
	if m.session != nil && m.session.Alive() {
		return m.session, nil
	}
	if m.session != nil {
		m.session.Close()
		m.session = nil
	}
	s, err := launch(ctx, m.root)
	if err != nil {
		return nil, err
	}
	m.session = s
	return s, nil
}

func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	s := m.session
	m.session = nil
	m.mu.Unlock()
	if s != nil {
		s.Close()
	}
}

type PageInfo struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

type Session struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	dir    string
	conn   *cdp
	stderr *tail
	done   chan error
	audit  bool
}

func launch(ctx context.Context, root string) (*Session, error) {
	executable, ok := FindExecutable()
	if !ok {
		return nil, errors.New("no Chromium browser found; install Chrome/Chromium or set WOMBAT_BROWSER")
	}
	dir, err := os.MkdirTemp("", "wombat-browser-")
	if err != nil {
		return nil, err
	}
	args := []string{
		"--remote-debugging-port=0",
		"--remote-allow-origins=*",
		"--user-data-dir=" + dir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--disable-component-update",
		"--disable-default-apps",
		"--disable-extensions",
		"--disable-sync",
		"--disable-dev-shm-usage",
		"--disable-gpu",
		"--hide-scrollbars",
		"--mute-audio",
		"--no-sandbox",
		"--window-size=1280,900",
		"about:blank",
	}
	if !strings.Contains(filepath.Base(executable), "headless-shell") {
		args = append([]string{"--headless=new"}, args...)
	}
	cmd := exec.Command(executable, args...)
	if root != "" {
		cmd.Dir = root
	}
	stderr := &tail{max: 32 << 10}
	cmd.Stderr = stderr
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("start browser: %w", err)
	}
	s := &Session{cmd: cmd, dir: dir, stderr: stderr, done: make(chan error, 1)}
	go func() { s.done <- cmd.Wait() }()

	cleanup := func(err error) (*Session, error) {
		_ = cmd.Process.Kill()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("%w%s", err, stderr.suffix())
	}
	port, err := waitForPort(ctx, dir, s.done)
	if err != nil {
		return cleanup(err)
	}
	target, err := pageTarget(ctx, port)
	if err != nil {
		return cleanup(err)
	}
	conn, err := dialCDP(ctx, target)
	if err != nil {
		return cleanup(err)
	}
	s.conn = conn
	if err := s.enable(ctx); err != nil {
		conn.Close()
		return cleanup(err)
	}
	return s, nil
}

func waitForPort(ctx context.Context, dir string, done <-chan error) (int, error) {
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	file := filepath.Join(dir, "DevToolsActivePort")
	for {
		if data, err := os.ReadFile(file); err == nil {
			line := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)[0]
			if port, err := strconv.Atoi(line); err == nil && port > 0 {
				return port, nil
			}
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case err := <-done:
			return 0, fmt.Errorf("browser exited during startup: %v", err)
		case <-deadline.C:
			return 0, errors.New("browser did not open a debugging port")
		case <-ticker.C:
		}
	}
}

type targetInfo struct {
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func pageTarget(ctx context.Context, port int) (string, error) {
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	client := &http.Client{Timeout: 5 * time.Second}
	list := func() ([]targetInfo, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/json/list", nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var targets []targetInfo
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&targets); err != nil {
			return nil, err
		}
		return targets, nil
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	newTab := false
	var lastErr error
	for {
		targets, err := list()
		if err == nil {
			for _, target := range targets {
				if target.Type == "page" && target.WebSocketDebuggerURL != "" {
					return target.WebSocketDebuggerURL, nil
				}
			}
			if !newTab {
				newTab = true
				req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/json/new?about:blank", nil)
				if err == nil {
					if resp, err := client.Do(req); err == nil {
						_ = resp.Body.Close()
					}
				}
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			if lastErr != nil {
				return "", fmt.Errorf("browser did not expose a page target: %w", lastErr)
			}
			return "", errors.New("browser did not expose a page target")
		case <-ticker.C:
		}
	}
}

func (s *Session) enable(ctx context.Context) error {
	if err := s.conn.Call(ctx, "Page.enable", nil, nil); err != nil {
		return err
	}
	if err := s.conn.Call(ctx, "Runtime.enable", nil, nil); err != nil {
		return err
	}
	return s.Resize(ctx, 1280, 900)
}

// EnableAudit installs a small script that runs before each new document and
// records console errors, uncaught exceptions, rejected promises, failed
// resource loads, and failed fetch/XHR requests in window.__wombatAudit.
func (s *Session) EnableAudit(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return errors.New("browser is closed")
	}
	if s.audit {
		return nil
	}
	if err := s.conn.Call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": auditShim}, nil); err != nil {
		return err
	}
	s.audit = true
	return nil
}

const auditShim = `(() => {
  if (window.__wombatAuditInstalled) return;
  window.__wombatAuditInstalled = true;
  const audit = window.__wombatAudit = {errors: [], failed: []};
  const push = (list, value) => {
    if (list.length >= 100) return;
    try { list.push(String(value)); } catch (e) {}
  };
  const originalError = console.error;
  console.error = function(...args) {
    push(audit.errors, "console.error: " + args.map(String).join(" "));
    return originalError.apply(this, args);
  };
  window.addEventListener("error", (event) => {
    if (event.target && event.target !== window) {
      const target = event.target;
      const src = target.src || target.href || "";
      if (src) push(audit.failed, "resource failed: " + src);
      return;
    }
    push(audit.errors, "uncaught: " + (event.message || event.error || "error"));
  }, true);
  window.addEventListener("unhandledrejection", (event) => {
    const reason = event.reason;
    push(audit.errors, "unhandled rejection: " + ((reason && reason.message) || reason));
  });
  if (window.fetch) {
    const originalFetch = window.fetch;
    window.fetch = function(...args) {
      const target = String(args[0] && (args[0].url || args[0]));
      return originalFetch.apply(this, args).then((response) => {
        if (!response.ok) push(audit.failed, "fetch " + response.status + ": " + target);
        return response;
      }).catch((error) => {
        push(audit.failed, "fetch failed: " + target + ": " + error);
        throw error;
      });
    };
  }
  if (window.XMLHttpRequest) {
    const open = XMLHttpRequest.prototype.open;
    const send = XMLHttpRequest.prototype.send;
    XMLHttpRequest.prototype.open = function(method, url, ...rest) {
      this.__wombatURL = String(url);
      return open.call(this, method, url, ...rest);
    };
    XMLHttpRequest.prototype.send = function(...args) {
      this.addEventListener("error", () => push(audit.failed, "xhr failed: " + this.__wombatURL));
      this.addEventListener("load", () => {
        if (this.status >= 400) push(audit.failed, "xhr " + this.status + ": " + this.__wombatURL);
      });
      return send.apply(this, args);
    };
  }
})();`

func (s *Session) Alive() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return false
	}
	select {
	case <-s.conn.done:
		return false
	default:
		return true
	}
}

func (s *Session) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	if s.done != nil {
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
		}
	}
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
}

func (s *Session) Navigate(ctx context.Context, target string) (PageInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return PageInfo{}, errors.New("browser is closed")
	}
	var result struct {
		ErrorText string `json:"errorText"`
	}
	if err := s.conn.Call(ctx, "Page.navigate", map[string]any{"url": target}, &result); err != nil {
		return PageInfo{}, err
	}
	if result.ErrorText != "" {
		return PageInfo{}, fmt.Errorf("navigate: %s", result.ErrorText)
	}
	if err := s.waitReady(ctx, 20*time.Second); err != nil {
		return PageInfo{}, err
	}
	return s.info(ctx)
}

func (s *Session) Reload(ctx context.Context) (PageInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return PageInfo{}, errors.New("browser is closed")
	}
	if err := s.conn.Call(ctx, "Page.reload", map[string]any{"ignoreCache": true}, nil); err != nil {
		return PageInfo{}, err
	}
	if err := s.waitReady(ctx, 20*time.Second); err != nil {
		return PageInfo{}, err
	}
	return s.info(ctx)
}

func (s *Session) Back(ctx context.Context) (PageInfo, error) {
	return s.history(ctx, -1)
}

func (s *Session) Forward(ctx context.Context) (PageInfo, error) {
	return s.history(ctx, 1)
}

func (s *Session) history(ctx context.Context, delta int) (PageInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return PageInfo{}, errors.New("browser is closed")
	}
	var history struct {
		CurrentIndex int `json:"currentIndex"`
		Entries      []struct {
			ID int `json:"id"`
		} `json:"entries"`
	}
	if err := s.conn.Call(ctx, "Page.getNavigationHistory", nil, &history); err != nil {
		return PageInfo{}, err
	}
	index := history.CurrentIndex + delta
	if index < 0 || index >= len(history.Entries) {
		return PageInfo{}, errors.New("no page in that direction")
	}
	if err := s.conn.Call(ctx, "Page.navigateToHistoryEntry", map[string]any{"entryId": history.Entries[index].ID}, nil); err != nil {
		return PageInfo{}, err
	}
	if err := s.waitReady(ctx, 20*time.Second); err != nil {
		return PageInfo{}, err
	}
	return s.info(ctx)
}

func (s *Session) info(ctx context.Context) (PageInfo, error) {
	var info PageInfo
	if err := s.evalValue(ctx, "({url: location.href, title: document.title})", &info); err != nil {
		return PageInfo{}, err
	}
	return info, nil
}

func (s *Session) waitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var state string
		if err := s.evalValue(ctx, "document.readyState", &state); err == nil && state == "complete" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("page did not finish loading within %s", timeout)
		case <-ticker.C:
		}
	}
}

func (s *Session) Click(ctx context.Context, selector string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	expr := fmt.Sprintf(`(() => {
		const el = document.querySelector(%s);
		if (!el) throw new Error("selector not found: " + %s);
		el.scrollIntoView({block: "center"});
		el.click();
		return true;
	})()`, jsString(selector), jsString(selector))
	var ok bool
	return s.evalValue(ctx, expr, &ok)
}

func (s *Session) Type(ctx context.Context, selector, text string, clear bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	expr := fmt.Sprintf(`(() => {
		const el = document.querySelector(%s);
		if (!el) throw new Error("selector not found: " + %s);
		el.focus();
		const current = el.isContentEditable ? (el.textContent || "") : (el.value || "");
		const next = %s ? %s : current + %s;
		if (el.isContentEditable) {
			el.textContent = next;
		} else {
			const proto = el.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
			const setter = Object.getOwnPropertyDescriptor(proto, "value")?.set;
			if (setter) setter.call(el, next); else el.value = next;
		}
		el.dispatchEvent(new Event("input", {bubbles: true}));
		el.dispatchEvent(new Event("change", {bubbles: true}));
		return el.isContentEditable ? el.textContent : el.value;
	})()`, jsString(selector), jsString(selector), boolString(clear), jsString(text), jsString(text))
	var value string
	if err := s.evalValue(ctx, expr, &value); err != nil {
		return "", err
	}
	return value, nil
}

func (s *Session) Press(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	info := keyInfo(key)
	base := map[string]any{
		"key": info.key, "code": info.code, "windowsVirtualKeyCode": info.codePoint,
	}
	down := map[string]any{"type": "keyDown"}
	for k, v := range base {
		down[k] = v
	}
	if len(key) == 1 {
		down["text"] = key
		down["unmodifiedText"] = key
	}
	if err := s.conn.Call(ctx, "Input.dispatchKeyEvent", down, nil); err != nil {
		return err
	}
	up := map[string]any{"type": "keyUp"}
	for k, v := range base {
		up[k] = v
	}
	return s.conn.Call(ctx, "Input.dispatchKeyEvent", up, nil)
}

type keyData struct {
	key       string
	code      string
	codePoint int
}

func keyInfo(key string) keyData {
	switch key {
	case "Enter":
		return keyData{"Enter", "Enter", 13}
	case "Tab":
		return keyData{"Tab", "Tab", 9}
	case "Escape", "Esc":
		return keyData{"Escape", "Escape", 27}
	case "Backspace":
		return keyData{"Backspace", "Backspace", 8}
	case "Delete":
		return keyData{"Delete", "Delete", 46}
	case "ArrowUp":
		return keyData{"ArrowUp", "ArrowUp", 38}
	case "ArrowDown":
		return keyData{"ArrowDown", "ArrowDown", 40}
	case "ArrowLeft":
		return keyData{"ArrowLeft", "ArrowLeft", 37}
	case "ArrowRight":
		return keyData{"ArrowRight", "ArrowRight", 39}
	case " ":
		return keyData{" ", "Space", 32}
	}
	if len(key) == 1 {
		return keyData{key, "Key" + strings.ToUpper(key), int(strings.ToUpper(key)[0])}
	}
	return keyData{key, key, 0}
}

func (s *Session) Scroll(ctx context.Context, selector string, x, y int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var expr string
	if selector != "" {
		expr = fmt.Sprintf(`(() => { const el = document.querySelector(%s); if (!el) throw new Error("selector not found"); el.scrollIntoView({block: "center"}); return true; })()`, jsString(selector))
	} else {
		expr = fmt.Sprintf("(() => { window.scrollBy(%d, %d); return true; })()", x, y)
	}
	var ok bool
	return s.evalValue(ctx, expr, &ok)
}

func (s *Session) Text(ctx context.Context, selector string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target := "document.body"
	if selector != "" {
		target = fmt.Sprintf("document.querySelector(%s)", jsString(selector))
	}
	expr := fmt.Sprintf("(() => { const el = %s; if (!el) throw new Error(\"selector not found\"); return el.innerText || el.textContent || \"\"; })()", target)
	var text string
	err := s.evalValue(ctx, expr, &text)
	return text, err
}

func (s *Session) Eval(ctx context.Context, expression string) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.eval(ctx, expression)
}

func (s *Session) evalValue(ctx context.Context, expression string, target any) error {
	raw, err := s.eval(ctx, expression)
	if err != nil {
		return err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, target)
}

func (s *Session) eval(ctx context.Context, expression string) (json.RawMessage, error) {
	if s.conn == nil {
		return nil, errors.New("browser is closed")
	}
	var response struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text        string `json:"text"`
			Description string `json:"description"`
		} `json:"exceptionDetails"`
	}
	params := map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": true}
	if err := s.conn.Call(ctx, "Runtime.evaluate", params, &response); err != nil {
		return nil, err
	}
	if response.Exception != nil {
		message := response.Exception.Description
		if message == "" {
			message = response.Exception.Text
		}
		if message == "" {
			message = "JavaScript exception"
		}
		return nil, errors.New(message)
	}
	return response.Result.Value, nil
}

func (s *Session) Wait(ctx context.Context, selector string, timeout time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if selector == "" {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(timeout):
			return nil
		}
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	expr := fmt.Sprintf("!!document.querySelector(%s)", jsString(selector))
	for {
		var found bool
		if err := s.evalValue(ctx, expr, &found); err == nil && found {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("selector %q did not appear within %s", selector, timeout)
		case <-ticker.C:
		}
	}
}

func (s *Session) Resize(ctx context.Context, width, height int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if width <= 0 {
		width = 1280
	}
	if height <= 0 {
		height = 900
	}
	params := map[string]any{
		"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false,
	}
	return s.conn.Call(ctx, "Emulation.setDeviceMetricsOverride", params, nil)
}

func (s *Session) Screenshot(ctx context.Context, fullPage bool) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	params := map[string]any{"format": "png", "fromSurface": true, "captureBeyondViewport": fullPage}
	if fullPage {
		var metrics struct {
			Content struct {
				Width  float64 `json:"width"`
				Height float64 `json:"height"`
			} `json:"cssContentSize"`
		}
		if err := s.conn.Call(ctx, "Page.getLayoutMetrics", nil, &metrics); err == nil && metrics.Content.Width > 0 && metrics.Content.Height > 0 {
			params["clip"] = map[string]any{
				"x": 0, "y": 0, "scale": 1,
				"width":  math.Min(metrics.Content.Width, 20000),
				"height": math.Min(metrics.Content.Height, 20000),
			}
		}
	}
	var response struct {
		Data string `json:"data"`
	}
	if err := s.conn.Call(ctx, "Page.captureScreenshot", params, &response); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(response.Data)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("browser returned an empty screenshot")
	}
	return data, nil
}

func jsString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

type tail struct {
	mu  sync.Mutex
	buf strings.Builder
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.Write(p)
	if t.buf.Len() > t.max {
		text := t.buf.String()
		t.buf.Reset()
		t.buf.WriteString(text[len(text)-t.max/2:])
	}
	return len(p), nil
}

func (t *tail) suffix() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	text := strings.TrimSpace(t.buf.String())
	if text == "" {
		return ""
	}
	return ": " + text
}

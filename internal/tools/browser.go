package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Browser drives a real Chromium page, so frontend work can be opened,
// inspected, clicked, and screenshotted instead of guessed at.
type Browser struct{}

type browserArgs struct {
	Action     string `json:"action"`
	URL        string `json:"url"`
	Selector   string `json:"selector"`
	Text       string `json:"text"`
	Key        string `json:"key"`
	Expression string `json:"expression"`
	Path       string `json:"path"`
	FullPage   bool   `json:"full_page"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	TimeoutMS  int    `json:"timeout_ms"`
	Clear      *bool  `json:"clear"`
}

func (t *Browser) Name() string  { return "browser" }
func (t *Browser) Class() string { return "network" }
func (t *Browser) Description() string {
	return "Control a local Chromium browser: open a page, read its text, click, type, press keys, scroll, run JavaScript, resize the viewport, and take screenshots. Screenshots are attached so you can visually verify frontend work. Use browser for local UI and web apps instead of guessing from HTML."
}
func (t *Browser) Schema() map[string]any {
	return obj(map[string]any{
		"action":     map[string]any{"type": "string", "enum": []string{"open", "navigate", "screenshot", "click", "type", "press", "scroll", "eval", "text", "wait", "resize", "back", "forward", "reload", "close"}},
		"url":        desc("URL to open; http, https, file, and data URLs are supported"),
		"selector":   desc("CSS selector for click, type, text, scroll, or wait"),
		"text":       desc("text to type"),
		"key":        desc("key to press, such as Enter, Tab, Escape, ArrowDown, or a single character"),
		"expression": desc("JavaScript expression for eval"),
		"path":       desc("optional screenshot output path; defaults to the session scratch directory"),
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
	if action == "close" {
		env.CloseBrowser()
		return "browser closed", nil
	}
	session, err := env.BrowserManager().Session(ctx)
	if err != nil {
		return "", err
	}
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
	case "screenshot":
		shot, err := session.Screenshot(ctx, a.FullPage)
		if err != nil {
			return "", err
		}
		path, rel, err := saveScreenshot(env, a.Path, shot)
		if err != nil {
			return "", err
		}
		width, height := 0, 0
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(shot)); err == nil {
			width, height = cfg.Width, cfg.Height
		}
		env.AddMedia(Media{Path: path, MIME: "image/png", Name: filepath.Base(path)})
		return fmt.Sprintf("screenshot %s (%dx%d, %d bytes); attached for you to look at", filepath.ToSlash(rel), width, height, len(shot)), nil
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
	return "", fmt.Errorf("action must be open, navigate, screenshot, click, type, press, scroll, eval, text, wait, resize, back, forward, reload, or close")
}

func saveScreenshot(env *Env, requested string, data []byte) (path, rel string, err error) {
	if strings.TrimSpace(requested) == "" {
		dir := env.SpillDir()
		if dir == "" {
			dir = filepath.Join(os.TempDir(), "wombat-screenshots")
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", "", err
		}
		path = filepath.Join(dir, fmt.Sprintf("browser-%d.png", time.Now().UnixNano()))
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return "", "", err
		}
		return path, path, nil
	}
	if err := env.CheckPath("write", requested); err != nil {
		return "", "", err
	}
	path, err = env.ResolvePath(requested)
	if err != nil {
		return "", "", err
	}
	if filepath.Ext(path) == "" {
		path += ".png"
	}
	if err := env.Snapshot("before screenshot", requested); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", "", err
	}
	rel, err = filepath.Rel(env.Dir, path)
	if err != nil {
		rel = path
	}
	return path, rel, nil
}

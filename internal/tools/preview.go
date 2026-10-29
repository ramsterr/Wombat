package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Preview serves a local site from the workspace. It exists because starting
// a static server by hand repeatedly picks the wrong working directory and
// burns several model turns.
type Preview struct{}

type previewArgs struct {
	Path string `json:"path"`
	Port int    `json:"port"`
}

type previewHost struct {
	mu       sync.Mutex
	server   *http.Server
	listener net.Listener
	root     string
	url      string
}

func (t *Preview) Name() string  { return "preview" }
func (t *Preview) Class() string { return "bash" }
func (t *Preview) Description() string {
	return "Serve a local static preview from the workspace and return its URL. Use this instead of starting python -m http.server or another dev server by hand: it resolves the path from the workspace, starts on a free local port, and reuses the running server. The URL stays valid for the session; open it with browser or pass it to verify_site."
}

func (t *Preview) Schema() map[string]any {
	return obj(map[string]any{
		"path": desc("optional file or directory to serve (default: the workspace, preferring index.html)"),
		"port": map[string]any{"type": "integer", "description": "optional local port; default picks a free port"},
	})
}

func (t *Preview) Run(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
	var a previewArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if env == nil || env.Dir == "" {
		return "", fmt.Errorf("preview needs a working directory")
	}
	target := strings.TrimSpace(a.Path)
	if target == "" {
		target = env.Dir
	}
	target, err := env.ResolvePath(target)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	root, rel := target, ""
	if !info.IsDir() {
		root = filepath.Dir(target)
		rel = filepath.Base(target)
	}
	if root, err = filepath.Abs(root); err != nil {
		return "", err
	}
	if err := env.CheckPath("read", root); err != nil {
		return "", err
	}
	host := env.PreviewHost()
	if host == nil {
		return "", fmt.Errorf("preview is unavailable")
	}
	base, err := host.serve(root, a.Port)
	if err != nil {
		return "", err
	}
	if rel != "" {
		base = strings.TrimRight(base, "/") + "/" + filepath.ToSlash(rel)
	}
	return "preview ready: " + base + "\nServing " + root + ". Leave it running; open it with browser or pass the URL to verify_site.", nil
}

func (h *previewHost) serve(root string, port int) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.root == root && h.url != "" && h.listener != nil {
		return h.url, nil
	}
	h.closeLocked()
	addr := "127.0.0.1:0"
	if port > 0 {
		addr = fmt.Sprintf("127.0.0.1:%d", port)
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	server := &http.Server{Handler: http.FileServer(http.Dir(root))}
	go func() { _ = server.Serve(listener) }()
	h.server = server
	h.listener = listener
	h.root = root
	h.url = "http://" + listener.Addr().String() + "/"
	return h.url, nil
}

func (h *previewHost) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closeLocked()
}

func (h *previewHost) closeLocked() {
	if h.server != nil {
		_ = h.server.Close()
	}
	if h.listener != nil {
		_ = h.listener.Close()
	}
	h.server = nil
	h.listener = nil
	h.root = ""
	h.url = ""
}

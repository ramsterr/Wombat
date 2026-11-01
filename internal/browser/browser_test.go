package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrowserInteractionAndScreenshot(t *testing.T) {
	if _, ok := FindExecutable(); !ok {
		t.Skip("no Chromium browser installed")
	}
	page := `<!doctype html>
<html><head><title>Crab UI</title></head>
<body>
<button id="go" onclick="document.getElementById('out').textContent='clicked'">Go</button>
<input id="name" value="">
<div id="out">waiting</div>
<div id="late"></div>
<script>setTimeout(function(){document.getElementById('late').textContent='ready'}, 100)</script>
</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	manager := NewManager(t.TempDir())
	defer manager.Close()
	session, err := manager.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	info, err := session.Navigate(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "Crab UI" || !strings.HasPrefix(info.URL, server.URL) {
		t.Fatalf("page = %+v", info)
	}
	if err := session.Wait(ctx, "#late", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := session.Click(ctx, "#go"); err != nil {
		t.Fatal(err)
	}
	text, err := session.Text(ctx, "#out")
	if err != nil || text != "clicked" {
		t.Fatalf("text = %q, err = %v", text, err)
	}
	value, err := session.Type(ctx, "#name", "Wombat", true)
	if err != nil || value != "Wombat" {
		t.Fatalf("value = %q, err = %v", value, err)
	}
	raw, err := session.Eval(ctx, "document.querySelector('#name').value")
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := json.Unmarshal(raw, &got); err != nil || got != "Wombat" {
		t.Fatalf("eval = %s, %v", raw, err)
	}
	if err := session.Resize(ctx, 390, 844); err != nil {
		t.Fatal(err)
	}
	width, err := session.Eval(ctx, "window.innerWidth")
	if err != nil {
		t.Fatal(err)
	}
	if string(width) != "390" {
		t.Fatalf("width = %s", width)
	}
	shot, err := session.Screenshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(shot, []byte("\x89PNG\r\n\x1a\n")) || len(shot) < 100 {
		t.Fatalf("screenshot = %d bytes", len(shot))
	}
	full, err := session.Screenshot(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(full, []byte("\x89PNG\r\n\x1a\n")) || len(full) < 100 {
		t.Fatalf("full screenshot = %d bytes", len(full))
	}
}

func TestPageTargetRetriesTransientHTTPErrors(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/list" {
			http.NotFound(w, r)
			return
		}
		if requests.Add(1) == 1 {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[{"type":"page","webSocketDebuggerUrl":"ws://127.0.0.1:1/devtools"}]`)
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	target, err := pageTarget(context.Background(), port)
	if err != nil {
		t.Fatal(err)
	}
	if target != "ws://127.0.0.1:1/devtools" {
		t.Fatalf("target = %q", target)
	}
}

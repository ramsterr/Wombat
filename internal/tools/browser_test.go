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
		Dir:    dir,
		Permit: func(string, string) Decision { return Decision{Allow: true} },
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
	if !strings.Contains(shot, "screenshot shots/home.png") {
		t.Fatalf("screenshot = %q", shot)
	}
	data, err := os.ReadFile(filepath.Join(dir, "shots", "home.png"))
	if err != nil || !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("screenshot file = %d bytes, %v", len(data), err)
	}
	if len(media) != 1 || media[0].MIME != "image/png" {
		t.Fatalf("media = %+v", media)
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

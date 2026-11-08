package serve

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestMain doubles as a fake `wombat bridge`. The server spawns this test
// binary with WOMBAT_SERVE_TEST_BRIDGE=1, and the child speaks the same
// newline-delimited JSON protocol as the real bridge without needing a
// provider or API key.
func TestMain(m *testing.M) {
	if os.Getenv("WOMBAT_SERVE_TEST_BRIDGE") == "1" {
		runFakeBridge()
		return
	}
	os.Exit(m.Run())
}

func runFakeBridge() {
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(map[string]any{"type": "ready", "args": os.Args[1:]})
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var req map[string]any
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			continue
		}
		switch req["type"] {
		case "shutdown", "quit":
			os.Exit(0)
		}
		_ = enc.Encode(map[string]any{"type": "echo", "raw": sc.Text()})
	}
	os.Exit(0)
}

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	t.Setenv("WOMBAT_SERVE_TEST_BRIDGE", "1")
	srv, err := New(Options{Token: "secret-token", Dir: t.TempDir(), Exe: os.Args[0]})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func wsURL(ts *httptest.Server, query string) string {
	return "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?" + query
}

func dialWS(t *testing.T, ts *httptest.Server, query string, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	return websocket.DefaultDialer.Dial(wsURL(ts, query), header)
}

func readEvent(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read event: %v", err)
	}
	return msg
}

func TestWebSocketRejectsMissingOrWrongToken(t *testing.T) {
	_, ts := newTestServer(t)

	cases := []struct{ name, query string }{
		{"no token", ""},
		{"wrong token", "token=nope"},
		{"token prefix", "token=secret"},
		{"token with suffix", "token=secret-token-x"},
	}
	for _, tc := range cases {
		_, resp, err := dialWS(t, ts, tc.query, nil)
		if err == nil {
			t.Fatalf("%s: dial succeeded, want rejection", tc.name)
		}
		if resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: status = %v, want 401", tc.name, resp)
		}
	}
}

func TestBearerHeaderAccepted(t *testing.T) {
	_, ts := newTestServer(t)
	hdr := http.Header{"Authorization": []string{"Bearer secret-token"}}
	conn, _, err := dialWS(t, ts, "", hdr)
	if err != nil {
		t.Fatalf("dial with bearer header: %v", err)
	}
	defer conn.Close()
	if got := readEvent(t, conn)["type"]; got != "ready" {
		t.Fatalf("first event = %v, want ready", got)
	}
}

func TestRelayRoundTrip(t *testing.T) {
	_, ts := newTestServer(t)
	conn, _, err := dialWS(t, ts, "token=secret-token", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if got := readEvent(t, conn)["type"]; got != "ready" {
		t.Fatalf("first event = %v, want ready", got)
	}
	if err := conn.WriteJSON(map[string]any{"type": "submit", "text": "hello phone"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	echo := readEvent(t, conn)
	if echo["type"] != "echo" {
		t.Fatalf("event = %v, want echo", echo)
	}
	raw, _ := echo["raw"].(string)
	if !strings.Contains(raw, `"text":"hello phone"`) || !strings.Contains(raw, `"type":"submit"`) {
		t.Fatalf("child received %q", raw)
	}
}

func TestSessionIDPassedAsFlag(t *testing.T) {
	_, ts := newTestServer(t)
	conn, _, err := dialWS(t, ts, "token=secret-token&session=abc-123_X", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	ready := readEvent(t, conn)
	args, _ := ready["args"].([]any)
	if len(args) != 3 || args[0] != "bridge" || args[1] != "--session" || args[2] != "abc-123_X" {
		t.Fatalf("bridge args = %v, want [bridge --session abc-123_X]", args)
	}
}

func TestSessionIDValidated(t *testing.T) {
	_, ts := newTestServer(t)
	for _, bad := range []string{"..%2F..%2Fetc", "a.b", "x%20y", strings.Repeat("a", 81)} {
		_, resp, err := dialWS(t, ts, "token=secret-token&session="+bad, nil)
		if err == nil {
			t.Fatalf("session %q accepted, want rejection", bad)
		}
		if resp == nil || resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("session %q: status = %v, want 400", bad, resp)
		}
	}
}

func TestCrossOriginWebSocketRejected(t *testing.T) {
	_, ts := newTestServer(t)
	hdr := http.Header{"Origin": []string{"https://evil.example"}}
	_, resp, err := dialWS(t, ts, "token=secret-token", hdr)
	if err == nil {
		t.Fatal("cross-origin dial succeeded, want rejection")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v, want 403", resp)
	}
}

func TestSameOriginWebSocketAccepted(t *testing.T) {
	_, ts := newTestServer(t)
	hdr := http.Header{"Origin": []string{ts.URL}}
	conn, _, err := dialWS(t, ts, "token=secret-token", hdr)
	if err != nil {
		t.Fatalf("same-origin dial: %v", err)
	}
	defer conn.Close()
	readEvent(t, conn)
}

func TestBridgeExitClosesSocket(t *testing.T) {
	_, ts := newTestServer(t)
	conn, _, err := dialWS(t, ts, "token=secret-token", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	readEvent(t, conn)
	if err := conn.WriteJSON(map[string]any{"type": "quit"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return // socket closed after the child exited, as intended
		}
	}
}

func TestInvalidClientMessageGetsToast(t *testing.T) {
	_, ts := newTestServer(t)
	conn, _, err := dialWS(t, ts, "token=secret-token", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	readEvent(t, conn)
	if err := conn.WriteMessage(websocket.TextMessage, []byte("not json")); err != nil {
		t.Fatalf("write: %v", err)
	}
	toast := readEvent(t, conn)
	if toast["type"] != "toast" || toast["error"] != true {
		t.Fatalf("event = %v, want error toast", toast)
	}
}

func TestNormalizeRequest(t *testing.T) {
	if _, err := normalizeRequest([]byte("{nope")); err == nil {
		t.Error("invalid JSON accepted")
	}
	if _, err := normalizeRequest([]byte(`{"text":"no type"}`)); err == nil {
		t.Error("message without type accepted")
	}
	out, err := normalizeRequest([]byte("{\n  \"type\": \"submit\",\n  \"text\": \"line one\\nline two\"\n}"))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if strings.ContainsAny(string(out), "\n\r") {
		t.Fatalf("normalized request contains a raw newline: %q", out)
	}
	if string(out) != `{"type":"submit","text":"line one\nline two"}` {
		t.Fatalf("normalized = %s", out)
	}
}

func TestNormalizeDefaultsToLoopback(t *testing.T) {
	var opts Options
	if err := opts.Normalize(); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if opts.Host != DefaultHost {
		t.Errorf("host = %q, want %q", opts.Host, DefaultHost)
	}
	if opts.Port != DefaultPort {
		t.Errorf("port = %d, want %d", opts.Port, DefaultPort)
	}
	if len(opts.Token) != 64 {
		t.Errorf("generated token length = %d, want 64 hex chars", len(opts.Token))
	}
	if opts.Dir == "" || opts.Exe == "" {
		t.Errorf("dir/exe defaults missing: %+v", opts)
	}
}

func TestBannerOnlyExposesTokenInLANMode(t *testing.T) {
	local := Options{Host: DefaultHost, Port: 8787, Token: "tok", Dir: "/tmp/p"}
	for _, line := range Banner(local) {
		if strings.Contains(line, "phone:") {
			t.Fatalf("local banner advertises a phone URL: %q", line)
		}
	}
	lan := Options{Host: LANHost, Port: 8787, Token: "tok", Dir: "/tmp/p"}
	joined := strings.Join(Banner(lan), "\n")
	if !strings.Contains(joined, "plain HTTP") {
		t.Fatalf("LAN banner does not warn about plain HTTP:\n%s", joined)
	}
}

func TestNewTokenIsRandom(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewToken()
	if len(a) != 64 || a == b {
		t.Fatalf("tokens not random hex: %q %q", a, b)
	}
}

func TestStaticUIServedWithSecurityHeaders(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("CSP header = %q", csp)
	}
	if resp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q", resp.Header.Get("Referrer-Policy"))
	}
}

// Package serve exposes the wombat bridge to a phone browser.
//
// Each WebSocket connection spawns one `wombat bridge` child process and
// relays its newline-delimited JSON protocol in both directions. The agent
// logic stays in the bridge; this package only authenticates, validates, and
// forwards lines.
package serve

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// DefaultHost keeps the server reachable only from this machine.
	DefaultHost = "127.0.0.1"
	// LANHost listens on every interface so a phone on the same network can connect.
	LANHost     = "0.0.0.0"
	DefaultPort = 8787

	maxMessageBytes = 4 << 20
	writeWait       = 10 * time.Second
	pongWait        = 60 * time.Second
	pingEvery       = 25 * time.Second
	shutdownGrace   = 3 * time.Second
)

// sessionIDPattern guards the session ID passed to `bridge --session`.
// session.Open joins the ID into a file path, so anything with separators
// or dots must be rejected.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// Options configures the server. Zero values are replaced by Normalize.
type Options struct {
	Host    string // listen address; default 127.0.0.1
	Port    int    // listen port; default 8787
	Token   string // access token; generated when empty
	Dir     string // working directory for the agent; default current directory
	Exe     string // wombat executable spawned as `bridge`; default this executable
	LogFile string // bridge stderr destination; empty discards it
}

// Normalize fills defaults. It returns an error only when the current
// directory or executable cannot be determined.
func (o *Options) Normalize() error {
	if o.Host == "" {
		o.Host = DefaultHost
	}
	if o.Port == 0 {
		o.Port = DefaultPort
	}
	if o.Token == "" {
		tok, err := NewToken()
		if err != nil {
			return err
		}
		o.Token = tok
	}
	if o.Dir == "" {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		o.Dir = dir
	}
	if o.Exe == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		o.Exe = exe
	}
	return nil
}

// Addr is the host:port the server listens on.
func (o Options) Addr() string {
	return net.JoinHostPort(o.Host, strconv.Itoa(o.Port))
}

// NewToken returns 32 random bytes encoded as hex.
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// LANAddresses lists the non-loopback IPv4 addresses a phone could reach.
func LANAddresses() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if ip4 := ipnet.IP.To4(); ip4 != nil {
			out = append(out, ip4.String())
		}
	}
	return out
}

// Server serves the phone UI and the WebSocket relay.
type Server struct {
	opts     Options
	upgrader websocket.Upgrader
}

// New validates and normalizes the options and returns a server.
func New(opts Options) (*Server, error) {
	if err := opts.Normalize(); err != nil {
		return nil, err
	}
	s := &Server{opts: opts}
	s.upgrader = websocket.Upgrader{
		ReadBufferSize:  32 << 10,
		WriteBufferSize: 32 << 10,
		CheckOrigin:     sameOrigin,
	}
	return s, nil
}

// Options returns the normalized options in use.
func (s *Server) Options() Options { return s.opts }

// Handler returns the HTTP handler: the static UI at / and the relay at /ws.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.Handle("/", securityHeaders(staticHandler()))
	return mux
}

// ListenAndServe blocks until the context is cancelled or the listener fails.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.opts.Addr())
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	err = srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	sessionID := r.URL.Query().Get("session")
	if sessionID != "" && !sessionIDPattern.MatchString(sessionID) {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade has already written the HTTP error response.
		return
	}
	defer conn.Close()
	s.relay(conn, sessionID)
}

// authorized checks the access token in the query string or a bearer header.
// The comparison is constant-time.
func (s *Server) authorized(r *http.Request) bool {
	got := r.URL.Query().Get("token")
	if got == "" {
		got = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.opts.Token)) == 1
}

// sameOrigin allows requests without an Origin header (non-browser clients,
// which still need the token) and browser requests whose Origin host matches
// the Host they connected to. This blocks cross-site WebSocket hijacking.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

// relay runs one bridge child for the lifetime of the connection.
func (s *Server) relay(conn *websocket.Conn, sessionID string) {
	args := []string{"bridge"}
	if sessionID != "" {
		args = append(args, "--session", sessionID)
	}
	cmd := exec.Command(s.opts.Exe, args...)
	cmd.Dir = s.opts.Dir
	cmd.Env = append(os.Environ(), "WOMBAT_UI=1")
	if s.opts.LogFile != "" {
		if f, err := os.OpenFile(s.opts.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			defer f.Close()
			cmd.Stderr = f
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		writeToast(conn, "could not start agent: "+err.Error())
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		writeToast(conn, "could not start agent: "+err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		writeToast(conn, "could not start agent: "+err.Error())
		return
	}

	var writeMu sync.Mutex
	send := func(messageType int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
		return conn.WriteMessage(messageType, data)
	}

	// Every write to the socket goes through send, which holds writeMu.
	// gorilla/websocket allows one concurrent writer per connection.
	toast := func(message string) {
		raw, _ := json.Marshal(map[string]any{"type": "toast", "message": message, "error": true})
		_ = send(websocket.TextMessage, raw)
	}

	quit := make(chan struct{})
	childDone := make(chan struct{})

	// Child to browser. When the child exits, close the socket so the
	// browser sees the end of the session and can reconnect.
	go func() {
		defer close(childDone)
		defer conn.Close()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), maxMessageBytes)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 || !json.Valid(line) {
				continue
			}
			if err := send(websocket.TextMessage, append([]byte(nil), line...)); err != nil {
				return
			}
		}
	}()

	// Keep the connection alive through phone network changes.
	go func() {
		ticker := time.NewTicker(pingEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := send(websocket.PingMessage, nil); err != nil {
					return
				}
			case <-quit:
				return
			}
		}
	}()

	conn.SetReadLimit(maxMessageBytes)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	// Browser to child. Only this goroutine writes to stdin.
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		line, err := normalizeRequest(data)
		if err != nil {
			toast("bad request: " + err.Error())
			continue
		}
		if _, err := stdin.Write(append(line, '\n')); err != nil {
			break
		}
	}

	close(quit)
	_, _ = stdin.Write([]byte(`{"type":"shutdown"}` + "\n"))
	_ = stdin.Close()
	select {
	case <-childDone:
	case <-time.After(shutdownGrace):
		_ = cmd.Process.Kill()
		select {
		case <-childDone:
		case <-time.After(shutdownGrace):
		}
	}
	_ = cmd.Wait()
}

// normalizeRequest validates a browser message and compacts it onto one line
// so it cannot break the bridge's newline framing.
func normalizeRequest(data []byte) ([]byte, error) {
	if !json.Valid(data) {
		return nil, errors.New("not JSON")
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	if probe.Type == "" {
		return nil, errors.New("missing type")
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeToast(conn *websocket.Conn, message string) {
	raw, _ := json.Marshal(map[string]any{"type": "toast", "message": message, "error": true})
	_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
	_ = conn.WriteMessage(websocket.TextMessage, raw)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self' ws: wss:; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// Banner returns the lines printed when the server starts. The token appears
// in the URL fragment, which browsers never send to the server.
func Banner(opts Options) []string {
	lines := []string{fmt.Sprintf("wombat serve: listening on http://%s", opts.Addr())}
	if opts.Host == DefaultHost {
		lines = append(lines, fmt.Sprintf("this machine: http://%s/#token=%s", opts.Addr(), opts.Token))
		lines = append(lines, "use --lan to reach it from your phone")
	} else {
		for _, ip := range LANAddresses() {
			lines = append(lines, fmt.Sprintf("phone: http://%s:%d/#token=%s", ip, opts.Port, opts.Token))
		}
		lines = append(lines, "traffic is plain HTTP: use Tailscale or another encrypted network outside your home Wi-Fi")
	}
	lines = append(lines, "project: "+filepath.Clean(opts.Dir))
	return lines
}

// Package mcp connects Wombat to Model Context Protocol servers and turns
// their tools into ordinary Wombat tools.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ramsterr/Wombat/internal/config"
)

// Server is one configured MCP server and its live session.
type Server struct {
	ID     string
	Config *config.MCPServer

	session  *sdk.ClientSession
	tools    []*sdk.Tool
	stderr   *tailBuffer
	err      error
	calls    atomic.Int64
	failures atomic.Int64
}

// Name returns the configured server name.
func (s *Server) Name() string {
	if s == nil {
		return ""
	}
	return s.ID
}

// Manager owns every MCP server started for a session.
type Manager struct {
	servers []*Server
	once    sync.Once
}

// Connect starts all enabled servers in parallel using cached OAuth tokens
// only. It never opens a browser.
func Connect(ctx context.Context, servers map[string]*config.MCPServer, cwd, version string) *Manager {
	return ConnectWithOptions(ctx, servers, cwd, version, Options{})
}

// ConnectWithOptions starts every enabled server in parallel. A broken server
// is recorded on its Server rather than failing the whole session.
func ConnectWithOptions(ctx context.Context, servers map[string]*config.MCPServer, cwd, version string, opts Options) *Manager {
	m := &Manager{}
	if len(servers) == 0 {
		return m
	}
	names := make([]string, 0, len(servers))
	for name, cfg := range servers {
		if cfg != nil && cfg.EffectiveEnabled() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return m
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}
	m.servers = make([]*Server, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		i, name := i, name
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.servers[i] = connectOne(ctx, name, servers[name], cwd, version, opts)
		}()
	}
	wg.Wait()
	return m
}

// Servers returns every configured server, including failed ones.
func (m *Manager) Servers() []*Server {
	if m == nil {
		return nil
	}
	return m.servers
}

// Close shuts down every live session.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.once.Do(func() {
		var wg sync.WaitGroup
		for _, server := range m.servers {
			if server == nil || server.session == nil {
				continue
			}
			wg.Add(1)
			go func(s *Server) {
				defer wg.Done()
				_ = s.session.Close()
			}(server)
		}
		wg.Wait()
	})
}

// Tools returns the server's tools, filtered by the configured allow and
// exclude lists.
func (s *Server) Tools() []*sdk.Tool {
	if s == nil {
		return nil
	}
	return s.tools
}

// Err reports why this server failed to start, if it did.
func (s *Server) Err() error {
	if s == nil {
		return nil
	}
	return s.err
}

// Stderr returns the last diagnostic output from a local server process.
func (s *Server) Stderr() string {
	if s == nil || s.stderr == nil {
		return ""
	}
	return strings.TrimSpace(s.stderr.String())
}

// CallTool invokes one tool on this server.
func (s *Server) CallTool(ctx context.Context, name string, args any) (*sdk.CallToolResult, error) {
	if s == nil || s.session == nil {
		return nil, fmt.Errorf("mcp server %q is not connected", s.Name())
	}
	s.calls.Add(1)
	res, err := s.session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		s.failures.Add(1)
	} else if res != nil && res.IsError {
		s.failures.Add(1)
	}
	return res, err
}

// Calls returns how many tool calls this session sent to the server.
func (s *Server) Calls() int64 {
	if s == nil {
		return 0
	}
	return s.calls.Load()
}

// Failures returns how many calls failed or returned a tool error.
func (s *Server) Failures() int64 {
	if s == nil {
		return 0
	}
	return s.failures.Load()
}

func connectOne(parent context.Context, name string, cfg *config.MCPServer, cwd, version string, opts Options) *Server {
	server := &Server{ID: name, Config: cfg, stderr: &tailBuffer{max: 8 << 10}}
	if cfg == nil {
		server.err = errors.New("missing configuration")
		return server
	}
	timeout := cfg.Timeout()
	if oauthEnabled(cfg) {
		if opts.InteractiveAuth {
			timeout = oauthLoginTimeout + 30*time.Second
		} else if HasAuth(name, configuredEndpoint(cfg)) && timeout < 60*time.Second {
			timeout = 60 * time.Second
		}
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	transport, cleanup, err := buildTransport(name, cfg, cwd, server.stderr, version, opts)
	if err != nil {
		server.err = err
		return server
	}
	if cleanup != nil {
		defer cleanup()
	}
	server = connectWithTransport(ctx, server, transport, version)
	if server.err != nil && oauthEnabled(cfg) {
		server.err = redactOAuthError(server.err)
		if !opts.InteractiveAuth && looksLikeAuthFailure(server.err) {
			server.err = fmt.Errorf("%w — run `wombat mcp login %s`", server.err, name)
		}
	}
	return server
}

func looksLikeAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{"401", "403", "unauthorized", "forbidden"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func connectWithTransport(ctx context.Context, server *Server, transport sdk.Transport, version string) *Server {
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}
	client := sdk.NewClient(&sdk.Implementation{
		Name:    "wombat",
		Title:   "Wombat",
		Version: version,
	}, &sdk.ClientOptions{
		// Wombat does not answer roots, sampling, or elicitation requests.
		// Advertising an empty capability set keeps servers from asking.
		Capabilities: &sdk.ClientCapabilities{},
	})
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		server.err = withStderr(err, server.stderr)
		return server
	}
	server.session = session
	tools, err := listAllTools(ctx, session)
	if err != nil {
		server.err = withStderr(err, server.stderr)
		_ = session.Close()
		server.session = nil
		return server
	}
	for _, tool := range tools {
		if toolAllowed(server.Config, tool.Name) {
			server.tools = append(server.tools, tool)
		}
	}
	return server
}

func listAllTools(ctx context.Context, session *sdk.ClientSession) ([]*sdk.Tool, error) {
	var tools []*sdk.Tool
	cursor := ""
	for i := 0; i < 100; i++ {
		page, err := session.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		tools = append(tools, page.Tools...)
		if page.NextCursor == "" || page.NextCursor == cursor {
			return tools, nil
		}
		cursor = page.NextCursor
	}
	return tools, nil
}

func buildTransport(serverName string, cfg *config.MCPServer, cwd string, stderr *tailBuffer, version string, opts Options) (sdk.Transport, func(), error) {
	switch cfg.Transport() {
	case "stdio":
		if strings.TrimSpace(cfg.Command) == "" {
			return nil, nil, errors.New("stdio server has no command")
		}
		command := expandEnv(cfg.Command)
		args := make([]string, 0, len(cfg.Args))
		for _, arg := range cfg.Args {
			args = append(args, expandEnv(arg))
		}
		dir := cwd
		if cfg.Cwd != "" {
			dir = expandEnv(cfg.Cwd)
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(cwd, dir)
			}
		}
		cmd := exec.Command(command, args...)
		cmd.Dir = dir
		cmd.Env = mergedEnv(cfg.Env)
		cmd.Stderr = stderr
		return &sdk.CommandTransport{Command: cmd}, nil, nil
	case "http", "sse":
		endpoint := expandEnv(cfg.URL)
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, nil, fmt.Errorf("invalid MCP url %q", cfg.URL)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, nil, fmt.Errorf("MCP url must be http or https: %q", cfg.URL)
		}
		if u.Scheme == "http" && !isLoopbackIP(u.Hostname()) {
			return nil, nil, fmt.Errorf("remote MCP url must use https (loopback may use http): %q", cfg.URL)
		}
		client := &http.Client{
			Transport: &headerTransport{
				base:    newHTTPTransport(),
				headers: expandMap(cfg.Headers),
				host:    u.Host,
			},
			CheckRedirect: safeRedirect,
		}
		if cfg.Transport() == "sse" {
			if cfg.OAuth != nil && *cfg.OAuth {
				return nil, nil, errors.New("OAuth is only supported for streamable HTTP servers")
			}
			return &sdk.SSEClientTransport{Endpoint: endpoint, HTTPClient: client}, nil, nil
		}
		transport := &sdk.StreamableClientTransport{
			Endpoint:   endpoint,
			HTTPClient: client,
			MaxRetries: 2,
		}
		if oauthEnabled(cfg) && hasAuthorizationHeader(cfg.Headers) {
			return nil, nil, errors.New("configure either OAuth or an Authorization header, not both")
		}
		var cleanup func()
		if oauthEnabled(cfg) {
			if opts.InteractiveAuth {
				handler, closeHandler, err := newInteractiveOAuthHandler(serverName, cfg, canonicalEndpoint(endpoint), version, opts)
				if err != nil {
					return nil, nil, err
				}
				transport.OAuthHandler = handler
				cleanup = closeHandler
			} else {
				handler, err := newStoredOAuthHandler(serverName, canonicalEndpoint(endpoint))
				if err != nil {
					return nil, nil, err
				}
				transport.OAuthHandler = handler
			}
		}
		return transport, cleanup, nil
	}
	return nil, nil, fmt.Errorf("unsupported MCP transport %q", cfg.Type)
}

func hasAuthorizationHeader(headers map[string]string) bool {
	for key := range headers {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "authorization", "proxy-authorization":
			return true
		}
	}
	return false
}

func toolAllowed(cfg *config.MCPServer, name string) bool {
	if cfg == nil {
		return true
	}
	matches := func(pattern string) bool {
		if pattern == "*" {
			return true
		}
		if ok, _ := path.Match(pattern, name); ok {
			return true
		}
		return pattern == name
	}
	for _, pattern := range cfg.ExcludeTools {
		if matches(pattern) {
			return false
		}
	}
	if len(cfg.AllowTools) == 0 {
		return true
	}
	for _, pattern := range cfg.AllowTools {
		if matches(pattern) {
			return true
		}
	}
	return false
}

func mergedEnv(overrides map[string]string) []string {
	values := map[string]string{}
	for _, entry := range os.Environ() {
		if i := strings.IndexByte(entry, '='); i > 0 {
			values[entry[:i]] = entry[i+1:]
		}
	}
	for key, value := range overrides {
		values[key] = expandEnv(value)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}

func expandMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = expandEnv(value)
	}
	return out
}

func withStderr(err error, stderr *tailBuffer) error {
	if err == nil {
		return nil
	}
	text := strings.TrimSpace(stderr.String())
	if text == "" {
		return err
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	return fmt.Errorf("%w (server stderr: %s)", err, strings.Join(lines, " | "))
}

type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
	host    string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	if t.host == "" || strings.EqualFold(req.URL.Host, t.host) {
		for key, value := range t.headers {
			clone.Header.Set(key, value)
		}
	}
	return t.base.RoundTrip(clone)
}

type tailBuffer struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.max {
		b.data = b.data[len(b.data)-b.max:]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

func expandEnv(value string) string {
	value = expandEnvPattern(value, "${", "}")
	return expandEnvPattern(value, "{env:", "}")
}

func expandEnvPattern(value, prefix, suffix string) string {
	var b strings.Builder
	for {
		start := strings.Index(value, prefix)
		if start < 0 {
			b.WriteString(value)
			return b.String()
		}
		b.WriteString(value[:start])
		rest := value[start+len(prefix):]
		end := strings.Index(rest, suffix)
		if end < 0 {
			b.WriteString(value[start:])
			return b.String()
		}
		whole := value[start : start+len(prefix)+end+len(suffix)]
		ref := rest[:end]
		name, fallback, hasFallback := strings.Cut(ref, ":-")
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, " \t\n") {
			b.WriteString(whole)
		} else if replacement, ok := os.LookupEnv(name); ok {
			b.WriteString(replacement)
		} else if hasFallback {
			b.WriteString(fallback)
		} else {
			b.WriteString(whole)
		}
		value = rest[end+len(suffix):]
	}
}

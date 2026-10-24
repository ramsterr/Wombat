package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	"github.com/ramsterr/Wombat/internal/config"
)

const (
	oauthLoginTimeout = 5 * time.Minute
	oauthCallbackPath = "/callback"
)

var errOAuthLoginRequired = errors.New("this MCP server needs an interactive OAuth login")

var oauthSecretPattern = regexp.MustCompile(`(?i)(access_token|refresh_token|id_token|client_secret)(["']?\s*[:=]\s*["']?)[^"'&\s,}]+`)

// Options controls how a Manager connects to servers.
type Options struct {
	// InteractiveAuth permits opening a browser and waiting for an OAuth
	// callback. It is only enabled by explicit commands such as mcp login.
	InteractiveAuth bool
	// OpenBrowser opens an authorization URL. Tests can replace it.
	OpenBrowser func(string) error
	// Notify receives human-readable login progress. It must not receive
	// tokens or other secrets.
	Notify func(string)
}

// OAuthEnabled reports whether this server should use OAuth. If oauth is not
// set, remote HTTP servers without an Authorization/API-key header use it.
func OAuthEnabled(cfg *config.MCPServer) bool {
	return oauthEnabled(cfg)
}

func oauthEnabled(cfg *config.MCPServer) bool {
	if cfg == nil || cfg.Transport() != "http" {
		return false
	}
	if cfg.OAuth != nil {
		return *cfg.OAuth
	}
	if u, err := url.Parse(expandEnv(cfg.URL)); err == nil {
		for key := range u.Query() {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "token") || strings.Contains(lower, "key") ||
				strings.Contains(lower, "secret") || strings.Contains(lower, "auth") {
				return false
			}
		}
	}
	for key := range cfg.Headers {
		lower := strings.ToLower(strings.TrimSpace(key))
		if lower == "cookie" || strings.Contains(lower, "auth") || strings.Contains(lower, "token") ||
			strings.Contains(lower, "key") || strings.Contains(lower, "secret") {
			return false
		}
	}
	return true
}

// HasAuth reports whether a saved login exists for this server endpoint.
func HasAuth(serverName, endpoint string) bool {
	_, err := loadAuth(serverName, canonicalEndpoint(endpoint))
	return err == nil
}

// Logout deletes the saved OAuth login for a server.
func Logout(serverName string) error {
	err := os.Remove(authPath(serverName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func newInteractiveOAuthHandler(serverName string, cfg *config.MCPServer, endpoint, version string, opts Options) (auth.OAuthHandler, func(), error) {
	callback, err := newCallbackServer(expandEnv(cfg.OAuthRedirectURL), opts.OpenBrowser, opts.Notify)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { callback.Close() }
	record, err := loadAuth(serverName, endpoint)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		cleanup()
		return nil, nil, fmt.Errorf("cached OAuth login for %s: %w", serverName, err)
	}
	handlerConfig := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL:              callback.RedirectURL(),
		Client:                   oauthHTTPClient(version),
		RequestRefreshToken:      true,
		AuthorizationCodeFetcher: callback.Fetch,
		NewTokenSource: func(ctx context.Context, cfg *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
			if err := saveAuth(serverName, endpoint, cfg, token); err != nil {
				return nil, fmt.Errorf("saving OAuth login: %w", err)
			}
			return newSavingTokenSource(cfg.TokenSource(ctx, token), serverName, endpoint, cfg, token), nil
		},
	}
	if len(cfg.OAuthScopes) > 0 {
		scopes := make([]string, 0, len(cfg.OAuthScopes))
		for _, scope := range cfg.OAuthScopes {
			if scope = expandEnv(scope); scope != "" {
				scopes = append(scopes, scope)
			}
		}
		handlerConfig.ScopeFilter = func([]string) []string { return scopes }
	}
	clientID := expandEnv(cfg.OAuthClientID)
	clientSecret := expandEnv(cfg.OAuthClientSecret)
	if clientID != "" {
		credentials := &oauthex.ClientCredentials{ClientID: clientID}
		if clientSecret != "" {
			credentials.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: clientSecret}
		}
		handlerConfig.PreregisteredClient = credentials
	} else {
		handlerConfig.DynamicClientRegistrationConfig = &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				RedirectURIs:            []string{callback.RedirectURL()},
				ClientName:              "Wombat",
				GrantTypes:              []string{"authorization_code", "refresh_token"},
				ResponseTypes:           []string{"code"},
				TokenEndpointAuthMethod: "none",
				Scope:                   strings.Join(cfg.OAuthScopes, " "),
			},
		}
	}
	if record != nil {
		source, err := recordTokenSource(record, serverName, endpoint)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		handlerConfig.InitialTokenSource = source
	}
	handler, err := auth.NewAuthorizationCodeHandler(handlerConfig)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return handler, cleanup, nil
}

func newStoredOAuthHandler(serverName, endpoint string) (auth.OAuthHandler, error) {
	record, err := loadAuth(serverName, endpoint)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cached OAuth login for %s: %w — run `wombat mcp logout %s` and log in again", serverName, err, serverName)
	}
	source, err := recordTokenSource(record, serverName, endpoint)
	if err != nil {
		return nil, err
	}
	return &storedOAuthHandler{source: source, serverName: serverName}, nil
}

type storedOAuthHandler struct {
	source     oauth2.TokenSource
	serverName string
}

func (h *storedOAuthHandler) TokenSource(context.Context) (oauth2.TokenSource, error) {
	return h.source, nil
}

func (h *storedOAuthHandler) Authorize(_ context.Context, _ *http.Request, resp *http.Response) error {
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
	}
	return fmt.Errorf("%w: run `wombat mcp login %s`", errOAuthLoginRequired, h.serverName)
}

type callbackServer struct {
	redirect string
	path     string
	server   *http.Server
	listener net.Listener
	open     func(string) error
	notify   func(string)
	results  chan callbackResult
	once     sync.Once
	mu       sync.Mutex
	used     bool
}

type callbackResult struct {
	code  string
	state string
	iss   string
	err   error
}

func newCallbackServer(configured string, open func(string) error, notify func(string)) (*callbackServer, error) {
	host, port, path := "127.0.0.1", "0", oauthCallbackPath
	if configured != "" {
		u, err := url.Parse(configured)
		if err != nil || u.Scheme != "http" || !isLoopbackIP(u.Hostname()) {
			return nil, fmt.Errorf("oauth_redirect_url must be an http loopback URL such as http://127.0.0.1:19876/callback")
		}
		if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path == "" {
			return nil, fmt.Errorf("oauth_redirect_url must not contain credentials, a query, or a fragment")
		}
		host = u.Hostname()
		if u.Port() == "" {
			return nil, fmt.Errorf("oauth_redirect_url must include a port")
		}
		port = u.Port()
		path = u.Path
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, fmt.Errorf("cannot start OAuth callback listener: %w", err)
	}
	redirect := (&url.URL{Scheme: "http", Host: listener.Addr().String(), Path: path}).String()
	cs := &callbackServer{
		redirect: redirect,
		path:     path,
		listener: listener,
		open:     open,
		notify:   notify,
		results:  make(chan callbackResult, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc(path, cs.handle)
	cs.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       15 * time.Second,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	go func() { _ = cs.server.Serve(listener) }()
	return cs, nil
}

func (cs *callbackServer) RedirectURL() string { return cs.redirect }

func (cs *callbackServer) Close() {
	cs.once.Do(func() {
		if cs.server != nil {
			_ = cs.server.Close()
		}
		if cs.listener != nil {
			_ = cs.listener.Close()
		}
	})
}

func (cs *callbackServer) Fetch(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
	if args == nil || args.URL == "" {
		return nil, errors.New("authorization server returned no authorization URL")
	}
	parsed, err := url.Parse(args.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid authorization URL: %w", err)
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackIP(parsed.Hostname())) {
		return nil, errors.New("authorization URL must use https (or http on loopback)")
	}
	state := parsed.Query().Get("state")
	if state == "" || len(state) > 4096 {
		return nil, errors.New("authorization URL has no valid state")
	}
	cs.mu.Lock()
	if cs.used {
		cs.mu.Unlock()
		return nil, errors.New("OAuth callback was already used")
	}
	cs.used = true
	cs.mu.Unlock()

	if cs.notify != nil {
		cs.notify("Open this URL to authorize:\n" + args.URL)
	}
	open := cs.open
	if open == nil {
		open = openDefaultBrowser
	}
	if err := open(args.URL); err != nil && cs.notify != nil {
		cs.notify("Could not open the browser automatically: " + err.Error() + "\nOpen the URL above manually.")
	}

	timer := time.NewTimer(oauthLoginTimeout)
	defer timer.Stop()
	select {
	case result := <-cs.results:
		if result.err != nil {
			return nil, result.err
		}
		if result.state != state {
			return nil, errors.New("OAuth state mismatch; login was rejected")
		}
		return &auth.AuthorizationResult{Code: result.code, State: result.state, Iss: result.iss}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("OAuth login timed out after %s", oauthLoginTimeout)
	}
}

func (cs *callbackServer) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	if text := query.Get("error"); text != "" {
		detail := query.Get("error_description")
		if detail == "" {
			detail = text
		}
		cs.deliver(callbackResult{err: fmt.Errorf("OAuth authorization failed: %s", safeOAuthText(detail))})
		http.Error(w, "Authorization failed. You can close this tab.", http.StatusBadRequest)
		return
	}
	code := query.Get("code")
	state := query.Get("state")
	if code == "" || state == "" {
		cs.deliver(callbackResult{err: errors.New("OAuth callback had no code or state")})
		http.Error(w, "Authorization response was incomplete. You can close this tab.", http.StatusBadRequest)
		return
	}
	cs.deliver(callbackResult{code: code, state: state, iss: query.Get("iss")})
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "Authorization complete. You can close this tab and return to Wombat.\n")
}

func (cs *callbackServer) deliver(result callbackResult) {
	select {
	case cs.results <- result:
	default:
	}
}

func openDefaultBrowser(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackIP(u.Hostname()))) {
		return errors.New("refusing to open an unsafe authorization URL")
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", raw)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", raw)
	default:
		command = exec.Command("xdg-open", raw)
	}
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

func isLoopbackIP(host string) bool {
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func safeOAuthText(text string) string {
	text = oauthSecretPattern.ReplaceAllString(text, `${1}${2}[redacted]`)
	text = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, text)
	text = strings.TrimSpace(text)
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	return text
}

func oauthHTTPClient(version string) *http.Client {
	base := newHTTPTransport()
	userAgent := "wombat"
	if version != "" {
		userAgent += "/" + version
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &headerTransport{
			base:    base,
			headers: map[string]string{"User-Agent": userAgent},
		},
		CheckRedirect: safeRedirect,
	}
}

func newHTTPTransport() *http.Transport {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	base.TLSHandshakeTimeout = 10 * time.Second
	base.ExpectContinueTimeout = time.Second
	base.MaxIdleConns = 32
	base.MaxIdleConnsPerHost = 8
	return base
}

func safeRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("too many redirects")
	}
	if len(via) > 0 && !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
		return errors.New("refusing cross-host redirect")
	}
	if req.URL.Scheme != "https" && !(req.URL.Scheme == "http" && isLoopbackIP(req.URL.Hostname())) {
		return errors.New("refusing insecure redirect")
	}
	return nil
}

// authRecord is the on-disk OAuth session. It contains secrets and must never
// be logged.
type authRecord struct {
	Version  int              `json:"version"`
	Endpoint string           `json:"endpoint"`
	Config   authConfigRecord `json:"config"`
	Token    authTokenRecord  `json:"token"`
}

type authConfigRecord struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret,omitempty"`
	AuthURL      string   `json:"auth_url"`
	TokenURL     string   `json:"token_url"`
	AuthStyle    int      `json:"auth_style,omitempty"`
	RedirectURL  string   `json:"redirect_url"`
	Scopes       []string `json:"scopes,omitempty"`
}

type authTokenRecord struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
}

var authStoreMu sync.Mutex

func authPath(serverName string) string {
	sum := sha256.Sum256([]byte(serverName))
	safe := sanitizeFilePart(serverName)
	return filepath.Join(config.Home(), "mcp-auth", safe+"-"+hex.EncodeToString(sum[:6])+".json")
}

func sanitizeFilePart(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		out = "server"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

func saveAuth(serverName, endpoint string, oauthCfg *oauth2.Config, token *oauth2.Token) error {
	if oauthCfg == nil || token == nil {
		return errors.New("missing OAuth configuration or token")
	}
	record := authRecord{
		Version:  1,
		Endpoint: endpoint,
		Config: authConfigRecord{
			ClientID:     oauthCfg.ClientID,
			ClientSecret: oauthCfg.ClientSecret,
			AuthURL:      oauthCfg.Endpoint.AuthURL,
			TokenURL:     oauthCfg.Endpoint.TokenURL,
			AuthStyle:    int(oauthCfg.Endpoint.AuthStyle),
			RedirectURL:  oauthCfg.RedirectURL,
			Scopes:       append([]string(nil), oauthCfg.Scopes...),
		},
		Token: authTokenRecord{
			AccessToken:  token.AccessToken,
			TokenType:    token.TokenType,
			RefreshToken: token.RefreshToken,
			Expiry:       token.Expiry,
		},
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	path := authPath(serverName)
	dir := filepath.Dir(path)
	authStoreMu.Lock()
	defer authStoreMu.Unlock()
	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to write OAuth tokens through a symlinked directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".auth-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(path)
		if err := os.Rename(tmpName, path); err != nil {
			return err
		}
	}
	return os.Chmod(path, 0o600)
}

func loadAuth(serverName, endpoint string) (*authRecord, error) {
	path := authPath(serverName)
	if dirInfo, err := os.Lstat(filepath.Dir(path)); err == nil && dirInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("refusing to read OAuth tokens from a symlinked directory")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("refusing to read a symlinked OAuth token file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("OAuth token file permissions are too open; expected 0600")
	}
	if info.Size() > 1<<20 {
		return nil, errors.New("OAuth token file is unexpectedly large")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var record authRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, errors.New("OAuth token file is corrupt")
	}
	if record.Version != 1 || record.Config.ClientID == "" || record.Config.TokenURL == "" {
		return nil, errors.New("OAuth token file is incomplete")
	}
	if record.Token.AccessToken == "" && record.Token.RefreshToken == "" {
		return nil, errors.New("OAuth token file has no token")
	}
	if record.Endpoint != endpoint {
		return nil, errors.New("saved login belongs to a different server endpoint")
	}
	return &record, nil
}

func (r *authRecord) oauth2Config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     r.Config.ClientID,
		ClientSecret: r.Config.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:   r.Config.AuthURL,
			TokenURL:  r.Config.TokenURL,
			AuthStyle: oauth2.AuthStyle(r.Config.AuthStyle),
		},
		RedirectURL: r.Config.RedirectURL,
		Scopes:      append([]string(nil), r.Config.Scopes...),
	}
}

func (r *authRecord) oauth2Token() *oauth2.Token {
	return &oauth2.Token{
		AccessToken:  r.Token.AccessToken,
		TokenType:    r.Token.TokenType,
		RefreshToken: r.Token.RefreshToken,
		Expiry:       r.Token.Expiry,
	}
}

func recordTokenSource(record *authRecord, serverName, endpoint string) (oauth2.TokenSource, error) {
	if record == nil {
		return nil, errors.New("missing OAuth session")
	}
	cfg := record.oauth2Config()
	token := record.oauth2Token()
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, oauthHTTPClient(""))
	source := cfg.TokenSource(ctx, token)
	return newSavingTokenSource(source, serverName, endpoint, cfg, token), nil
}

type savingTokenSource struct {
	mu           sync.Mutex
	source       oauth2.TokenSource
	serverName   string
	endpoint     string
	config       *oauth2.Config
	accessToken  string
	refreshToken string
}

func newSavingTokenSource(source oauth2.TokenSource, serverName, endpoint string, cfg *oauth2.Config, initial *oauth2.Token) oauth2.TokenSource {
	if source == nil {
		return nil
	}
	access, refresh := "", ""
	if initial != nil {
		access = initial.AccessToken
		refresh = initial.RefreshToken
	}
	return &savingTokenSource{
		source:       source,
		serverName:   serverName,
		endpoint:     endpoint,
		config:       cfg,
		accessToken:  access,
		refreshToken: refresh,
	}
}

func (s *savingTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, err := s.source.Token()
	if err != nil {
		return nil, err
	}
	if token.AccessToken != s.accessToken || token.RefreshToken != s.refreshToken {
		s.accessToken = token.AccessToken
		s.refreshToken = token.RefreshToken
		// A failed refresh-save must not break a working session; the next
		// launch simply asks the user to log in again.
		_ = saveAuth(s.serverName, s.endpoint, s.config, token)
	}
	return token, nil
}

// ConfiguredEndpoint returns the expanded endpoint used to bind saved logins.
func ConfiguredEndpoint(cfg *config.MCPServer) string {
	return configuredEndpoint(cfg)
}

func configuredEndpoint(cfg *config.MCPServer) string {
	if cfg == nil {
		return ""
	}
	return canonicalEndpoint(expandEnv(cfg.URL))
}

func canonicalEndpoint(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimSpace(raw)
	}
	u.Fragment = ""
	if u.Path != "/" {
		u.Path = strings.TrimRight(u.Path, "/")
	}
	return u.String()
}

// redactOAuthError removes token-like values from errors produced by remote
// services before they reach logs or the transcript.
func redactOAuthError(err error) error {
	if err == nil {
		return nil
	}
	text := oauthSecretPattern.ReplaceAllString(err.Error(), `${1}${2}[redacted]`)
	return errors.New(text)
}

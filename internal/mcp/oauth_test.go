package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"github.com/ramsterr/Wombat/internal/config"
)

type oauthMock struct {
	t             *testing.T
	base          string
	server        *httptest.Server
	mu            sync.Mutex
	browserCalls  int
	registrations int
	tokenCalls    int
	accessToken   string
	codeVerifier  string
}

func newOAuthMock(t *testing.T) *oauthMock {
	t.Helper()
	m := &oauthMock{t: t, accessToken: "access-token"}
	server := sdk.NewServer(&sdk.Implementation{Name: "secure", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "secure_tool", Description: "A protected tool"}, func(context.Context, *sdk.CallToolRequest, any) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "protected"}}}, nil, nil
	})
	mcpHandler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)

	mux := http.NewServeMux()
	prm := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resource := m.base
		if strings.HasSuffix(r.URL.Path, "/mcp") {
			resource = m.base + "/mcp"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              resource,
			"authorization_servers": []string{m.base},
		})
	}
	mux.HandleFunc("/.well-known/oauth-protected-resource", prm)
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", prm)
	metadata := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                m.base,
			"authorization_endpoint":                m.base + "/authorize",
			"token_endpoint":                        m.base + "/token",
			"registration_endpoint":                 m.base + "/register",
			"response_types_supported":              []string{"code"},
			"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported":      []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
		})
	}
	mux.HandleFunc("/.well-known/oauth-authorization-server", metadata)
	mux.HandleFunc("/.well-known/openid-configuration", metadata)
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.registrations++
		m.mu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		redirects, _ := body["redirect_uris"].([]any)
		if len(redirects) != 1 {
			http.Error(w, "missing redirect_uris", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":                  "test-client",
			"token_endpoint_auth_method": "none",
			"redirect_uris":              redirects,
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		m.mu.Lock()
		m.tokenCalls++
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "test-code" || r.Form.Get("client_id") != "test-client" {
				http.Error(w, "bad code", http.StatusBadRequest)
				return
			}
			if r.Form.Get("code_verifier") == "" {
				http.Error(w, "missing code_verifier", http.StatusBadRequest)
				return
			}
			m.mu.Lock()
			m.codeVerifier = r.Form.Get("code_verifier")
			access := m.accessToken
			m.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  access,
				"token_type":    "Bearer",
				"expires_in":    3600,
				"refresh_token": "refresh-token",
			})
		case "refresh_token":
			if r.Form.Get("refresh_token") != "refresh-token" {
				http.Error(w, "bad refresh token", http.StatusBadRequest)
				return
			}
			m.mu.Lock()
			m.accessToken = "refreshed-token"
			access := m.accessToken
			m.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  access,
				"token_type":    "Bearer",
				"expires_in":    3600,
				"refresh_token": "refresh-token-2",
			})
		default:
			http.Error(w, "unsupported grant", http.StatusBadRequest)
		}
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		access := m.accessToken
		m.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+access {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource"`, m.base))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})
	m.server = httptest.NewServer(mux)
	m.base = m.server.URL
	return m
}

func (m *oauthMock) Close() { m.server.Close() }

func (m *oauthMock) openBrowser(raw string) error {
	m.mu.Lock()
	m.browserCalls++
	m.mu.Unlock()
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	redirect := u.Query().Get("redirect_uri")
	state := u.Query().Get("state")
	if redirect == "" || state == "" {
		return fmt.Errorf("authorization URL missing redirect_uri or state")
	}
	resp, err := http.Get(redirect + "?code=test-code&state=" + url.QueryEscape(state))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("callback status %d", resp.StatusCode)
	}
	return nil
}

func TestInteractiveOAuthLoginAndTokenPersistence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	mock := newOAuthMock(t)
	defer mock.Close()
	cfg := map[string]*config.MCPServer{
		"secure": {Type: "http", URL: mock.base + "/mcp"},
	}
	var notices []string
	manager := ConnectWithOptions(context.Background(), cfg, t.TempDir(), "test", Options{
		InteractiveAuth: true,
		OpenBrowser:     mock.openBrowser,
		Notify: func(message string) {
			notices = append(notices, message)
		},
	})
	if len(manager.Servers()) != 1 {
		t.Fatal("server did not start")
	}
	server := manager.Servers()[0]
	if err := server.Err(); err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if len(server.Tools()) != 1 || server.Tools()[0].Name != "secure_tool" {
		t.Fatalf("tools = %+v", server.Tools())
	}
	result, err := server.CallTool(context.Background(), "secure_tool", map[string]any{})
	if err != nil || len(result.Content) != 1 {
		t.Fatalf("call = %+v, %v", result, err)
	}
	manager.Close()

	path := authPath("secure")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("token file mode = %v, want 0600", info.Mode().Perm())
		}
		if dirInfo, err := os.Stat(home + "/mcp-auth"); err != nil || dirInfo.Mode().Perm() != 0o700 {
			t.Fatalf("token dir = %v, %v", dirInfo, err)
		}
	}
	record, err := loadAuth("secure", canonicalEndpoint(mock.base+"/mcp"))
	if err != nil {
		t.Fatal(err)
	}
	if record.Token.AccessToken != "access-token" || record.Token.RefreshToken != "refresh-token" {
		t.Fatalf("record = %+v", record)
	}
	mock.mu.Lock()
	if mock.registrations == 0 || mock.codeVerifier == "" {
		t.Fatalf("registrations = %d, verifier = %q", mock.registrations, mock.codeVerifier)
	}
	browserBefore := mock.browserCalls
	mock.mu.Unlock()
	for _, notice := range notices {
		if strings.Contains(notice, "access-token") || strings.Contains(notice, "refresh-token") {
			t.Fatalf("notice leaked a token: %q", notice)
		}
	}

	// A second, non-interactive connection must use the saved token and must
	// not open another browser.
	manager = Connect(context.Background(), cfg, t.TempDir(), "test")
	server = manager.Servers()[0]
	if err := server.Err(); err != nil {
		t.Fatalf("saved login failed: %v", err)
	}
	manager.Close()
	mock.mu.Lock()
	if mock.browserCalls != browserBefore {
		t.Fatalf("browser was opened again: %d -> %d", browserBefore, mock.browserCalls)
	}
	mock.mu.Unlock()

	// Expired access tokens refresh in the background and the refreshed token
	// is written back to disk.
	record, err = loadAuth("secure", canonicalEndpoint(mock.base+"/mcp"))
	if err != nil {
		t.Fatal(err)
	}
	expired := &oauth2.Token{
		AccessToken:  "expired-token",
		TokenType:    "Bearer",
		RefreshToken: "refresh-token",
		Expiry:       time.Now().Add(-time.Hour),
	}
	if err := saveAuth("secure", canonicalEndpoint(mock.base+"/mcp"), record.oauth2Config(), expired); err != nil {
		t.Fatal(err)
	}
	mock.mu.Lock()
	mock.accessToken = "expired-token"
	mock.mu.Unlock()
	manager = Connect(context.Background(), cfg, t.TempDir(), "test")
	server = manager.Servers()[0]
	if err := server.Err(); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	manager.Close()
	record, err = loadAuth("secure", canonicalEndpoint(mock.base+"/mcp"))
	if err != nil {
		t.Fatal(err)
	}
	if record.Token.AccessToken != "refreshed-token" {
		t.Fatalf("refreshed token = %q", record.Token.AccessToken)
	}
	if record.Token.RefreshToken != "refresh-token-2" {
		t.Fatalf("rotated refresh token was not saved: %q", record.Token.RefreshToken)
	}

	if err := Logout("secure"); err != nil {
		t.Fatal(err)
	}
	if HasAuth("secure", mock.base+"/mcp") {
		t.Fatal("logout did not delete the token")
	}
}

func TestOAuthRequiresLoginNonInteractive(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	mock := newOAuthMock(t)
	defer mock.Close()
	manager := Connect(context.Background(), map[string]*config.MCPServer{
		"secure": {Type: "http", URL: mock.base + "/mcp"},
	}, t.TempDir(), "test")
	defer manager.Close()
	server := manager.Servers()[0]
	if err := server.Err(); err == nil || !strings.Contains(err.Error(), "wombat mcp login") {
		t.Fatalf("error = %v, want login hint", err)
	}
	mock.mu.Lock()
	if mock.browserCalls != 0 {
		t.Fatalf("non-interactive connect opened a browser")
	}
	mock.mu.Unlock()
}

func TestOAuthTokenStoreSafety(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	cfg := &config.MCPServer{Type: "http", URL: "https://example.com/mcp"}
	oauthCfg := &oauth2.Config{
		ClientID: "client",
		Endpoint: oauth2.Endpoint{AuthURL: "https://example.com/auth", TokenURL: "https://example.com/token"},
	}
	token := &oauth2.Token{AccessToken: "secret", TokenType: "Bearer"}
	if err := saveAuth("safe", canonicalEndpoint(cfg.URL), oauthCfg, token); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAuth("safe", "https://other.example.com/mcp"); err == nil {
		t.Fatal("a token for another endpoint must not load")
	}
	path := authPath("safe")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := home + "/target.json"
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := loadAuth("safe", canonicalEndpoint(cfg.URL)); err == nil {
		t.Fatal("a symlinked token file must be rejected")
	}
}

func TestOAuthRejectsUnsafeRedirect(t *testing.T) {
	for _, redirect := range []string{
		"https://127.0.0.1:19876/callback",
		"http://localhost:19876/callback",
		"http://0.0.0.0:19876/callback",
		"http://127.0.0.1:19876/callback?x=1",
		"http://127.0.0.1/callback",
	} {
		if _, err := newCallbackServer(redirect, nil, nil); err == nil {
			t.Fatalf("redirect %q was accepted", redirect)
		}
	}
}

func TestOAuthErrorRedaction(t *testing.T) {
	err := redactOAuthError(fmt.Errorf(`token error: {"access_token":"abc","refresh_token":"def","client_secret":"ghi"}`))
	for _, secret := range []string{"abc", "def", "ghi"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("secret %q leaked: %v", secret, err)
		}
	}
}

func TestOAuthEnabledHeuristic(t *testing.T) {
	if !OAuthEnabled(&config.MCPServer{URL: "https://example.com/mcp"}) {
		t.Fatal("a plain remote server should use OAuth by default")
	}
	for _, header := range []string{"Authorization", "X-API-Key", "Private-Token", "Cookie", "X-Auth"} {
		if OAuthEnabled(&config.MCPServer{URL: "https://example.com/mcp", Headers: map[string]string{header: "x"}}) {
			t.Fatalf("header %q should disable automatic OAuth", header)
		}
	}
	disabled := false
	if OAuthEnabled(&config.MCPServer{URL: "https://example.com/mcp", OAuth: &disabled}) {
		t.Fatal("oauth:false was ignored")
	}
}

func TestCallbackRejectsStateMismatch(t *testing.T) {
	callback, err := newCallbackServer("", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer callback.Close()
	authURL := "http://127.0.0.1/authorize?state=expected"
	open := func(string) error {
		resp, err := http.Get(callback.RedirectURL() + "?code=attacker&state=wrong")
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
	callback.open = open
	_, err = callback.Fetch(context.Background(), &auth.AuthorizationArgs{URL: authURL})
	if err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatalf("err = %v, want state mismatch", err)
	}
}

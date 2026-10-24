package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	DefaultMCPTimeout     = 15
	DefaultMCPCallTimeout = 300
)

// MCPServer is one Model Context Protocol server. It accepts both the Wombat
// shape and the common Claude/Cursor shape (command + args + env) and the
// OpenCode shape (command as an array, environment). Remote servers use url +
// headers and either the streamable HTTP or the older SSE transport.
type MCPServer struct {
	Type              string            `json:"type,omitempty"`
	Command           string            `json:"command,omitempty"`
	Args              []string          `json:"args,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	Cwd               string            `json:"cwd,omitempty"`
	URL               string            `json:"url,omitempty"`
	Headers           map[string]string `json:"headers,omitempty"`
	Enabled           *bool             `json:"enabled,omitempty"`
	Disabled          bool              `json:"disabled,omitempty"`
	TimeoutSeconds    int               `json:"timeout_seconds,omitempty"`
	CallSeconds       int               `json:"call_timeout_seconds,omitempty"`
	Class             string            `json:"class,omitempty"`
	Workers           bool              `json:"workers,omitempty"`
	OAuth             *bool             `json:"oauth,omitempty"`
	OAuthRedirectURL  string            `json:"oauth_redirect_url,omitempty"`
	OAuthClientID     string            `json:"oauth_client_id,omitempty"`
	OAuthClientSecret string            `json:"oauth_client_secret,omitempty"`
	OAuthScopes       []string          `json:"oauth_scopes,omitempty"`
	AllowTools        []string          `json:"allow_tools,omitempty"`
	ExcludeTools      []string          `json:"exclude_tools,omitempty"`
}

// UnmarshalJSON also accepts OpenCode-style command arrays and the
// "environment" key used by several other MCP hosts.
func (s *MCPServer) UnmarshalJSON(data []byte) error {
	type rawServer struct {
		Type              string            `json:"type"`
		Command           json.RawMessage   `json:"command"`
		Args              []string          `json:"args"`
		Env               map[string]string `json:"env"`
		Environment       map[string]string `json:"environment"`
		Cwd               string            `json:"cwd"`
		URL               string            `json:"url"`
		Headers           map[string]string `json:"headers"`
		Enabled           *bool             `json:"enabled"`
		Disabled          bool              `json:"disabled"`
		TimeoutSeconds    int               `json:"timeout_seconds"`
		CallSeconds       int               `json:"call_timeout_seconds"`
		Class             string            `json:"class"`
		Workers           bool              `json:"workers"`
		OAuth             json.RawMessage   `json:"oauth"`
		OAuthRedirectURL  string            `json:"oauth_redirect_url"`
		OAuthClientID     string            `json:"oauth_client_id"`
		OAuthClientSecret string            `json:"oauth_client_secret"`
		OAuthScopes       []string          `json:"oauth_scopes"`
		AllowTools        []string          `json:"allow_tools"`
		ExcludeTools      []string          `json:"exclude_tools"`
	}
	var raw rawServer
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.Type = raw.Type
	s.Args = append([]string(nil), raw.Args...)
	s.Env = map[string]string{}
	for k, v := range raw.Environment {
		s.Env[k] = v
	}
	for k, v := range raw.Env {
		s.Env[k] = v
	}
	if len(s.Env) == 0 {
		s.Env = nil
	}
	s.Cwd = raw.Cwd
	s.URL = raw.URL
	s.Headers = raw.Headers
	s.Enabled = raw.Enabled
	s.Disabled = raw.Disabled
	s.TimeoutSeconds = raw.TimeoutSeconds
	s.CallSeconds = raw.CallSeconds
	s.Class = raw.Class
	s.Workers = raw.Workers
	s.OAuthRedirectURL = raw.OAuthRedirectURL
	s.OAuthClientID = raw.OAuthClientID
	s.OAuthClientSecret = raw.OAuthClientSecret
	s.OAuthScopes = raw.OAuthScopes
	s.AllowTools = raw.AllowTools
	if len(raw.OAuth) > 0 && string(raw.OAuth) != "null" {
		var enabled bool
		if err := json.Unmarshal(raw.OAuth, &enabled); err == nil {
			s.OAuth = &enabled
		} else {
			var options struct {
				RedirectURI   string `json:"redirectUri"`
				RedirectURL   string `json:"redirect_uri"`
				ClientID      string `json:"clientId"`
				ClientID2     string `json:"client_id"`
				ClientSecret  string `json:"clientSecret"`
				ClientSecret2 string `json:"client_secret"`
			}
			if err := json.Unmarshal(raw.OAuth, &options); err != nil {
				return fmt.Errorf("mcp oauth must be a boolean or an object")
			}
			on := true
			s.OAuth = &on
			if s.OAuthRedirectURL == "" {
				s.OAuthRedirectURL = firstMCPValue(options.RedirectURL, options.RedirectURI)
			}
			if s.OAuthClientID == "" {
				s.OAuthClientID = firstMCPValue(options.ClientID2, options.ClientID)
			}
			if s.OAuthClientSecret == "" {
				s.OAuthClientSecret = firstMCPValue(options.ClientSecret2, options.ClientSecret)
			}
		}
	}
	s.ExcludeTools = raw.ExcludeTools
	if len(raw.Command) == 0 || string(raw.Command) == "null" {
		return nil
	}
	var command string
	if err := json.Unmarshal(raw.Command, &command); err == nil {
		s.Command = command
		return nil
	}
	var commandLine []string
	if err := json.Unmarshal(raw.Command, &commandLine); err != nil {
		return fmt.Errorf("mcp command must be a string or an array of strings")
	}
	if len(commandLine) > 0 {
		s.Command = commandLine[0]
		s.Args = append(append([]string(nil), commandLine[1:]...), s.Args...)
	}
	return nil
}

// EffectiveEnabled reports whether the server should be started.
func (s *MCPServer) EffectiveEnabled() bool {
	if s == nil {
		return false
	}
	if s.Disabled {
		return false
	}
	if s.Enabled != nil {
		return *s.Enabled
	}
	return true
}

// Transport normalizes the transport aliases used by MCP hosts.
func (s *MCPServer) Transport() string {
	if s == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(s.Type)) {
	case "stdio", "local", "command":
		return "stdio"
	case "http", "streamable-http", "streamable", "remote":
		return "http"
	case "sse":
		return "sse"
	case "":
		if strings.TrimSpace(s.URL) != "" {
			return "http"
		}
		if strings.TrimSpace(s.Command) != "" {
			return "stdio"
		}
	}
	return ""
}

// EffectiveClass is the orchestration class for this server's tools.
func (s *MCPServer) EffectiveClass() string {
	if s == nil {
		return "mcp"
	}
	switch strings.ToLower(strings.TrimSpace(s.Class)) {
	case "read":
		return "read"
	case "network":
		return "network"
	case "write":
		return "write"
	case "edit":
		return "edit"
	case "bash":
		return "bash"
	}
	return "mcp"
}

func (s *MCPServer) Timeout() time.Duration {
	seconds := s.TimeoutSeconds
	if seconds <= 0 {
		seconds = DefaultMCPTimeout
	}
	return time.Duration(seconds) * time.Second
}

func (s *MCPServer) CallTimeout() time.Duration {
	seconds := s.CallSeconds
	if seconds <= 0 {
		seconds = DefaultMCPCallTimeout
	}
	return time.Duration(seconds) * time.Second
}

func firstMCPValue(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

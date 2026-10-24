package config

import (
	"encoding/json"
	"testing"
)

func TestMCPServerAcceptsCommandForms(t *testing.T) {
	var stringCommand MCPServer
	if err := json.Unmarshal([]byte(`{"command":"npx","args":["-y","server"]}`), &stringCommand); err != nil {
		t.Fatal(err)
	}
	if stringCommand.Transport() != "stdio" || stringCommand.Command != "npx" || len(stringCommand.Args) != 2 {
		t.Fatalf("server = %+v", stringCommand)
	}

	var arrayCommand MCPServer
	if err := json.Unmarshal([]byte(`{"type":"local","command":["npx","-y","server"],"environment":{"A":"1"}}`), &arrayCommand); err != nil {
		t.Fatal(err)
	}
	if arrayCommand.Transport() != "stdio" || arrayCommand.Command != "npx" || len(arrayCommand.Args) != 2 {
		t.Fatalf("server = %+v", arrayCommand)
	}
	if arrayCommand.Env["A"] != "1" {
		t.Fatalf("environment was not merged: %+v", arrayCommand.Env)
	}
}

func TestMCPServerRemoteAndEnabled(t *testing.T) {
	var server MCPServer
	if err := json.Unmarshal([]byte(`{"url":"https://example.com/mcp","headers":{"Authorization":"Bearer x"}}`), &server); err != nil {
		t.Fatal(err)
	}
	if server.Transport() != "http" || !server.EffectiveEnabled() {
		t.Fatalf("server = %+v", server)
	}
	var disabled MCPServer
	if err := json.Unmarshal([]byte(`{"url":"https://example.com/mcp","disabled":true}`), &disabled); err != nil {
		t.Fatal(err)
	}
	if disabled.EffectiveEnabled() {
		t.Fatalf("disabled server = %+v", disabled)
	}
}

func TestMCPServerOAuthForms(t *testing.T) {
	var server MCPServer
	if err := json.Unmarshal([]byte(`{"url":"https://example.com/mcp","oauth":false}`), &server); err != nil {
		t.Fatal(err)
	}
	if server.OAuth == nil || *server.OAuth {
		t.Fatalf("oauth = %v, want false", server.OAuth)
	}
	if err := json.Unmarshal([]byte(`{"url":"https://example.com/mcp","oauth":{"redirectUri":"http://127.0.0.1:19876/callback","clientId":"cid"}}`), &server); err != nil {
		t.Fatal(err)
	}
	if server.OAuth == nil || !*server.OAuth {
		t.Fatalf("oauth = %v, want true", server.OAuth)
	}
	if server.OAuthRedirectURL != "http://127.0.0.1:19876/callback" || server.OAuthClientID != "cid" {
		t.Fatalf("server = %+v", server)
	}
}

package cli

import "testing"

func TestParseMCPAddLocal(t *testing.T) {
	server, err := parseMCPAdd("everything", []string{
		"--env", "TOKEN=abc", "--header", "X-Test: yes",
		"--", "npx", "-y", "@modelcontextprotocol/server-everything",
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.Transport() != "stdio" || server.Command != "npx" || len(server.Args) != 2 {
		t.Fatalf("server = %+v", server)
	}
	if server.Env["TOKEN"] != "abc" || server.Headers["X-Test"] != "yes" {
		t.Fatalf("env/headers = %+v %+v", server.Env, server.Headers)
	}
}

func TestParseMCPAddRemote(t *testing.T) {
	server, err := parseMCPAdd("context7", []string{
		"--url", "https://mcp.context7.com/mcp",
		"--header", "Authorization=Bearer secret",
		"--class", "read",
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.Transport() != "http" || server.URL == "" || server.Headers["Authorization"] != "Bearer secret" {
		t.Fatalf("server = %+v", server)
	}
	if server.EffectiveClass() != "read" {
		t.Fatalf("class = %q", server.EffectiveClass())
	}
}

func TestParseMCPAddRejectsBadInput(t *testing.T) {
	if _, err := parseMCPAdd("bad", []string{"--url", "https://example.com", "--", "echo"}); err == nil {
		t.Fatal("command plus url should fail")
	}
	if _, err := parseMCPAdd("bad", []string{}); err == nil {
		t.Fatal("empty server should fail")
	}
	if _, err := parseMCPAdd("bad", []string{"--env", "oops", "--", "echo"}); err == nil {
		t.Fatal("bad env should fail")
	}
}

func TestParseMCPFileFormats(t *testing.T) {
	for _, raw := range []string{
		`{"mcpServers":{"a":{"command":"npx","args":["-y","x"]}}}`,
		`{"mcp":{"servers":{"a":{"type":"local","command":["npx","-y","x"]}}}}`,
	} {
		servers, err := parseMCPFile([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if len(servers) != 1 || servers["a"].Command != "npx" {
			t.Fatalf("servers = %+v (input %s)", servers, raw)
		}
	}
}

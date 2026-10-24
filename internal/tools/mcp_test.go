package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	wombatmcp "github.com/ramsterr/Wombat/internal/mcp"
)

func TestMCPToolNamesAreSafeAndUnique(t *testing.T) {
	used := map[string]bool{}
	first := uniqueMCPToolName("my server", "read/file", used)
	if first != "mcp__my_server__read_file" {
		t.Fatalf("name = %q", first)
	}
	second := uniqueMCPToolName("my server", "read file", used)
	if second == first || !strings.HasSuffix(second, "_2") {
		t.Fatalf("duplicate name = %q, first = %q", second, first)
	}
	long := uniqueMCPToolName(strings.Repeat("server", 20), strings.Repeat("tool", 40), used)
	if len(long) > 64 {
		t.Fatalf("name is too long (%d): %q", len(long), long)
	}
	for _, r := range long {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			t.Fatalf("name has an unsafe character: %q", long)
		}
	}
}

func TestMCPToolSchema(t *testing.T) {
	schema := normalizeMCPSchema(map[string]any{"properties": map[string]any{"x": map[string]any{"type": "string"}}})
	if schema["type"] != "object" {
		t.Fatalf("schema = %+v", schema)
	}
	raw := normalizeMCPSchema([]byte(`{"type":"object","properties":{"x":{"type":"number"}}}`))
	if raw["type"] != "object" {
		t.Fatalf("raw schema = %+v", raw)
	}
}

func TestFormatMCPResult(t *testing.T) {
	var media []Media
	env := &Env{}
	env.SetMediaSink(&media)
	result := &sdk.CallToolResult{Content: []sdk.Content{
		&sdk.TextContent{Text: "hello"},
		&sdk.ImageContent{Data: []byte{1, 2, 3}, MIMEType: "image/png"},
	}}
	out, err := formatMCPResult(result, env, "srv", "shot")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "image attached") {
		t.Fatalf("out = %q", out)
	}
	if len(media) != 1 || media[0].MIME != "image/png" || len(media[0].Data) != 3 {
		t.Fatalf("media = %+v", media)
	}

	if _, err := formatMCPResult(&sdk.CallToolResult{
		IsError: true,
		Content: []sdk.Content{&sdk.TextContent{Text: "boom"}},
	}, nil, "srv", "bad"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v", err)
	}
}

func TestMCPCallIsPermissionGated(t *testing.T) {
	var kind, detail string
	env := &Env{Permit: func(k, d string) Decision {
		kind, detail = k, d
		return Decision{Feedback: "no"}
	}}
	tool := &MCPTool{
		server:   &wombatmcp.Server{ID: "github"},
		tool:     &sdk.Tool{Name: "create_issue"},
		name:     "mcp__github__create_issue",
		class:    "mcp",
		callWait: time.Second,
	}
	_, err := tool.Run(context.Background(), []byte(`{"title":"x"}`), env)
	if err == nil {
		t.Fatal("permission denial was ignored")
	}
	if kind != "mcp:github" || !strings.Contains(detail, "github.create_issue") {
		t.Fatalf("kind = %q detail = %q", kind, detail)
	}
}

func TestMCPPermissionFallsBackToSharedSetting(t *testing.T) {
	perms := map[string]string{"mcp": "allow"}
	if value, ok := PermissionValue(perms, "mcp:github"); !ok || value != "allow" {
		t.Fatalf("value = %q ok = %v", value, ok)
	}
	if PermKindFor("mcp:github") != PermMCP {
		t.Fatal("mcp permission kind was not recognized")
	}
}

func TestMCPWorkerEligibility(t *testing.T) {
	leadOnly := &MCPTool{class: "mcp"}
	if WorkerEligible(leadOnly) {
		t.Fatal("default MCP tools must stay on the lead agent")
	}
	readOnly := &MCPTool{class: "read"}
	if !WorkerEligible(readOnly) {
		t.Fatal("read-only MCP tools should be safe for workers")
	}
	optedIn := &MCPTool{class: "mcp", workers: true}
	if !WorkerEligible(optedIn) {
		t.Fatal("--workers should expose an MCP tool")
	}
}

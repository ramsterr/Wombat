package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ramsterr/Wombat/internal/config"
)

func TestConnectAndCallInMemoryServer(t *testing.T) {
	ctx := context.Background()
	server := sdk.NewServer(&sdk.Implementation{Name: "test-server", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "echo", Description: "Echo text"}, func(_ context.Context, _ *sdk.CallToolRequest, input struct {
		Text string `json:"text"`
	}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: input.Text}}}, nil, nil
	})
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	session, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	got := connectWithTransport(ctx, &Server{
		ID: "test", Config: &config.MCPServer{}, stderr: &tailBuffer{max: 1024},
	}, clientTransport, "test")
	if err := got.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got.Tools()) != 1 || got.Tools()[0].Name != "echo" {
		t.Fatalf("tools = %+v", got.Tools())
	}
	result, err := got.CallTool(ctx, "echo", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content = %+v", result.Content)
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok || text.Text != "hello" {
		t.Fatalf("content = %#v", result.Content[0])
	}
}

func TestToolAllowAndExclude(t *testing.T) {
	cfg := &config.MCPServer{AllowTools: []string{"read_*", "list"}}
	for _, name := range []string{"read_file", "list"} {
		if !toolAllowed(cfg, name) {
			t.Fatalf("%s should be allowed", name)
		}
	}
	if toolAllowed(cfg, "write_file") {
		t.Fatal("write_file should not be allowed")
	}
	cfg = &config.MCPServer{ExcludeTools: []string{"delete_*"}}
	if toolAllowed(cfg, "delete_file") || !toolAllowed(cfg, "read_file") {
		t.Fatal("exclude filter is wrong")
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("MCP_TEST_VALUE", "hello")
	if got := expandEnv("x-${MCP_TEST_VALUE}-y"); got != "x-hello-y" {
		t.Fatalf("expandEnv = %q", got)
	}
	if got := expandEnv("${MCP_TEST_MISSING:-fallback}"); got != "fallback" {
		t.Fatalf("expandEnv fallback = %q", got)
	}
	if got := expandEnv("{env:MCP_TEST_VALUE}"); got != "hello" {
		t.Fatalf("expandEnv opencode = %q", got)
	}
	if got := expandEnv("${MCP_TEST_MISSING}"); !strings.Contains(got, "${MCP_TEST_MISSING}") {
		t.Fatalf("missing value should be left alone: %q", got)
	}
}

func TestConnectStreamableHTTPWithHeader(t *testing.T) {
	t.Setenv("MCP_TEST_TOKEN", "secret")
	ctx := context.Background()
	server := sdk.NewServer(&sdk.Implementation{Name: "remote", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "ping", Description: "Ping"}, func(context.Context, *sdk.CallToolRequest, any) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "pong"}}}, nil, nil
	})
	mcpHandler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	got := connectOne(ctx, "remote", &config.MCPServer{
		URL:     httpServer.URL,
		Headers: map[string]string{"Authorization": "Bearer ${MCP_TEST_TOKEN}"},
	}, t.TempDir(), "test", Options{})
	if err := got.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got.Tools()) != 1 || got.Tools()[0].Name != "ping" {
		t.Fatalf("tools = %+v", got.Tools())
	}
	_ = got.session.Close()
}

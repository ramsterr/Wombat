package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

func TestBuildAgentAddsMCPTools(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "echo", Description: "Echo text"}, func(_ context.Context, _ *sdk.CallToolRequest, input struct {
		Text string `json:"text"`
	}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "echo: " + input.Text}}}, nil, nil
	})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	cfg := &config.Config{
		Provider:  "openai",
		Model:     "test-model",
		Mode:      "create",
		Providers: map[string]*config.Provider{"openai": {Kind: "openai", BaseURL: "http://127.0.0.1:1/v1"}},
		Permissions: map[string]string{
			"mcp": "allow",
		},
		MCPServers: map[string]*config.MCPServer{
			"test": {Type: "http", URL: httpServer.URL},
		},
	}
	sess, err := session.CreateAt(home, t.TempDir(), "openai", "test-model", "test")
	if err != nil {
		t.Fatal(err)
	}
	ag, err := buildAgent(cfg, runOpts{}, sess, newPrompter(false, false, cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer ag.Env.CloseMCP()

	var echo tools.Tool
	for _, tool := range ag.Tools {
		if tool.Name() == "mcp__test__echo" {
			echo = tool
		}
	}
	if echo == nil {
		t.Fatalf("MCP tool missing from agent toolset (%d tools)", len(ag.Tools))
	}
	if !strings.Contains(echo.Description(), "MCP server test") {
		t.Fatalf("description = %q", echo.Description())
	}
	out, err := echo.Run(context.Background(), json.RawMessage(`{"text":"hi"}`), ag.Env)
	if err != nil {
		t.Fatal(err)
	}
	if out != "echo: hi" {
		t.Fatalf("output = %q", out)
	}
}

package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	wombatmcp "github.com/ramsterr/Wombat/internal/mcp"
)

// MCPTool adapts one Model Context Protocol tool to Wombat's Tool interface.
// MCP calls are gated by a per-server permission ("mcp:<server>") and are
// exclusive by default because a server tool may change external state.
type MCPTool struct {
	server   *wombatmcp.Server
	tool     *sdk.Tool
	name     string
	class    string
	workers  bool
	callWait time.Duration
}

// MCPTools turns every successfully connected MCP tool into a Wombat tool.
func MCPTools(manager *wombatmcp.Manager) []Tool {
	if manager == nil {
		return nil
	}
	used := map[string]bool{}
	var out []Tool
	for _, server := range manager.Servers() {
		if server == nil || server.Err() != nil || server.Config == nil {
			continue
		}
		for _, tool := range server.Tools() {
			name := uniqueMCPToolName(server.Name(), tool.Name, used)
			out = append(out, &MCPTool{
				server:   server,
				tool:     tool,
				name:     name,
				class:    server.Config.EffectiveClass(),
				workers:  server.Config.Workers,
				callWait: server.Config.CallTimeout(),
			})
		}
	}
	return out
}

func (t *MCPTool) Name() string  { return t.name }
func (t *MCPTool) Class() string { return t.class }

// AllowedInWorkers reports whether delegated workers may call this tool.
// Read-only servers are safe by default; others opt in with --workers.
func (t *MCPTool) AllowedInWorkers() bool { return t.workers || t.class == "read" }

func (t *MCPTool) Description() string {
	desc := strings.TrimSpace(t.tool.Description)
	if title := toolTitle(t.tool); title != "" && !strings.Contains(strings.ToLower(desc), strings.ToLower(title)) {
		desc = title + ": " + desc
	}
	if desc == "" {
		desc = "Tool " + t.tool.Name + " from MCP server " + t.server.Name() + "."
	}
	desc = fmt.Sprintf("[MCP server %s] %s", t.server.Name(), desc)
	runes := []rune(desc)
	if len(runes) > 2000 {
		desc = string(runes[:1999]) + "…"
	}
	return desc
}

func (t *MCPTool) Schema() map[string]any {
	schema := normalizeMCPSchema(t.tool.InputSchema)
	if schema == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return schema
}

func (t *MCPTool) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	args := map[string]any{}
	if text := strings.TrimSpace(string(raw)); text != "" && text != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("bad arguments: %w", err)
		}
	}
	detail := fmt.Sprintf("%s.%s", t.server.Name(), t.tool.Name)
	if encoded, err := json.Marshal(args); err == nil && string(encoded) != "{}" {
		detail += " " + Truncate(string(encoded), 0, 0, 500)
	}
	if err := env.Check("mcp:"+t.server.Name(), RedactSecrets(detail)); err != nil {
		return "", err
	}
	callCtx := ctx
	cancel := func() {}
	if t.callWait > 0 {
		callCtx, cancel = context.WithTimeout(ctx, t.callWait)
	}
	defer cancel()
	result, err := t.server.CallTool(callCtx, t.tool.Name, args)
	if err != nil {
		if callCtx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("MCP tool %s.%s timed out after %s", t.server.Name(), t.tool.Name, t.callWait)
		}
		return "", err
	}
	return formatMCPResult(result, env, t.server.Name(), t.tool.Name)
}

func formatMCPResult(result *sdk.CallToolResult, env *Env, server, tool string) (string, error) {
	if result == nil {
		return "ok", nil
	}
	var parts []string
	images := 0
	for _, item := range result.Content {
		switch content := item.(type) {
		case *sdk.TextContent:
			if text := strings.TrimSpace(content.Text); text != "" {
				parts = append(parts, text)
			}
		case *sdk.ImageContent:
			images++
			name := fmt.Sprintf("%s-%s-%d", server, tool, images)
			if env != nil {
				env.AddMedia(Media{
					Data: content.Data,
					Size: int64(len(content.Data)),
					MIME: content.MIMEType,
					Name: name,
				})
			}
			if content.MIMEType != "" {
				parts = append(parts, fmt.Sprintf("[image attached: %s (%s)]", name, content.MIMEType))
			} else {
				parts = append(parts, fmt.Sprintf("[image attached: %s]", name))
			}
		case *sdk.AudioContent:
			parts = append(parts, fmt.Sprintf("[audio content omitted: %s, %d bytes]", content.MIMEType, len(content.Data)))
		case *sdk.ResourceLink:
			label := firstNonEmpty(content.Title, content.Name, content.URI)
			if content.URI != "" && !strings.Contains(label, content.URI) {
				label += " (" + content.URI + ")"
			}
			parts = append(parts, "[resource: "+label+"]")
		case *sdk.EmbeddedResource:
			if content.Resource != nil {
				if text := strings.TrimSpace(content.Resource.Text); text != "" {
					parts = append(parts, text)
				} else if len(content.Resource.Blob) > 0 {
					parts = append(parts, fmt.Sprintf("[resource blob omitted: %s, %d bytes]", content.Resource.URI, len(content.Resource.Blob)))
				}
			}
		}
	}
	if len(result.Content) == 0 && result.StructuredContent != nil {
		if raw, err := json.MarshalIndent(result.StructuredContent, "", "  "); err == nil {
			parts = append(parts, string(raw))
		}
	}
	out := strings.TrimSpace(strings.Join(parts, "\n"))
	if result.IsError {
		if out == "" {
			out = "the MCP server reported an error"
		}
		return "", fmt.Errorf("%s", out)
	}
	if out == "" {
		return "ok", nil
	}
	return out, nil
}

func toolTitle(tool *sdk.Tool) string {
	if tool == nil {
		return ""
	}
	if tool.Title != "" {
		return strings.TrimSpace(tool.Title)
	}
	if tool.Annotations != nil {
		return strings.TrimSpace(tool.Annotations.Title)
	}
	return ""
}

func normalizeMCPSchema(value any) map[string]any {
	if value == nil {
		return nil
	}
	var schema map[string]any
	switch v := value.(type) {
	case map[string]any:
		schema = v
	case json.RawMessage:
		if err := json.Unmarshal(v, &schema); err != nil {
			return nil
		}
	case []byte:
		if err := json.Unmarshal(v, &schema); err != nil {
			return nil
		}
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			return nil
		}
	}
	if len(schema) == 0 {
		return nil
	}
	out := make(map[string]any, len(schema)+1)
	for key, item := range schema {
		out[key] = item
	}
	if _, ok := out["type"]; !ok {
		out["type"] = "object"
	}
	if out["type"] == "object" {
		if _, ok := out["properties"]; !ok {
			out["properties"] = map[string]any{}
		}
	}
	return out
}

func uniqueMCPToolName(server, tool string, used map[string]bool) string {
	base := "mcp__" + sanitizeToolPart(server, 20) + "__" + sanitizeToolPart(tool, 32)
	if len(base) > 64 {
		base = base[:55] + "__" + shortHash(server+"\x00"+tool)
	}
	name := base
	for i := 2; used[name]; i++ {
		suffix := fmt.Sprintf("_%d", i)
		name = base
		if len(name)+len(suffix) > 64 {
			name = name[:64-len(suffix)]
		}
		name += suffix
	}
	used[name] = true
	return name
}

func sanitizeToolPart(value string, max int) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "server"
	}
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:4])
}

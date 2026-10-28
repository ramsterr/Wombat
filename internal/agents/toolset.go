package agents

import (
	"path"
	"strings"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/tools"
)

// orchestrationTools never go to a delegated specialist: only the lead
// plans, delegates, and verifies the ledger.
var orchestrationTools = map[string]bool{
	"question":       true,
	"update_plan":    true,
	"run_plan":       true,
	"delegate":       true,
	"delegate_many":  true,
	"delegate_write": true,
	"run_checks":     true,
	"resume_worker":  true,
}

// serverNamer is implemented by MCP tool adapters so the agent toolset can
// be scoped to specific servers.
type serverNamer interface {
	ServerName() string
}

// FilterTools builds the toolset for one custom agent from the session's
// full tool list.
//
// Matching rules for Tools and ExcludeTools:
//   - "bash", "write", …            exact tool name
//   - "web*", "mcp__github__*"      glob on the tool name
//   - "class:read", "class:network" tool class
//   - "mcp:github", "mcp:*"         every tool from an MCP server
//
// An empty allowlist means every worker-eligible tool. Exclusions always
// win, and orchestration tools are never offered.
func FilterTools(all []tools.Tool, def *config.AgentDef) []tools.Tool {
	if def == nil {
		return nil
	}
	var out []tools.Tool
	for _, t := range all {
		if t == nil || !tools.WorkerEligible(t) {
			continue
		}
		if orchestrationTools[strings.ToLower(t.Name())] {
			continue
		}
		if server, ok := mcpServerOf(t); ok && len(def.MCPServers) > 0 {
			if !matchesAnyFold(def.MCPServers, server) {
				continue
			}
		}
		if len(def.Tools) > 0 && !matchesToolList(def.Tools, t) {
			continue
		}
		if matchesToolList(def.ExcludeTools, t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func matchesToolList(patterns []string, t tools.Tool) bool {
	for _, pattern := range patterns {
		if matchToolPattern(pattern, t) {
			return true
		}
	}
	return false
}

func matchToolPattern(pattern string, t tools.Tool) bool {
	p := strings.ToLower(strings.TrimSpace(pattern))
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "class:") {
		return strings.EqualFold(t.Class(), strings.TrimSpace(p[len("class:"):]))
	}
	if strings.HasPrefix(p, "mcp:") {
		server, ok := mcpServerOf(t)
		if !ok {
			return false
		}
		want := strings.TrimSpace(p[len("mcp:"):])
		return want == "*" || strings.EqualFold(server, want)
	}
	name := strings.ToLower(t.Name())
	if p == name {
		return true
	}
	if strings.ContainsAny(p, "*?[") {
		ok, err := path.Match(p, name)
		return err == nil && ok
	}
	return false
}

func mcpServerOf(t tools.Tool) (string, bool) {
	if namer, ok := t.(serverNamer); ok {
		name := strings.TrimSpace(namer.ServerName())
		if name != "" {
			return name, true
		}
	}
	return "", false
}

func matchesAnyFold(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(strings.TrimSpace(v), want) {
			return true
		}
	}
	return false
}

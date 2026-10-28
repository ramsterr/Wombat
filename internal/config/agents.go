package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MaxAgentNameLen is the longest allowed custom agent name.
const MaxAgentNameLen = 32

// MaxAgentSteps caps a custom agent's step budget so a definition cannot
// turn into an unbounded run.
const MaxAgentSteps = 100

// AgentDef defines a custom agent: a specialist the planner can hand work
// to. It carries its own system prompt, tool allowlist, permission
// decisions, MCP servers, and skills.
//
// Definitions are read from the "agents" map in config.json, from
// ~/.wombat/agents/*.json|*.md, and from .wombat/agents/*.json|*.md in the
// project. Project definitions win over user definitions, which win over
// inline config entries.
type AgentDef struct {
	// Name is the stable id shown in the agent picker and used by the
	// planner to assign steps. It is required.
	Name string `json:"name"`
	// Description tells the planner and the picker when to use this agent.
	Description string `json:"description,omitempty"`
	// Prompt is the agent's system prompt section. PromptFile is an
	// alternative that names a markdown file to read instead.
	Prompt     string `json:"prompt,omitempty"`
	PromptFile string `json:"prompt_file,omitempty"`
	// Provider and Model override the session's provider and model.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	// Tools is an allowlist of tool names, globs, classes ("class:read"),
	// or MCP servers ("mcp:<server>"). Empty means every worker-eligible
	// tool.
	Tools []string `json:"tools,omitempty"`
	// ExcludeTools removes tools that Tools (or the defaults) would
	// otherwise include.
	ExcludeTools []string `json:"exclude_tools,omitempty"`
	// MCPServers limits the agent to the named MCP servers. Empty means
	// every connected server.
	MCPServers []string `json:"mcp_servers,omitempty"`
	// Permissions maps a permission kind to "allow" or "deny". Kinds are
	// the tool classes (bash, write, edit, network, mcp, mcp:<server>,
	// dangerous, sensitive-read, outside, sandbox-write). Custom agents
	// never prompt, so "ask" is not accepted.
	Permissions map[string]string `json:"permissions,omitempty"`
	// Skills names skills from ~/.wombat/skills or .wombat/skills whose
	// instructions are appended to the agent's system prompt.
	Skills []string `json:"skills,omitempty"`
	// MaxSteps is the per-run step budget. Zero uses the session default.
	MaxSteps int `json:"max_steps,omitempty"`
	// ReadOnly removes write, edit, and mutating bash from the agent even
	// when its tool list would allow them.
	ReadOnly bool `json:"readonly,omitempty"`

	// Source records where the definition came from. It is set by the
	// loader and never serialized.
	Source string `json:"-"`
}

var agentNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// reservedAgentNames are ids the planner already uses for itself.
var reservedAgentNames = map[string]bool{
	"default": true,
	"lead":    true,
	"main":    true,
	"general": true,
}

// NormalizeAgentName lowercases a name and turns runs of spaces into single
// dashes, so "Math Research" and "math-research" are the same agent.
func NormalizeAgentName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	return strings.Trim(name, "-")
}

// Validate checks the definition is safe to load and run.
func (d *AgentDef) Validate() error {
	if d == nil {
		return fmt.Errorf("agent definition is nil")
	}
	d.Name = NormalizeAgentName(d.Name)
	if d.Name == "" {
		return fmt.Errorf("name is required")
	}
	if len(d.Name) > MaxAgentNameLen {
		return fmt.Errorf("name %q is too long (max %d characters)", d.Name, MaxAgentNameLen)
	}
	if !agentNamePattern.MatchString(d.Name) {
		return fmt.Errorf("name %q must use lowercase letters, digits, dashes, or underscores", d.Name)
	}
	if reservedAgentNames[d.Name] {
		return fmt.Errorf("name %q is reserved", d.Name)
	}
	if d.MaxSteps < 0 || d.MaxSteps > MaxAgentSteps {
		return fmt.Errorf("max_steps must be between 0 and %d", MaxAgentSteps)
	}
	if len(d.Tools) > 64 {
		return fmt.Errorf("too many tool entries (%d); max 64", len(d.Tools))
	}
	if len(d.ExcludeTools) > 64 {
		return fmt.Errorf("too many exclude_tools entries (%d); max 64", len(d.ExcludeTools))
	}
	if len(d.MCPServers) > 32 {
		return fmt.Errorf("too many mcp_servers entries (%d); max 32", len(d.MCPServers))
	}
	if len(d.Skills) > 32 {
		return fmt.Errorf("too many skills entries (%d); max 32", len(d.Skills))
	}
	for kind, value := range d.Permissions {
		kind = strings.TrimSpace(strings.ToLower(kind))
		if !validAgentPermKind(kind) {
			return fmt.Errorf("unknown permission %q", kind)
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "allow", "deny":
		default:
			return fmt.Errorf("permission %q must be allow or deny (custom agents cannot ask)", kind)
		}
	}
	return nil
}

// validAgentPermKind mirrors the permission kinds the tool layer enforces.
func validAgentPermKind(kind string) bool {
	switch kind {
	case "bash", "write", "edit", "outside", "network", "sandbox-write",
		"dangerous", "sensitive-read", "mcp":
		return true
	}
	return strings.HasPrefix(kind, "mcp:") && len(kind) > len("mcp:")
}

// ParseAgentMarkdown parses a markdown agent definition:
//
//	---
//	name: math-research
//	description: Verifies mathematical claims
//	tools: read, grep, websearch
//	permissions: write=deny, bash=allow
//	skills: careful-proofs
//	---
//	You are a mathematician...
//
// The frontmatter is deliberately a flat key: value block so no YAML
// dependency is needed. Unknown keys are ignored; the body becomes the
// prompt.
func ParseAgentMarkdown(data []byte, filename string) (*AgentDef, error) {
	meta, body, err := ParseFrontmatter(data)
	if err != nil {
		return nil, err
	}
	def := &AgentDef{}
	def.Name = firstNonEmpty(meta["name"], "")
	def.Description = firstNonEmpty(meta["description"], meta["desc"], meta["about"], meta["when"])
	def.Prompt = firstNonEmpty(meta["prompt"], meta["system"], meta["system_prompt"], meta["instructions"])
	if strings.TrimSpace(body) != "" {
		def.Prompt = strings.TrimSpace(body)
	}
	def.PromptFile = meta["prompt_file"]
	def.Provider = meta["provider"]
	def.Model = meta["model"]
	def.Tools = splitList(firstNonEmpty(meta["tools"], meta["tool"]))
	def.ExcludeTools = splitList(firstNonEmpty(meta["exclude_tools"], meta["excludes"], meta["exclude"]))
	def.MCPServers = splitList(firstNonEmpty(meta["mcp_servers"], meta["mcp"]))
	def.Skills = splitList(firstNonEmpty(meta["skills"], meta["skill"]))
	if raw := meta["permissions"]; raw != "" {
		perms, err := parsePermissionList(raw)
		if err != nil {
			return nil, err
		}
		def.Permissions = perms
	}
	if raw := firstNonEmpty(meta["max_steps"], meta["steps"]); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("max_steps %q is not a number", raw)
		}
		def.MaxSteps = n
	}
	if raw := firstNonEmpty(meta["readonly"], meta["read_only"]); raw != "" {
		readOnly, err := strconv.ParseBool(strings.ToLower(raw))
		if err != nil {
			return nil, fmt.Errorf("readonly %q is not true or false", raw)
		}
		def.ReadOnly = readOnly
	}
	if def.Name == "" && filename != "" {
		def.Name = filename
	}
	if err := def.Validate(); err != nil {
		return nil, err
	}
	return def, nil
}

// ParseFrontmatter splits an optional "--- key: value ---" block from a
// markdown body. Keys are lowercased and dashes become underscores. It is
// shared by agent and skill files so both use one flat, dependency-free
// format.
func ParseFrontmatter(data []byte) (map[string]string, string, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimPrefix(text, "\ufeff")
	meta := map[string]string{}
	trimmed := strings.TrimLeft(text, " \t\n")
	if !strings.HasPrefix(trimmed, "---") {
		return meta, text, nil
	}
	lines := strings.Split(trimmed, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, "", fmt.Errorf("frontmatter is not closed with ---")
	}
	for _, line := range lines[1:end] {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		key = strings.ReplaceAll(key, "-", "_")
		if key == "" {
			continue
		}
		meta[key] = strings.TrimSpace(value)
	}
	return meta, strings.Join(lines[end+1:], "\n"), nil
}

// ParseAgentJSON parses a JSON agent definition, filling the name from the
// filename when it is omitted.
func ParseAgentJSON(data []byte, filename string) (*AgentDef, error) {
	var def AgentDef
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parse agent: %w", err)
	}
	if strings.TrimSpace(def.Name) == "" && filename != "" {
		def.Name = filename
	}
	if err := def.Validate(); err != nil {
		return nil, err
	}
	return &def, nil
}

// parsePermissionList reads "write=deny, bash=allow" or
// "write:deny; bash:allow".
func parsePermissionList(raw string) (map[string]string, error) {
	raw = strings.NewReplacer(";", ",", " ", ",").Replace(raw)
	perms := map[string]string{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			key, value, ok = strings.Cut(item, ":")
		}
		if !ok {
			return nil, fmt.Errorf("permission %q must look like kind=allow or kind=deny", item)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.ToLower(strings.TrimSpace(value))
		if key == "" || value == "" {
			return nil, fmt.Errorf("permission %q must look like kind=allow or kind=deny", item)
		}
		perms[key] = value
	}
	return perms, nil
}

// splitList accepts comma-separated values and drops empties.
func splitList(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

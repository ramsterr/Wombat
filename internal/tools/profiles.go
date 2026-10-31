package tools

import (
	"fmt"
	"sort"
	"strings"
)

// Capability is a small, composable bundle of tools. Profiles are presets
// built from capabilities; the model can request an optional capability at
// runtime without ever loading the full catalog.
type Capability string

const (
	CapCore        Capability = "core"
	CapEdit        Capability = "edit"
	CapWeb         Capability = "web"
	CapSemantic    Capability = "semantic"
	CapGit         Capability = "git"
	CapVerify      Capability = "verify"
	CapMedia       Capability = "media"
	CapJobs        Capability = "jobs"
	CapBatch       Capability = "batch"
	CapOrchestrate Capability = "orchestrate"
	CapFull        Capability = "full"
)

var capabilityTools = map[Capability][]string{
	CapCore: {
		"read", "write", "edit", "bash", "glob", "grep",
		"tool_search", "request_capability",
	},
	CapEdit:     {"apply_patch", "multi_edit"},
	CapWeb:      {"browser", "websearch", "webfetch"},
	CapSemantic: {"lsp", "symbols", "semantic_search"},
	CapGit:      {"git_status", "git_diff"},
	CapVerify:   {"verify", "verify_site"},
	CapMedia:    {"imagesearch"},
	CapJobs:     {"jobs", "job_output", "job_kill"},
	CapBatch:    {"batch"},
	CapFull:     nil,
}

var profileCapabilities = map[string][]Capability{
	"minimal":      {CapCore},
	"coding":       {CapCore, CapEdit},
	"web":          {CapCore, CapEdit, CapWeb},
	"semantic":     {CapCore, CapEdit, CapSemantic},
	"git":          {CapCore, CapEdit, CapGit},
	"verify":       {CapCore, CapEdit, CapVerify},
	"media":        {CapCore, CapEdit, CapMedia},
	"orchestrator": {CapCore, CapEdit},
	"full":         {CapFull},
}

var optionalCapabilities = []Capability{CapWeb, CapSemantic, CapGit, CapVerify, CapMedia}

type CapabilityMatch struct {
	Name        Capability `json:"name"`
	Description string     `json:"description"`
	Tools       []string   `json:"tools"`
}

var capabilityCatalog = []CapabilityMatch{
	{CapWeb, "Browser, web search, and page fetch for URLs, pages, screenshots, and current facts.", []string{"browser", "websearch", "webfetch"}},
	{CapSemantic, "LSP, symbols, and semantic search for definitions, references, and code navigation.", []string{"lsp", "symbols", "semantic_search"}},
	{CapGit, "Git status and diff for commits, branches, merges, and working-tree review.", []string{"git_status", "git_diff"}},
	{CapVerify, "Project verification for tests, build, lint, typecheck, and local site checks.", []string{"verify", "verify_site"}},
	{CapMedia, "Image search for finding image references and downloadable assets.", []string{"imagesearch"}},
}

var capabilityTriggers = map[Capability][]string{
	CapWeb:      {"http://", "https://", "url", "browser", "page", "website", "web", "screenshot", "fetch", "search", "docs", "latest", "current"},
	CapSemantic: {"symbol", "definition", "reference", "references", "lsp", "semantic", "where is", "call graph"},
	CapGit:      {"git", "commit", "branch", "diff", "merge", "rebase", "stash", "checkout"},
	CapVerify:   {"verify", "verify site", "typecheck"},
	CapMedia:    {"image", "logo", "asset", "assets", "icon", "photo", "picture"},
}

// ResolveCapabilities chooses the initial capability set. The profile is a
// preset; "auto" starts with coding and adds capabilities when the prompt
// clearly needs them. Per-capability settings override the preset.
func ResolveCapabilities(profile, prompt string, settings map[string]string, orchestrator bool) []Capability {
	profile = strings.ToLower(strings.TrimSpace(profile))
	if profile == "" {
		profile = "auto"
	}
	auto := profile == "auto"
	if auto {
		profile = "coding"
	}
	caps := append([]Capability(nil), profileCapabilities[profile]...)
	if len(caps) == 0 {
		caps = append([]Capability(nil), profileCapabilities["coding"]...)
	}

	lower := strings.ToLower(prompt)
	if auto {
		for _, cap := range optionalCapabilities {
			if promptMatchesCapability(lower, cap) {
				caps = appendCapability(caps, cap)
			}
		}
	}
	for _, cap := range optionalCapabilities {
		switch strings.ToLower(strings.TrimSpace(settings[string(cap)])) {
		case "on", "true", "yes", "1":
			caps = appendCapability(caps, cap)
		case "off", "false", "no", "0":
			caps = removeCapability(caps, cap)
		case "auto":
			if auto && promptMatchesCapability(lower, cap) {
				caps = appendCapability(caps, cap)
			}
		}
	}
	if orchestrator && profile != "minimal" {
		caps = appendCapability(caps, CapOrchestrate)
	} else {
		caps = removeCapability(caps, CapOrchestrate)
	}
	return caps
}

func promptMatchesCapability(lower string, cap Capability) bool {
	for _, trigger := range capabilityTriggers[cap] {
		if strings.Contains(lower, trigger) {
			return true
		}
	}
	return false
}

func appendCapability(caps []Capability, cap Capability) []Capability {
	for _, c := range caps {
		if c == cap {
			return caps
		}
	}
	return append(caps, cap)
}

func removeCapability(caps []Capability, cap Capability) []Capability {
	out := caps[:0]
	for _, c := range caps {
		if c != cap {
			out = append(out, c)
		}
	}
	return out
}

// HasCapability reports whether the set contains cap.
func HasCapability(caps []Capability, cap Capability) bool {
	for _, c := range caps {
		if c == cap {
			return true
		}
	}
	return false
}

// FilterTools keeps only the tools in the chosen capabilities, plus MCP tools
// when keepMCP is set. The imagegen tool is never exposed by a profile.
func FilterTools(all []Tool, caps []Capability, interactive, keepMCP bool) []Tool {
	allowed := map[string]bool{}
	for _, cap := range caps {
		for _, name := range capabilityTools[cap] {
			allowed[name] = true
		}
	}
	full := HasCapability(caps, CapFull)
	var out []Tool
	for _, t := range all {
		if t == nil {
			continue
		}
		name := strings.ToLower(t.Name())
		if name == "imagegen" {
			continue
		}
		if name == "question" {
			if interactive {
				out = append(out, t)
			}
			continue
		}
		if isMCPTool(t) {
			if keepMCP {
				out = append(out, t)
			}
			continue
		}
		if full || allowed[name] {
			out = append(out, t)
		}
	}
	return out
}

func isMCPTool(t Tool) bool {
	name := strings.ToLower(t.Name())
	return strings.HasPrefix(name, "mcp__") || strings.HasPrefix(name, "mcp_")
}

// ToolsForCapability returns the tools in all that belong to cap. It is used
// by the runtime capability loader.
func ToolsForCapability(all []Tool, name string) ([]Tool, error) {
	cap := Capability(strings.ToLower(strings.TrimSpace(name)))
	if !isOptionalCapability(cap) {
		return nil, fmt.Errorf("unknown or non-loadable capability %q", name)
	}
	want := map[string]bool{}
	for _, tool := range capabilityTools[cap] {
		want[tool] = true
	}
	var out []Tool
	for _, t := range all {
		if t != nil && want[strings.ToLower(t.Name())] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("capability %q is unavailable", name)
	}
	return out, nil
}

func isOptionalCapability(cap Capability) bool {
	for _, c := range optionalCapabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// CapabilityNote is a compact capability index for the system prompt. It lists
// only capabilities that are not already loaded.
func CapabilityNote(caps []Capability) string {
	if HasCapability(caps, CapFull) {
		return ""
	}
	var missing []string
	for _, cap := range optionalCapabilities {
		if HasCapability(caps, cap) {
			continue
		}
		for _, match := range capabilityCatalog {
			if match.Name == cap {
				missing = append(missing, fmt.Sprintf("- %s: %s", cap, match.Description))
				break
			}
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "Optional capabilities (call request_capability; use tool_search if unsure):\n" + strings.Join(missing, "\n")
}

// SearchCapabilities returns compact capability matches for a query. It never
// returns schemas; the model must call request_capability to load one.
func SearchCapabilities(query string) []CapabilityMatch {
	query = strings.ToLower(strings.TrimSpace(query))
	var out []CapabilityMatch
	for _, match := range capabilityCatalog {
		score := 0
		if query == "" {
			score = 1
		} else if strings.Contains(string(match.Name), query) {
			score += 5
		}
		if query != "" {
			if strings.Contains(strings.ToLower(match.Description), query) {
				score += 3
			}
			for _, tool := range match.Tools {
				if strings.Contains(strings.ToLower(tool), query) {
					score += 2
				}
			}
		}
		if score > 0 {
			out = append(out, match)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

// CapabilityNames returns the loadable optional capability names.
func CapabilityNames() []string {
	out := make([]string, 0, len(optionalCapabilities))
	for _, cap := range optionalCapabilities {
		out = append(out, string(cap))
	}
	return out
}

// ProfileNames returns the supported profile names.
func ProfileNames() []string {
	out := make([]string, 0, len(profileCapabilities))
	for name := range profileCapabilities {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

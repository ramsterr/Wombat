package agents

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/tools"
)

// Member is a custom agent resolved against the live session: a concrete
// provider and model, the toolset it may use, its permission rules, and its
// composed persona. The orchestrator stamps a fresh worker from a member for
// every step the planner assigns to it.
type Member struct {
	// ID is the definition name used by the planner.
	ID string
	// Source records where the definition came from, for display.
	Source string
	// Def is the definition exactly as loaded.
	Def *config.AgentDef
	// Provider is the resolved provider. Nil means the lead's provider.
	Provider llm.Provider
	// Model is the resolved model. Empty means the lead's model.
	Model string
	// Tools is the filtered toolset the agent may call.
	Tools []tools.Tool
	// MaxSteps caps one run. Zero means the orchestrator default.
	MaxSteps int
	// ReadOnly blocks write, edit, and mutating bash.
	ReadOnly bool
	// PermRules holds normalized allow/deny decisions.
	PermRules map[string]string
	// Persona is the composed system-prompt section (prompt + skills).
	Persona string
}

// PrepareOptions is the live context needed to resolve members.
type PrepareOptions struct {
	Cfg          *config.Config
	Cwd          string
	Home         string
	BaseProvider llm.Provider
	BaseModel    string
	AllTools     []tools.Tool
	// ForceReadOnly makes every member read-only. The session uses it in
	// chat mode so selecting an agent can never widen write access.
	ForceReadOnly bool
	// Defs lets the caller reuse definitions it already loaded (the picker
	// loads them to render). Nil loads them here.
	Defs     []Loaded
	Selected []string
}

// Prepare resolves the selected agent ids into runnable members. Definitions
// that cannot be resolved are reported as problems and skipped so one broken
// agent cannot break the session.
func Prepare(opts PrepareOptions) ([]*Member, []Problem) {
	selected := normalizeSelection(opts.Selected)
	if len(selected) == 0 {
		return nil, nil
	}
	defs := opts.Defs
	if defs == nil {
		defs = Load(opts.Cfg, opts.Home, opts.Cwd)
	}
	byName := make(map[string]Loaded, len(defs))
	for _, loaded := range defs {
		if loaded.Def == nil {
			continue
		}
		byName[strings.ToLower(loaded.Def.Name)] = loaded
	}

	skills, skillProblems := LoadSkills(opts.Home, opts.Cwd)
	elsewhere := make([]Problem, 0, len(skillProblems))
	for _, problem := range skillProblems {
		elsewhere = append(elsewhere, Problem{Name: problem.Name, Source: problem.Source, Err: problem.Err})
	}

	var members []*Member
	var problems []Problem
	for _, name := range selected {
		loaded, ok := byName[name]
		if !ok {
			problems = append(problems, Problem{Name: name,
				Err: fmt.Errorf("unknown agent %q — create it with `wombat agent new %s`", name, name)})
			continue
		}
		if loaded.Err != nil {
			problems = append(problems, Problem{Name: loaded.Name(), Source: loaded.Source, Err: loaded.Err})
			continue
		}
		def := loaded.Def
		if opts.Cfg != nil && len(def.MCPServers) > 0 {
			var unknown []string
			for _, server := range def.MCPServers {
				if _, ok := opts.Cfg.MCPServers[server]; !ok {
					unknown = append(unknown, server)
				}
			}
			if len(unknown) > 0 {
				problems = append(problems, Problem{Name: def.Name, Source: loaded.Source,
					Err: fmt.Errorf("unknown MCP server(s): %s", strings.Join(unknown, ", "))})
				continue
			}
		}
		provider := opts.BaseProvider
		model := opts.BaseModel
		if def.Model != "" {
			model = def.Model
		}
		if def.Provider != "" {
			resolved, err := ResolveProvider(opts.Cfg, def.Provider)
			if err != nil {
				problems = append(problems, Problem{Name: def.Name, Source: loaded.Source,
					Err: fmt.Errorf("provider %q: %w", def.Provider, err)})
				continue
			}
			provider = resolved
			if def.Model == "" && opts.Cfg != nil {
				if pc := opts.Cfg.Providers[def.Provider]; pc != nil && len(pc.Models) > 0 {
					model = pc.Models[0]
				}
			}
		}
		enabled, missing := resolveSkills(def.Skills, skills)
		if len(missing) > 0 {
			for _, problem := range missing {
				problems = append(problems, Problem{Name: def.Name, Source: loaded.Source, Err: problem.Err})
			}
			continue
		}
		members = append(members, &Member{
			ID:        def.Name,
			Source:    loaded.Source,
			Def:       def,
			Provider:  provider,
			Model:     model,
			Tools:     FilterTools(opts.AllTools, def),
			MaxSteps:  def.MaxSteps,
			ReadOnly:  def.ReadOnly || opts.ForceReadOnly,
			PermRules: normalizePerms(def.Permissions),
			Persona:   Persona(def, enabled),
		})
	}
	return members, append(problems, elsewhere...)
}

// ResolveProvider builds the provider a definition names. It mirrors the
// session provider construction: stored key, preset env key, fallback
// models.
func ResolveProvider(cfg *config.Config, name string) (llm.Provider, error) {
	name = strings.TrimSpace(name)
	if cfg == nil {
		return nil, fmt.Errorf("no configuration loaded")
	}
	pc, ok := cfg.Providers[name]
	if !ok || pc == nil {
		return nil, fmt.Errorf("not configured")
	}
	prov, err := llm.New(name, pc, cfg.APIKey(name, pc))
	if err != nil {
		return nil, err
	}
	if len(cfg.FallbackModels) > 0 {
		prov = llm.WithFallback(prov, cfg.FallbackModels, nil)
	}
	return prov, nil
}

// AvailableNames lists the usable definition names in display order.
func AvailableNames(defs []Loaded) []string {
	names := make([]string, 0, len(defs))
	for _, loaded := range defs {
		if loaded.Def != nil && loaded.Err == nil {
			names = append(names, loaded.Def.Name)
		}
	}
	sort.Strings(names)
	return names
}

func normalizeSelection(selected []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, entry := range selected {
		for _, part := range strings.Split(entry, ",") {
			name := config.NormalizeAgentName(part)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

func normalizePerms(perms map[string]string) map[string]string {
	if len(perms) == 0 {
		return nil
	}
	out := make(map[string]string, len(perms))
	for kind, value := range perms {
		kind = strings.ToLower(strings.TrimSpace(kind))
		value = strings.ToLower(strings.TrimSpace(value))
		if kind == "" {
			continue
		}
		out[kind] = value
	}
	return out
}

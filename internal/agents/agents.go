// Package agents turns custom agent definitions into runnable team members.
// A member carries its own persona (prompt plus skills), tool allowlist,
// permission decisions, and optional provider and model overrides. The
// orchestrator stamps a fresh worker from a member whenever the planner
// assigns it a step.
package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ramsterr/Wombat/internal/config"
)

// maxPromptFileBytes caps a prompt_file or skill body so one file cannot
// swallow the context window.
const maxPromptFileBytes = 64 * 1024

// Loaded is one discovered agent definition, or the error that prevented
// loading it. Definitions are kept sorted by name. A definition loaded from
// the project replaces a same-named user definition, which replaces an
// inline config.json entry.
type Loaded struct {
	Def    *config.AgentDef
	Source string
	Err    error
}

// Name is the definition id, or a readable fallback for a broken file.
func (l Loaded) Name() string {
	if l.Def != nil {
		return l.Def.Name
	}
	if l.Source != "" {
		base := strings.TrimSuffix(filepath.Base(l.Source), filepath.Ext(l.Source))
		if slug := Slug(base); slug != "" {
			return slug
		}
	}
	return "(unnamed)"
}

// Problem describes a definition or a selection that cannot be resolved.
// The UI shows problems instead of silently dropping an agent.
type Problem struct {
	Name   string
	Source string
	Err    error
}

func (p Problem) String() string {
	label := strings.TrimSpace(p.Name)
	if label == "" {
		label = p.Source
	}
	if p.Name != "" && p.Source != "" {
		label += " (" + p.Source + ")"
	}
	return label + ": " + p.Err.Error()
}

// Load discovers every custom agent definition.
//
// Sources, in increasing precedence:
//  1. the Agents map in config.json
//  2. ~/.wombat/agents/*.json and *.md
//  3. <cwd>/.wombat/agents/*.json and *.md
//
// The returned slice includes unusable definitions with Err set so the UI
// can explain what is wrong.
func Load(cfg *config.Config, home, cwd string) []Loaded {
	byName := map[string]Loaded{}
	var problems []Loaded

	put := func(key string, l Loaded) {
		key = config.NormalizeAgentName(key)
		if key == "" {
			key = "!" + l.Source
		}
		byName[key] = l
	}

	if cfg != nil {
		names := make([]string, 0, len(cfg.Agents))
		for name := range cfg.Agents {
			names = append(names, name)
		}
		sort.Strings(names)
		base := filepath.Dir(config.Path())
		for _, key := range names {
			raw := cfg.Agents[key]
			if raw == nil {
				problems = append(problems, Loaded{Source: "config.json",
					Err: fmt.Errorf("agent %q has an empty definition", key)})
				continue
			}
			def := *raw
			if strings.TrimSpace(def.Name) == "" {
				def.Name = key
			}
			def.Name = config.NormalizeAgentName(def.Name)
			def.Source = "config.json"
			if err := resolvePrompt(&def, base); err != nil {
				put(def.Name, Loaded{Def: &def, Source: def.Source, Err: err})
				continue
			}
			if err := def.Validate(); err != nil {
				put(def.Name, Loaded{Def: &def, Source: def.Source, Err: err})
				continue
			}
			put(def.Name, Loaded{Def: &def, Source: def.Source})
		}
	}

	dirs := agentDirs(home, cwd)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				problems = append(problems, Loaded{Source: dir, Err: err})
			}
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if ext != ".json" && ext != ".md" {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			def, err := loadDefFile(path)
			if err != nil {
				problems = append(problems, Loaded{Source: path, Err: err})
				continue
			}
			if err := resolvePrompt(def, filepath.Dir(path)); err != nil {
				put(def.Name, Loaded{Def: def, Source: path, Err: err})
				continue
			}
			def.Source = path
			put(def.Name, Loaded{Def: def, Source: path})
		}
	}

	out := make([]Loaded, 0, len(byName)+len(problems))
	for _, l := range byName {
		out = append(out, l)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	sort.SliceStable(problems, func(i, j int) bool { return problems[i].Source < problems[j].Source })
	return append(out, problems...)
}

// DefDir is where a new definition is written: the project's
// .wombat/agents when the project opted into .wombat, otherwise the user's
// ~/.wombat/agents.
func DefDir(home, cwd string) string {
	if strings.TrimSpace(cwd) != "" {
		if info, err := os.Stat(filepath.Join(cwd, ".wombat")); err == nil && info.IsDir() {
			return filepath.Join(cwd, ".wombat", "agents")
		}
	}
	if strings.TrimSpace(home) == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, "agents")
}

// Usable returns only the definitions that loaded without error.
func Usable(loaded []Loaded) []*config.AgentDef {
	defs := make([]*config.AgentDef, 0, len(loaded))
	for _, l := range loaded {
		if l.Def != nil && l.Err == nil {
			defs = append(defs, l.Def)
		}
	}
	return defs
}

func agentDirs(home, cwd string) []string {
	var dirs []string
	if strings.TrimSpace(home) != "" {
		dirs = append(dirs, filepath.Join(home, "agents"))
	}
	if strings.TrimSpace(cwd) != "" {
		dirs = append(dirs, filepath.Join(cwd, ".wombat", "agents"))
	}
	return dirs
}

func loadDefFile(path string) (*config.AgentDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	fallback := Slug(base)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md":
		return config.ParseAgentMarkdown(data, fallback)
	case ".json":
		return config.ParseAgentJSON(data, fallback)
	default:
		return nil, fmt.Errorf("unsupported agent file type %q", filepath.Ext(path))
	}
}

// resolvePrompt fills Prompt from PromptFile. A relative path is resolved
// against the definition's own directory.
func resolvePrompt(def *config.AgentDef, baseDir string) error {
	if def == nil || strings.TrimSpace(def.Prompt) != "" || strings.TrimSpace(def.PromptFile) == "" {
		return nil
	}
	ref := strings.TrimSpace(def.PromptFile)
	if strings.HasPrefix(ref, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			ref = filepath.Join(home, ref[2:])
		}
	}
	if !filepath.IsAbs(ref) {
		ref = filepath.Join(baseDir, ref)
	}
	data, err := os.ReadFile(ref)
	if err != nil {
		return fmt.Errorf("prompt_file %s: %w", ref, err)
	}
	if len(data) > maxPromptFileBytes {
		data = append(data[:maxPromptFileBytes], []byte("\n…[truncated]")...)
	}
	def.Prompt = strings.TrimSpace(string(data))
	def.PromptFile = ""
	return nil
}

// Slug turns a filename or phrase into a lowercase id.
func Slug(value string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-_")
	if len(out) > config.MaxAgentNameLen {
		out = strings.Trim(out[:config.MaxAgentNameLen], "-_")
	}
	return out
}

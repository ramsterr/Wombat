package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/tools"
)

type fakeProvider struct{ name string }

func (p *fakeProvider) Name() string { return p.name }
func (p *fakeProvider) ListModels(context.Context) ([]string, error) {
	return []string{"mock"}, nil
}
func (p *fakeProvider) Chat(context.Context, llm.Request, func(string)) (*llm.Result, error) {
	return &llm.Result{}, nil
}

type fakeTool struct {
	name  string
	class string
}

func (f *fakeTool) Name() string           { return f.name }
func (f *fakeTool) Class() string          { return f.class }
func (f *fakeTool) Description() string    { return "fake tool" }
func (f *fakeTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (f *fakeTool) Run(context.Context, json.RawMessage, *tools.Env) (string, error) {
	return "", nil
}

type fakeMCPTool struct {
	fakeTool
	server string
}

func (f *fakeMCPTool) ServerName() string { return f.server }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findLoaded(defs []Loaded, name string) *Loaded {
	for i := range defs {
		if defs[i].Def != nil && defs[i].Def.Name == name {
			return &defs[i]
		}
	}
	return nil
}

func TestLoadPrecedenceAndSources(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)

	writeFile(t, filepath.Join(home, "agents", "math.md"),
		"---\nname: math\ndescription: user math\n---\nYou research math.\n")
	writeFile(t, filepath.Join(home, "agents", "notes.json"), `{"description":"user notes"}`)
	writeFile(t, filepath.Join(cwd, ".wombat", "agents", "math.json"),
		`{"name":"math","description":"project math"}`)

	cfg := &config.Config{Agents: map[string]*config.AgentDef{
		"math":   {Name: "math", Description: "config math"},
		"inline": {Description: "inline description"},
	}}
	defs := Load(cfg, home, cwd)

	math := findLoaded(defs, "math")
	if math == nil {
		t.Fatal("math agent missing")
	}
	if math.Def.Description != "project math" {
		t.Errorf("math description = %q, want the project definition to win", math.Def.Description)
	}
	if !strings.HasSuffix(math.Source, filepath.Join(".wombat", "agents", "math.json")) {
		t.Errorf("math source = %q", math.Source)
	}
	if defs := findLoaded(defs, "notes"); defs == nil {
		t.Error("notes.json should be loaded with its filename as name")
	}
	inline := findLoaded(defs, "inline")
	if inline == nil || inline.Def.Description != "inline description" {
		t.Errorf("inline config agent not loaded: %#v", inline)
	}
	if inline.Source != "config.json" {
		t.Errorf("inline source = %q", inline.Source)
	}
}

func TestLoadPromptFile(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	writeFile(t, filepath.Join(home, "agents", "writer.json"),
		`{"name":"writer","prompt_file":"prompts/writer.md","tools":["read"]}`)
	writeFile(t, filepath.Join(home, "agents", "prompts", "writer.md"), "Write clearly.\n")

	defs := Load(nil, home, cwd)
	writer := findLoaded(defs, "writer")
	if writer == nil || writer.Err != nil {
		t.Fatalf("writer not loaded: %#v", writer)
	}
	if writer.Def.Prompt != "Write clearly." {
		t.Errorf("prompt = %q", writer.Def.Prompt)
	}
}

func TestLoadReportsBrokenDefinitions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	writeFile(t, filepath.Join(home, "agents", "broken.json"), `{"tools": [`)
	writeFile(t, filepath.Join(home, "agents", "unclosed.md"), "---\nname: x\n")

	defs := Load(nil, home, t.TempDir())
	problems := 0
	for _, def := range defs {
		if def.Err != nil {
			problems++
		}
	}
	if problems != 2 {
		t.Fatalf("problems = %d, want 2 (%+v)", problems, defs)
	}
}

func TestLoadSkillsPrecedenceAndFrontmatter(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeFile(t, filepath.Join(home, "skills", "style.md"),
		"---\nname: style\ndescription: house style\n---\nUse short sentences.\n")
	writeFile(t, filepath.Join(home, "skills", "plain.md"), "# Plain\n\nDo the plain thing.\n")
	writeFile(t, filepath.Join(cwd, ".wombat", "skills", "style.md"),
		"---\ndescription: project style\n---\nUse the project voice.\n")

	skills, problems := LoadSkills(home, cwd)
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if len(skills) != 2 {
		t.Fatalf("skills = %#v", skills)
	}
	var style, plain *Skill
	for i := range skills {
		switch skills[i].Name {
		case "style":
			style = &skills[i]
		case "plain":
			plain = &skills[i]
		}
	}
	if style == nil || !strings.Contains(style.Prompt, "project voice") {
		t.Fatalf("project skill should win: %#v", style)
	}
	if plain == nil || plain.Description != "Plain" {
		t.Fatalf("plain skill = %#v", plain)
	}
	if !strings.Contains(plain.Prompt, "plain thing") {
		t.Fatalf("plain prompt = %q", plain.Prompt)
	}
}

func TestFilterTools(t *testing.T) {
	all := []tools.Tool{
		&fakeTool{name: "read", class: "read"},
		&fakeTool{name: "bash", class: "bash"},
		&fakeTool{name: "write", class: "write"},
		&fakeTool{name: "websearch", class: "network"},
		&fakeTool{name: "update_plan", class: "plan"},
		&fakeTool{name: "question", class: "read"},
		&fakeMCPTool{fakeTool: fakeTool{name: "mcp__github__search", class: "read"}, server: "github"},
		&fakeMCPTool{fakeTool: fakeTool{name: "mcp__slack__post", class: "write"}, server: "slack"},
	}

	got := names(FilterTools(all, &config.AgentDef{
		Name:         "x",
		Tools:        []string{"class:read", "bash", "mcp:github"},
		ExcludeTools: []string{"write"},
	}))
	want := []string{"read", "bash", "mcp__github__search"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("filtered = %v, want %v", got, want)
	}

	// An MCP server allowlist keeps builtins and only that server.
	got = names(FilterTools(all, &config.AgentDef{Name: "x", MCPServers: []string{"slack"}}))
	want = []string{"read", "bash", "write", "websearch", "mcp__slack__post"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("mcp filter = %v, want %v", got, want)
	}

	// Globs match tool names.
	got = names(FilterTools(all, &config.AgentDef{Name: "x", Tools: []string{"mcp__github__*", "web*"}}))
	want = []string{"websearch", "mcp__github__search"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("glob filter = %v, want %v", got, want)
	}

	// The read-only flag is enforced by the environment, not by silently
	// changing the tool list.
	got = names(FilterTools(all, &config.AgentDef{Name: "x", Tools: []string{"read", "write"}}))
	if strings.Join(got, ",") != "read,write" {
		t.Fatalf("tools = %v", got)
	}
}

func TestWorkerPermit(t *testing.T) {
	if WorkerPermit(nil, "x") != nil {
		t.Fatal("empty rules must keep the default worker behavior")
	}
	permit := WorkerPermit(map[string]string{"write": "deny", "dangerous": "allow", "bash": "allow"}, "math")
	if d := permit("write", "a.txt"); d.Allow {
		t.Error("write should be denied")
	}
	if d := permit("dangerous", "rm -rf /"); !d.Allow {
		t.Error("explicit dangerous allow should win")
	}
	if d := permit("mcp:github", "x"); !d.Allow {
		t.Error("unspecified kinds default to allow for workers")
	}
	if d := permit("sensitive-read", "~/.ssh/id_rsa"); d.Allow {
		t.Error("sensitive reads must stay denied by default")
	}
	permit = WorkerPermit(map[string]string{"sensitive-read": "allow"}, "x")
	if d := permit("sensitive-read", "a"); !d.Allow {
		t.Error("explicit sensitive read allow should win")
	}
}

func TestPrepareResolvesMembersAndProblems(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	writeFile(t, filepath.Join(home, "skills", "proofs.md"),
		"---\ndescription: careful proofs\n---\nJustify each step.\n")
	writeFile(t, filepath.Join(cwd, ".wombat", "agents", "math.md"), `---
name: math
description: math specialist
provider: alt
model: reasoning-model
tools: read, grep
skills: proofs
---
You verify mathematics.
`)
	writeFile(t, filepath.Join(cwd, ".wombat", "agents", "bad-skill.json"),
		`{"name":"bad-skill","skills":["does-not-exist"]}`)

	cfg := &config.Config{
		Providers: map[string]*config.Provider{
			"alt": {Kind: "openai", BaseURL: "http://127.0.0.1:9/v1"},
		},
	}
	all := []tools.Tool{
		&fakeTool{name: "read", class: "read"},
		&fakeTool{name: "grep", class: "read"},
		&fakeTool{name: "bash", class: "bash"},
	}
	members, problems := Prepare(PrepareOptions{
		Cfg:          cfg,
		Cwd:          cwd,
		Home:         home,
		BaseProvider: &fakeProvider{name: "lead"},
		BaseModel:    "lead-model",
		AllTools:     all,
		Selected:     []string{"math", "bad-skill", "missing"},
	})
	if len(members) != 1 {
		t.Fatalf("members = %#v", members)
	}
	m := members[0]
	if m.ID != "math" || m.Provider.Name() != "alt" || m.Model != "reasoning-model" {
		t.Fatalf("member = %+v", m)
	}
	if strings.Join(names(m.Tools), ",") != "read,grep" {
		t.Fatalf("tools = %v", names(m.Tools))
	}
	if !strings.Contains(m.Persona, "You verify mathematics") || !strings.Contains(m.Persona, "Justify each step") {
		t.Fatalf("persona = %q", m.Persona)
	}
	if len(problems) != 2 {
		t.Fatalf("problems = %#v", problems)
	}
	var sawSkill, sawMissing bool
	for _, p := range problems {
		if strings.Contains(p.Err.Error(), "unknown skill") {
			sawSkill = true
		}
		if strings.Contains(p.Err.Error(), "unknown agent") {
			sawMissing = true
		}
	}
	if !sawSkill || !sawMissing {
		t.Fatalf("problems = %#v", problems)
	}
}

func TestPrepareWithoutOverrideUsesLeadModel(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	writeFile(t, filepath.Join(home, "agents", "design.md"),
		"---\nname: design\ndescription: deck design\n---\nYou design decks.\n")

	lead := &fakeProvider{name: "lead"}
	members, problems := Prepare(PrepareOptions{
		Cfg:          &config.Config{},
		Cwd:          cwd,
		Home:         home,
		BaseProvider: lead,
		BaseModel:    "lead-model",
		AllTools:     []tools.Tool{&fakeTool{name: "read", class: "read"}},
		Selected:     []string{"design"},
	})
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if len(members) != 1 {
		t.Fatalf("members = %#v", members)
	}
	if members[0].Provider != lead || members[0].Model != "lead-model" {
		t.Fatalf("member = %+v", members[0])
	}
}

func TestPrepareForceReadOnlyAndMCPValidation(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	writeFile(t, filepath.Join(home, "agents", "writer.md"),
		"---\nname: writer\ndescription: writes docs\n---\nWrite clearly.\n")
	writeFile(t, filepath.Join(home, "agents", "ghost-mcp.json"),
		`{"name":"ghost-mcp","mcp_servers":["ghost"]}`)

	members, problems := Prepare(PrepareOptions{
		Cfg:           &config.Config{},
		Cwd:           cwd,
		Home:          home,
		BaseProvider:  &fakeProvider{name: "lead"},
		BaseModel:     "lead-model",
		Selected:      []string{"writer", "ghost-mcp"},
		ForceReadOnly: true,
	})
	if len(members) != 1 || members[0].ID != "writer" {
		t.Fatalf("members = %#v", members)
	}
	if !members[0].ReadOnly {
		t.Error("chat mode must force custom agents read-only")
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Err.Error(), "unknown MCP server") {
		t.Fatalf("problems = %#v", problems)
	}
}

func TestPrepareProviderUsesFirstConfiguredModel(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	writeFile(t, filepath.Join(home, "agents", "alt.md"),
		"---\nname: alt\nprovider: alt-provider\n---\nAlt.\n")

	cfg := &config.Config{
		Providers: map[string]*config.Provider{
			"alt-provider": {Kind: "openai", BaseURL: "http://127.0.0.1:9/v1", Models: []string{"alt-small", "alt-large"}},
		},
	}
	members, problems := Prepare(PrepareOptions{
		Cfg: cfg, Cwd: cwd, Home: home,
		BaseModel: "lead-model",
		Selected:  []string{"alt"},
	})
	if len(problems) != 0 || len(members) != 1 {
		t.Fatalf("members = %#v problems = %#v", members, problems)
	}
	if members[0].Model != "alt-small" {
		t.Fatalf("model = %q, want the provider's first configured model", members[0].Model)
	}
}

func TestDefDir(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	if got := DefDir(home, project); got != filepath.Join(home, "agents") {
		t.Fatalf("DefDir without .wombat = %q", got)
	}
	if err := os.MkdirAll(filepath.Join(project, ".wombat"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DefDir(home, project); got != filepath.Join(project, ".wombat", "agents") {
		t.Fatalf("DefDir with .wombat = %q", got)
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Math Research.md": "math-research-md",
		"Deck  Design":     "deck-design",
		"__x__":            "x",
		"":                 "",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func names(list []tools.Tool) []string {
	out := make([]string, 0, len(list))
	for _, t := range list {
		out = append(out, t.Name())
	}
	return out
}

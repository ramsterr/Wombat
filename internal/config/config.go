package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Provider struct {
	Kind    string   `json:"kind"`
	BaseURL string   `json:"base_url"`
	APIKey  string   `json:"api_key,omitempty"`
	Models  []string `json:"models,omitempty"`
	NumCtx  int      `json:"num_ctx,omitempty"`
}

const (
	DefaultMaxDelegations        = 0
	DefaultMaxParallelScouts     = 5
	DefaultMaxParallelAgents     = 5
	DefaultWorkerMaxSteps        = 16
	DefaultWorkerTokenBudget     = 0
	DefaultTurnWorkerTokenBudget = 0
	DefaultMaxParallelWriters    = 2
	DefaultWriterMaxSteps        = 40
	DefaultWriterTokenBudget     = 0
	DefaultTurnWriterTokenBudget = 0
)

type Config struct {
	Version               int                   `json:"version"`
	Provider              string                `json:"provider"`
	Model                 string                `json:"model"`
	ImageProvider         string                `json:"image_provider,omitempty"`
	ImageModel            string                `json:"image_model,omitempty"`
	EmbeddingProvider     string                `json:"embedding_provider,omitempty"`
	EmbeddingModel        string                `json:"embedding_model,omitempty"`
	ReviewWriters         *bool                 `json:"review_writers,omitempty"`
	ReviewModel           string                `json:"review_model,omitempty"`
	CheckPolicy           string                `json:"check_policy,omitempty"`
	ReviewPolicy          string                `json:"review_policy,omitempty"`
	WorkerFullAccess      *bool                 `json:"worker_full_access,omitempty"`
	Providers             map[string]*Provider  `json:"providers"`
	MCPServers            map[string]*MCPServer `json:"mcp_servers,omitempty"`
	Agents                map[string]*AgentDef  `json:"agents,omitempty"`
	Permissions           map[string]string     `json:"permissions,omitempty"`
	Orchestrator          bool                  `json:"orchestrator"`
	MaxParallelScouts     int                   `json:"max_parallel_scouts"`
	MaxParallelAgents     int                   `json:"max_parallel_agents,omitempty"`
	MaxParallelWriters    int                   `json:"max_parallel_writers"`
	WorkerMaxSteps        int                   `json:"worker_max_steps,omitempty"`
	WorkerTokenBudget     int                   `json:"worker_token_budget,omitempty"`
	TurnWorkerTokenBudget int                   `json:"turn_worker_token_budget,omitempty"`
	WriterMaxSteps        int                   `json:"writer_max_steps,omitempty"`
	WriterTokenBudget     int                   `json:"writer_token_budget,omitempty"`
	TurnWriterTokenBudget int                   `json:"turn_writer_token_budget,omitempty"`
	Mode                  string                `json:"mode"`
	Reasoning             string                `json:"reasoning,omitempty"`
	ShowThinking          bool                  `json:"show_thinking"`
	BrowserScreenshots    bool                  `json:"browser_screenshots,omitempty"`
	TrimReasoning         bool                  `json:"trim_reasoning,omitempty"`
	ParallelAgents        bool                  `json:"parallel_agents"`
	ScoutModel            string                `json:"scout_model,omitempty"`
	FallbackModels        []string              `json:"fallback_models,omitempty"`
	Checkpoints           bool                  `json:"checkpoints"`
	RequireVerification   bool                  `json:"require_verification"`
	Writers               bool                  `json:"writers"`
	KeepWorktrees         bool                  `json:"keep_worktrees,omitempty"`
	Mouse                 bool                  `json:"mouse,omitempty"`
	Theme                 string                `json:"theme,omitempty"`
	Panel                 bool                  `json:"panel,omitempty"`
	PaintBackground       bool                  `json:"paint_background,omitempty"`
}

type Preset struct {
	Name   string
	Kind   string
	Base   string
	KeyEnv string
	Local  bool
}

var Presets = []Preset{
	{Name: "openai", Kind: "openai", Base: "https://api.openai.com/v1", KeyEnv: "OPENAI_API_KEY"},
	{Name: "anthropic", Kind: "anthropic", Base: "https://api.anthropic.com", KeyEnv: "ANTHROPIC_API_KEY"},
	{Name: "openrouter", Kind: "openai", Base: "https://openrouter.ai/api/v1", KeyEnv: "OPENROUTER_API_KEY"},
	{Name: "deepseek", Kind: "openai", Base: "https://api.deepseek.com/v1", KeyEnv: "DEEPSEEK_API_KEY"},
	{Name: "groq", Kind: "openai", Base: "https://api.groq.com/openai/v1", KeyEnv: "GROQ_API_KEY"},
	{Name: "xai", Kind: "openai", Base: "https://api.x.ai/v1", KeyEnv: "XAI_API_KEY"},
	{Name: "mistral", Kind: "openai", Base: "https://api.mistral.ai/v1", KeyEnv: "MISTRAL_API_KEY"},
	{Name: "opencode", Kind: "opencode", Base: "https://opencode.ai/inference", KeyEnv: "OPENCODE_API_KEY"},
	{Name: "opencode-go", Kind: "opencode", Base: "https://opencode.ai/inference/go", KeyEnv: "OPENCODE_API_KEY"},
	{Name: "ollama", Kind: "ollama", Base: "http://127.0.0.1:11434", Local: true},
	{Name: "lmstudio", Kind: "openai", Base: "http://127.0.0.1:1234/v1", Local: true},
	{Name: "llamacpp", Kind: "openai", Base: "http://127.0.0.1:9931/v1", Local: true},
	{Name: "vllm", Kind: "openai", Base: "http://127.0.0.1:8000/v1", Local: true},
	{Name: "mlx", Kind: "openai", Base: "http://127.0.0.1:8080/v1", Local: true},
}

func PresetByName(name string) (Preset, bool) {
	for _, p := range Presets {
		if p.Name == name {
			return p, true
		}
	}
	return Preset{}, false
}

// TmpDir is the scratch directory for a session. It lives outside the
// project so scratch files never pollute the repository and never end up in
// a checkpoint.
func TmpDir(sessionID string) string {
	dir := filepath.Join(Home(), "tmp")
	if sessionID != "" {
		dir = filepath.Join(dir, sessionID)
	}
	return dir
}

func Home() string {
	if h := os.Getenv("WOMBAT_HOME"); h != "" {
		return h
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return ".wombat"
	}
	return filepath.Join(dir, ".wombat")
}

func Path() string {
	if p := os.Getenv("WOMBAT_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(Home(), "config.json")
}

func Defaults() *Config {
	c := &Config{
		Version:   3,
		Provider:  "openai",
		Model:     "",
		Providers: map[string]*Provider{},
		// Simple by default: the agent works without stopping to ask. Set a
		// class back to "ask" to be consulted before it is used.
		Permissions: map[string]string{
			"bash":           "allow",
			"write":          "allow",
			"edit":           "allow",
			"network":        "allow",
			"mcp":            "ask",
			"dangerous":      "ask",
			"sensitive-read": "ask",
		},
		MCPServers:          map[string]*MCPServer{},
		Orchestrator:        true,
		MaxParallelScouts:   DefaultMaxParallelScouts,
		MaxParallelAgents:   DefaultMaxParallelAgents,
		MaxParallelWriters:  DefaultMaxParallelWriters,
		Mode:                "create",
		ParallelAgents:      true,
		Checkpoints:         true,
		RequireVerification: false,
		Writers:             true,
		Mouse:               false,
		BrowserScreenshots:  true,
		Theme:               "wombat",
		Panel:               true,
		PaintBackground:     true,
	}
	for _, p := range Presets {
		c.Providers[p.Name] = &Provider{Kind: p.Kind, BaseURL: p.Base}
	}
	if p, ok := PresetByName("ollama"); ok {
		c.Providers[p.Name].NumCtx = 32768
	}
	return c
}

func Load() (*Config, error) {
	raw, err := os.ReadFile(Path())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Defaults(), nil
		}
		return nil, err
	}
	c := Defaults()
	if err := json.Unmarshal(raw, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Path(), err)
	}
	// A config written before the simple defaults still carries the old
	// "ask" permissions and the removed keys. Move it forward so the agent
	// actually works, without touching the user's model or provider setup.
	if c.Version < 2 {
		c.Permissions = Defaults().Permissions
	}
	// Version 3 turns mouse reporting off by default so terminal text
	// selection works out of the box. /mouse turns wheel reporting back on.
	if c.Version < 3 {
		c.Mouse = false
		c.Version = 3
	}
	if c.Providers == nil {
		c.Providers = map[string]*Provider{}
	}
	if c.MCPServers == nil {
		c.MCPServers = map[string]*MCPServer{}
	}
	for _, p := range Presets {
		if _, ok := c.Providers[p.Name]; !ok {
			c.Providers[p.Name] = &Provider{Kind: p.Kind, BaseURL: p.Base}
		}
	}
	if c.MaxParallelScouts <= 0 {
		c.MaxParallelScouts = DefaultMaxParallelScouts
	}
	if c.MaxParallelWriters <= 0 {
		c.MaxParallelWriters = DefaultMaxParallelWriters
	}
	if c.MaxParallelAgents <= 0 {
		c.MaxParallelAgents = DefaultMaxParallelAgents
	}
	if strings.TrimSpace(c.Mode) == "" {
		c.Mode = "create"
	}
	return c, nil
}

// FullWorkers reports whether delegated workers get write/network/all-tool
// access. It defaults to true.
func (c *Config) FullWorkers() bool {
	return c == nil || c.WorkerFullAccess == nil || *c.WorkerFullAccess
}

func (c *Config) Save() error {
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (c *Config) Active() (name string, p *Provider, err error) {
	name = c.Provider
	if env := os.Getenv("WOMBAT_PROVIDER"); env != "" {
		name = env
	}
	if name == "" {
		return "", nil, errors.New("no provider selected: run `wombat setup`")
	}
	p, ok := c.Providers[name]
	if !ok {
		return "", nil, fmt.Errorf("unknown provider %q: run `wombat setup`", name)
	}
	return name, p, nil
}

func (c *Config) ActiveModel() string {
	if env := os.Getenv("WOMBAT_MODEL"); env != "" {
		return env
	}
	return c.Model
}

func (c *Config) APIKey(name string, p *Provider) string {
	if p.APIKey != "" {
		return p.APIKey
	}
	if preset, ok := PresetByName(name); ok && preset.KeyEnv != "" {
		if v := os.Getenv(preset.KeyEnv); v != "" {
			return v
		}
	}
	if env := os.Getenv(strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_API_KEY"); env != "" {
		return env
	}
	switch p.Kind {
	case "anthropic":
		return os.Getenv("ANTHROPIC_API_KEY")
	case "openai":
		return os.Getenv("OPENAI_API_KEY")
	default:
		return ""
	}
}

func (c *Config) Permission(kind string) string {
	if v, ok := c.Permissions[kind]; ok {
		return v
	}
	return "ask"
}

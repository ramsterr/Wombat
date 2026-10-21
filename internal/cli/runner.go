package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/checkpoint"
	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/images"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/mode"
	"github.com/ramsterr/Wombat/internal/orchestrator"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tokens"
	"github.com/ramsterr/Wombat/internal/tools"
	"github.com/ramsterr/Wombat/internal/trace"
)

type runOpts struct {
	model    string
	provider string
	yolo     bool
	quiet    bool

	// mode overrides the configured mode for this session when modeSet is
	// true (the /mode command).
	mode    mode.Mode
	modeSet bool
}

func BuildProvider(ctx context.Context, cfg *config.Config, name, modelOverride string) (llm.Provider, string, string, error) {
	return buildProvider(ctx, cfg, name, modelOverride)
}

func buildProvider(ctx context.Context, cfg *config.Config, name, modelOverride string) (llm.Provider, string, string, error) {
	if name == "" {
		name = cfg.Provider
	}
	if env := os.Getenv("WOMBAT_PROVIDER"); env != "" && name == "" {
		name = env
	}
	if name == "" {
		return nil, "", "", fmt.Errorf("no provider configured: run `wombat setup`")
	}
	pc, ok := cfg.Providers[name]
	if !ok {
		return nil, "", "", fmt.Errorf("provider %q not found in config: run `wombat setup`", name)
	}
	key := cfg.APIKey(name, pc)
	prov, err := llm.New(name, pc, key)
	if err != nil {
		return nil, "", "", err
	}
	if len(cfg.FallbackModels) > 0 {
		prov = llm.WithFallback(prov, cfg.FallbackModels, nil)
	}
	model := modelOverride
	if model == "" {
		model = cfg.ActiveModel()
	}
	if model == "" {
		return nil, "", "", fmt.Errorf("no model selected: run `wombat setup` or pass -m")
	}
	return prov, name, model, nil
}

func buildAgent(cfg *config.Config, opts runOpts, sess *session.Session, p *Prompter) (*agent.Agent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	providerName := opts.provider
	model := opts.model
	if providerName == "" && sess != nil && sess.Meta.Provider != "" {
		if _, ok := cfg.Providers[sess.Meta.Provider]; ok {
			providerName = sess.Meta.Provider
		}
	}
	if model == "" && sess != nil && sess.Meta.Model != "" {
		model = sess.Meta.Model
	}
	prov, providerName, model, err := buildProvider(ctx, cfg, providerName, model)
	if err != nil {
		return nil, err
	}
	pc := cfg.Providers[providerName]
	if sess.Meta.Provider == "" {
		sess.Meta.Provider = providerName
		sess.Meta.Model = model
	}
	cwd := sess.Meta.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
		sess.Meta.Cwd = cwd
	}
	sessionMode, err := mode.Parse(cfg.Mode)
	if err != nil {
		return nil, err
	}
	if opts.modeSet {
		sessionMode = opts.mode
	}
	env := &tools.Env{
		Dir:           cwd,
		Permit:        p.Permit,
		PermitPreview: p.PermitPreview,
		Mode:          sessionMode.Label(),
		DenyMutate:    !sessionMode.AllowsWrite(),
	}
	if p != nil {
		env.AskUser = func(q tools.Question) (string, error) {
			return p.AskUser(q)
		}
	}
	env.Images = imageClient(cfg)
	if cfg.Checkpoints {
		cp, err := checkpoint.New(checkpoint.RootForSession(sess.Path()), cwd)
		if err != nil {
			return nil, err
		}
		env.Checkpoint = cp
	}
	ag := &agent.Agent{
		Provider:      prov,
		Model:         model,
		Tools:         tools.RegistryWithQuestion(),
		Env:           env,
		Sess:          sess,
		Compact:       true,
		ContextWindow: tokens.Window(model),
		NumCtx:        pc.NumCtx,
		Reasoning:     cfg.Reasoning,
		ModeNote:      sessionMode.Note(),
		TrimReasoning: cfg.TrimReasoning,
	}
	if tr, err := trace.Open(strings.TrimSuffix(sess.Path(), ".jsonl") + ".events.jsonl"); err == nil {
		ag.Trace = tr
	}
	if cfg.Orchestrator {
		if _, err := orchestrator.Attach(ag, orchestrator.Config{
			MaxParallelScouts:   cfg.MaxParallelScouts,
			ScoutModel:          cfg.ScoutModel,
			ParallelAgents:      cfg.ParallelAgents,
			MaxParallelWriters:  cfg.MaxParallelWriters,
			RequireVerification: cfg.RequireVerification,
			EnableWriters:       cfg.Writers,
			KeepWorktrees:       cfg.KeepWorktrees,
		}); err != nil {
			return nil, err
		}
	}
	return ag, nil
}

func imageClient(cfg *config.Config) *images.Client {
	name := cfg.ImageProvider
	if env := os.Getenv("WOMBAT_IMAGE_PROVIDER"); env != "" {
		name = env
	}
	if name == "" {
		name = "openai"
	}
	pc, ok := cfg.Providers[name]
	if !ok {
		return nil
	}
	base := pc.BaseURL
	if env := os.Getenv("WOMBAT_IMAGE_BASE_URL"); env != "" {
		base = env
	}
	key := cfg.APIKey(name, pc)
	if env := os.Getenv("WOMBAT_IMAGE_API_KEY"); env != "" {
		key = env
	}
	model := cfg.ImageModel
	if env := os.Getenv("WOMBAT_IMAGE_MODEL"); env != "" {
		model = env
	}
	if key == "" && strings.Contains(base, "api.openai.com") {
		return nil
	}
	return images.New(images.Config{BaseURL: base, APIKey: key, Model: model})
}

func probeEndpoint(ctx context.Context, kind, base string) ([]string, error) {
	client := &http.Client{Timeout: 900 * time.Millisecond}
	var url string
	if kind == "ollama" {
		url = strings.TrimRight(base, "/") + "/api/tags"
	} else {
		url = strings.TrimRight(base, "/") + "/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	type tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	type oai struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	var names []string
	if kind == "ollama" {
		var t tags
		if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
			return nil, err
		}
		for _, m := range t.Models {
			names = append(names, m.Name)
		}
	} else {
		var o oai
		if err := json.NewDecoder(resp.Body).Decode(&o); err != nil {
			return nil, err
		}
		for _, m := range o.Data {
			names = append(names, m.ID)
		}
	}
	return names, nil
}

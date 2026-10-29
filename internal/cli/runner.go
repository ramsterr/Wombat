package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/agents"
	"github.com/ramsterr/Wombat/internal/checkpoint"
	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/embeddings"
	"github.com/ramsterr/Wombat/internal/images"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/mcp"
	"github.com/ramsterr/Wombat/internal/mode"
	"github.com/ramsterr/Wombat/internal/orchestrator"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/todo"
	"github.com/ramsterr/Wombat/internal/tokens"
	"github.com/ramsterr/Wombat/internal/tools"
	"github.com/ramsterr/Wombat/internal/trace"
)

type runOpts struct {
	model    string
	provider string
	// agents lists the custom agents selected for this run.
	agents []string
	yolo   bool
	quiet  bool

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
		Dir:            cwd,
		Permit:         p.Permit,
		PermitPreview:  p.PermitPreview,
		Mode:           sessionMode.Label(),
		DenyMutate:     !sessionMode.AllowsWrite(),
		Todos:          todo.New(todo.PathForSession(sess.Path())),
		SessionStarted: time.Now(),
	}
	if p != nil {
		env.AskUser = func(q tools.Question) (string, error) {
			return p.AskUser(q)
		}
	}
	env.Images = imageClient(cfg)
	env.Embeddings = embeddings.FromConfig(cfg)
	env.IndexDir = filepath.Join(config.Home(), "index")
	if cfg.Checkpoints {
		cp, err := checkpoint.New(checkpoint.RootForSession(sess.Path()), cwd)
		if err != nil {
			return nil, err
		}
		env.Checkpoint = cp
	}
	allTools := tools.RegistryWithQuestion()
	if len(cfg.MCPServers) > 0 {
		manager := mcp.Connect(context.Background(), cfg.MCPServers, cwd, Version)
		env.SetMCPManager(manager)
		allTools = append(allTools, tools.MCPTools(manager)...)
		for _, server := range manager.Servers() {
			if err := server.Err(); err != nil {
				fmt.Fprintf(os.Stderr, "%s\n", dim("mcp "+server.Name()+": "+tools.RedactSecrets(err.Error())))
			}
		}
	}
	selectedAgents := splitAgentFlags(opts.agents)
	if len(selectedAgents) == 0 && sess != nil {
		selectedAgents = append([]string(nil), sess.Meta.Agents...)
	}
	team, agentProblems, err := prepareTeam(cfg, cwd, prov, model, allTools, selectedAgents, !sessionMode.AllowsWrite())
	if err != nil {
		env.CloseMCP()
		return nil, err
	}
	for _, problem := range agentProblems {
		fmt.Fprintf(os.Stderr, "%s\n", dim("agent: "+problem.String()))
	}
	if len(opts.agents) > 0 && sess != nil {
		sess.Meta.Agents = selectedAgents
		_ = sess.Flush()
	}
	ag := &agent.Agent{
		Provider:      prov,
		Model:         model,
		Tools:         allTools,
		Env:           env,
		Sess:          sess,
		Compact:       true,
		ContextWindow: tokens.WindowFor(providerName, model),
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
			MaxParallelScouts:     cfg.MaxParallelScouts,
			MaxParallelAgents:     cfg.MaxParallelAgents,
			Team:                  team,
			ScoutModel:            cfg.ScoutModel,
			ParallelAgents:        cfg.ParallelAgents,
			MaxParallelWriters:    cfg.MaxParallelWriters,
			WorkerMaxSteps:        cfg.WorkerMaxSteps,
			WorkerTokenBudget:     cfg.WorkerTokenBudget,
			TurnWorkerTokenBudget: cfg.TurnWorkerTokenBudget,
			WriterMaxSteps:        cfg.WriterMaxSteps,
			WriterTokenBudget:     cfg.WriterTokenBudget,
			TurnWriterTokenBudget: cfg.TurnWriterTokenBudget,
			RequireVerification:   cfg.RequireVerification,
			EnableWriters:         cfg.Writers,
			KeepWorktrees:         cfg.KeepWorktrees,
			ReviewWriters:         cfg.ReviewWriters,
			ReviewModel:           cfg.ReviewModel,
			CheckPolicy:           cfg.CheckPolicy,
			ReviewPolicy:          cfg.ReviewPolicy,
			FullWorkers:           cfg.FullWorkers(),
		}); err != nil {
			env.CloseMCP()
			return nil, err
		}
	}
	return ag, nil
}

// splitAgentFlags splits repeatable --agents values into individual names.
func splitAgentFlags(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// prepareTeam resolves the selected custom agents for a headless run. A
// selected agent that cannot be prepared is a hard error: the user asked for
// it explicitly, so silently running without it would be misleading.
func prepareTeam(cfg *config.Config, cwd string, prov llm.Provider, model string, allTools []tools.Tool, selected []string, forceReadOnly bool) ([]*agents.Member, []agents.Problem, error) {
	if len(selected) == 0 {
		return nil, nil, nil
	}
	home := config.Home()
	defs := agents.Load(cfg, home, cwd)
	members, problems := agents.Prepare(agents.PrepareOptions{
		Cfg:           cfg,
		Cwd:           cwd,
		Home:          home,
		BaseProvider:  prov,
		BaseModel:     model,
		AllTools:      allTools,
		Defs:          defs,
		Selected:      selected,
		ForceReadOnly: forceReadOnly,
	})
	want := map[string]bool{}
	for _, name := range selected {
		for _, part := range strings.Split(name, ",") {
			name := config.NormalizeAgentName(part)
			if name != "" {
				want[name] = true
			}
		}
	}
	got := map[string]bool{}
	for _, member := range members {
		got[strings.ToLower(member.ID)] = true
	}
	var missing []string
	for name := range want {
		if !got[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		details := make([]string, 0, len(problems))
		for _, problem := range problems {
			details = append(details, problem.String())
		}
		if len(details) == 0 {
			details = append(details, "define it with `wombat agent new "+missing[0]+"`")
		}
		return nil, nil, fmt.Errorf("could not prepare custom agent(s) %s: %s",
			strings.Join(missing, ", "), strings.Join(details, "; "))
	}
	return members, problems, nil
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

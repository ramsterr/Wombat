package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/mode"
	"github.com/ramsterr/Wombat/internal/tools"
)

func cmdConfig(args []string) error {
	cfg := mustConfig()
	if len(args) == 0 {
		fmt.Println(dim(config.Path()))
		return printConfig(cfg)
	}
	if args[0] != "set" {
		return fmt.Errorf("usage: wombat config [set <key> <value>]")
	}
	if len(args) < 3 {
		return fmt.Errorf("usage: wombat config set <key> <value>\nkeys: provider, model, image_provider, image_model, embedding_provider, embedding_model, review_writers, review_model, check_policy, review_policy, worker_full_access, mode, tool_profile, research_mode, capabilities.<web|semantic|git|verify|media|mcp>, parallel_agents, scout_model, fallback_models, reasoning, show_thinking, orchestrator, checkpoints, require_verification, writers, keep_worktrees, max_parallel_scouts, max_parallel_agents, max_parallel_writers, worker_max_steps, worker_token_budget, turn_worker_token_budget, writer_max_steps, writer_token_budget, turn_writer_token_budget, permissions.<bash|write|edit|outside|network|dangerous|sensitive-read|mcp|mcp:server>")
	}
	key, val := args[1], strings.Join(args[2:], " ")
	switch {
	case key == "provider":
		if _, ok := cfg.Providers[val]; !ok {
			return fmt.Errorf("provider %q not in config: run `wombat setup`", val)
		}
		cfg.Provider = val
	case key == "model":
		cfg.Model = val
	case key == "image_provider":
		if _, ok := cfg.Providers[val]; !ok {
			return fmt.Errorf("provider %q not in config: run `wombat setup`", val)
		}
		cfg.ImageProvider = val
	case key == "image_model":
		cfg.ImageModel = val
	case key == "embedding_provider":
		if _, ok := cfg.Providers[val]; !ok {
			return fmt.Errorf("provider %q not in config: run `wombat setup`", val)
		}
		cfg.EmbeddingProvider = val
	case key == "embedding_model":
		cfg.EmbeddingModel = val
	case key == "review_writers":
		v, err := parseBool(key, val)
		if err != nil {
			return err
		}
		cfg.ReviewWriters = &v
	case key == "review_model":
		cfg.ReviewModel = val
	case key == "check_policy":
		switch strings.ToLower(val) {
		case "auto", "worktree", "merge", "both":
			cfg.CheckPolicy = strings.ToLower(val)
		default:
			return fmt.Errorf("check_policy must be auto, worktree, merge, or both")
		}
	case key == "review_policy":
		switch strings.ToLower(val) {
		case "always", "auto", "never":
			cfg.ReviewPolicy = strings.ToLower(val)
		default:
			return fmt.Errorf("review_policy must be always, auto, or never")
		}
	case key == "worker_full_access":
		v, err := parseBool(key, val)
		if err != nil {
			return err
		}
		cfg.WorkerFullAccess = &v
	case key == "orchestrator":
		v, err := parseBool(key, val)
		if err != nil {
			return err
		}
		cfg.Orchestrator = v
	case key == "parallel_agents":
		v, err := parseBool(key, val)
		if err != nil {
			return err
		}
		cfg.ParallelAgents = v
	case key == "scout_model":
		cfg.ScoutModel = val
	case key == "fallback_models":
		parts := strings.Split(val, ",")
		models := make([]string, 0, len(parts))
		for _, m := range parts {
			if m = strings.TrimSpace(m); m != "" {
				models = append(models, m)
			}
		}
		cfg.FallbackModels = models
	case key == "trim_reasoning":
		v, err := parseBool(key, val)
		if err != nil {
			return err
		}
		cfg.TrimReasoning = v
	case key == "show_thinking":
		v, err := parseBool(key, val)
		if err != nil {
			return err
		}
		cfg.ShowThinking = v
	case key == "reasoning":
		switch strings.ToLower(val) {
		case "off", "none", "minimal", "low", "medium", "default", "high", "max":
			cfg.Reasoning = strings.ToLower(val)
		default:
			return fmt.Errorf("reasoning must be off, low, medium, or high")
		}
	case key == "mode":
		m, err := mode.Parse(val)
		if err != nil {
			return err
		}
		cfg.Mode = string(m)
	case key == "tool_profile":
		val = strings.ToLower(strings.TrimSpace(val))
		if val != "auto" {
			found := false
			for _, name := range tools.ProfileNames() {
				if val == name {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("tool_profile must be auto or one of: %s", strings.Join(tools.ProfileNames(), ", "))
			}
		}
		cfg.ToolProfile = val
	case key == "research_mode":
		val = strings.ToLower(strings.TrimSpace(val))
		switch val {
		case "auto", "off", "quick", "standard", "deep":
			cfg.ResearchMode = val
		default:
			return fmt.Errorf("research_mode must be auto, off, quick, standard, or deep")
		}
	case strings.HasPrefix(key, "capabilities."):
		name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(key, "capabilities.")))
		switch name {
		case "web", "semantic", "git", "verify", "media", "mcp":
		default:
			return fmt.Errorf("capability must be web, semantic, git, verify, media, or mcp")
		}
		switch strings.ToLower(val) {
		case "auto", "on", "off":
			if cfg.Capabilities == nil {
				cfg.Capabilities = map[string]string{}
			}
			cfg.Capabilities[name] = strings.ToLower(val)
		default:
			return fmt.Errorf("capability value must be auto, on, or off")
		}
	case key == "checkpoints":
		v, err := parseBool(key, val)
		if err != nil {
			return err
		}
		cfg.Checkpoints = v
	case key == "require_verification":
		v, err := parseBool(key, val)
		if err != nil {
			return err
		}
		cfg.RequireVerification = v
	case key == "writers":
		v, err := parseBool(key, val)
		if err != nil {
			return err
		}
		cfg.Writers = v
	case key == "keep_worktrees":
		v, err := parseBool(key, val)
		if err != nil {
			return err
		}
		cfg.KeepWorktrees = v
	case strings.HasPrefix(key, "permissions."):
		name := strings.TrimPrefix(key, "permissions.")
		if name == "" {
			return fmt.Errorf("permission name is required")
		}
		switch strings.ToLower(val) {
		case "allow", "ask", "deny":
			if cfg.Permissions == nil {
				cfg.Permissions = map[string]string{}
			}
			cfg.Permissions[name] = strings.ToLower(val)
		default:
			return fmt.Errorf("permission must be allow, ask, or deny")
		}
	case key == "max_parallel_scouts", key == "max_parallel_writers", key == "max_parallel_agents",
		key == "worker_max_steps", key == "worker_token_budget", key == "turn_worker_token_budget",
		key == "writer_max_steps", key == "writer_token_budget", key == "turn_writer_token_budget":
		n, err := strconv.Atoi(val)
		if err != nil || n < 0 {
			return fmt.Errorf("%s must be zero or a positive integer", key)
		}
		switch key {
		case "max_parallel_scouts":
			if n < 1 {
				return fmt.Errorf("max_parallel_scouts must be positive")
			}
			cfg.MaxParallelScouts = n
		case "max_parallel_agents":
			if n < 1 {
				return fmt.Errorf("max_parallel_agents must be positive")
			}
			cfg.MaxParallelAgents = n
		case "max_parallel_writers":
			if n < 1 {
				return fmt.Errorf("max_parallel_writers must be positive")
			}
			cfg.MaxParallelWriters = n
		case "worker_max_steps":
			cfg.WorkerMaxSteps = n
		case "worker_token_budget":
			cfg.WorkerTokenBudget = n
		case "turn_worker_token_budget":
			cfg.TurnWorkerTokenBudget = n
		case "writer_max_steps":
			cfg.WriterMaxSteps = n
		case "writer_token_budget":
			cfg.WriterTokenBudget = n
		case "turn_writer_token_budget":
			cfg.TurnWriterTokenBudget = n
		}
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Println(green("saved: ") + key + " = " + bold(val))
	return nil
}

func parseBool(key, val string) (bool, error) {
	switch strings.ToLower(val) {
	case "true", "on", "yes", "1":
		return true, nil
	case "false", "off", "no", "0":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", key)
	}
}

func printConfig(cfg *config.Config) error {
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if provs, ok := m["providers"].(map[string]any); ok {
		for _, p := range provs {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if k, ok := pm["api_key"].(string); ok && k != "" {
				pm["api_key"] = redactKey(k)
			}
		}
	}
	if servers, ok := m["mcp_servers"].(map[string]any); ok {
		for _, rawServer := range servers {
			sm, ok := rawServer.(map[string]any)
			if !ok {
				continue
			}
			if secret, ok := sm["oauth_client_secret"].(string); ok && secret != "" {
				sm["oauth_client_secret"] = "***"
			}
			for _, field := range []string{"env", "headers"} {
				values, ok := sm[field].(map[string]any)
				if !ok {
					continue
				}
				for key, value := range values {
					if text, ok := value.(string); ok && text != "" {
						values[key] = redactMCPValue(text)
					}
				}
			}
		}
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

func redactMCPValue(value string) string {
	if strings.HasPrefix(value, "${") || strings.HasPrefix(value, "{env:") {
		return value
	}
	return "***"
}

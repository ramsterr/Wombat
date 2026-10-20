package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/sandbox"
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
		return fmt.Errorf("usage: wombat config set <key> <value>\nkeys: provider, model, max_steps, orchestrator, sandbox, sandbox_network, checkpoints, require_verification, writers, keep_worktrees, max_delegations, max_parallel_scouts, worker_max_steps, worker_token_budget, turn_worker_token_budget, max_turn_tokens, max_turn_cost, price_in, price_cached, price_out, max_turn_duration_seconds, max_tool_calls, max_parallel_writers, writer_max_steps, writer_token_budget, turn_writer_token_budget, permissions.<bash|write|edit>")
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
	case key == "max_steps":
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 {
			return fmt.Errorf("max_steps must be a positive integer")
		}
		cfg.MaxSteps = n
	case key == "orchestrator":
		v, err := parseBool(key, val)
		if err != nil {
			return err
		}
		cfg.Orchestrator = v
	case key == "sandbox":
		mode, err := sandbox.Parse(val)
		if err != nil {
			return err
		}
		cfg.Sandbox = string(mode)
	case key == "sandbox_network":
		v, err := parseBool(key, val)
		if err != nil {
			return err
		}
		cfg.SandboxNetwork = v
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
	case key == "max_turn_tokens", key == "max_tool_calls", key == "max_turn_duration_seconds", key == "max_parallel_writers", key == "writer_max_steps", key == "writer_token_budget", key == "turn_writer_token_budget":
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 {
			return fmt.Errorf("%s must be a positive integer", key)
		}
		switch key {
		case "max_turn_tokens":
			cfg.MaxTurnTokens = n
		case "max_tool_calls":
			cfg.MaxToolCalls = n
		case "max_turn_duration_seconds":
			cfg.MaxTurnDuration = n
		case "max_parallel_writers":
			cfg.MaxParallelWriters = n
		case "writer_max_steps":
			cfg.WriterMaxSteps = n
		case "writer_token_budget":
			cfg.WriterTokenBudget = n
		case "turn_writer_token_budget":
			cfg.TurnWriterTokenBudget = n
		}
	case key == "max_turn_cost", key == "price_in", key == "price_cached", key == "price_out":
		f, err := strconv.ParseFloat(val, 64)
		if err != nil || f < 0 {
			return fmt.Errorf("%s must be a non-negative number", key)
		}
		switch key {
		case "max_turn_cost":
			cfg.MaxTurnCost = f
		case "price_in":
			cfg.PriceIn = f
		case "price_cached":
			cfg.PriceCached = f
		case "price_out":
			cfg.PriceOut = f
		}
	case key == "max_delegations":
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 {
			return fmt.Errorf("max_delegations must be a positive integer")
		}
		cfg.MaxDelegations = n
	case key == "max_parallel_scouts":
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 {
			return fmt.Errorf("max_parallel_scouts must be a positive integer")
		}
		cfg.MaxParallelScouts = n
	case key == "worker_max_steps":
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 {
			return fmt.Errorf("worker_max_steps must be a positive integer")
		}
		cfg.WorkerMaxSteps = n
	case key == "worker_token_budget":
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 {
			return fmt.Errorf("worker_token_budget must be a positive integer")
		}
		cfg.WorkerTokenBudget = n
	case key == "turn_worker_token_budget":
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 {
			return fmt.Errorf("turn_worker_token_budget must be a positive integer")
		}
		cfg.TurnWorkerTokenBudget = n
	case strings.HasPrefix(key, "permissions."):
		kind := strings.TrimPrefix(key, "permissions.")
		if kind != "bash" && kind != "write" && kind != "edit" {
			return fmt.Errorf("unknown permission class %q (bash, write, edit)", kind)
		}
		if val != "ask" && val != "allow" && val != "deny" {
			return fmt.Errorf("permission must be ask, allow, or deny")
		}
		if cfg.Permissions == nil {
			cfg.Permissions = map[string]string{}
		}
		cfg.Permissions[kind] = val
	default:
		return fmt.Errorf("unknown key %q", key)
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
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

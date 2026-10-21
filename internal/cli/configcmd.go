package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/mode"
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
		return fmt.Errorf("usage: wombat config set <key> <value>\nkeys: provider, model, mode, parallel_agents, scout_model, reasoning, show_thinking, orchestrator, checkpoints, checkpoint_keep, writers, keep_worktrees, permissions.<bash|write|edit|outside|network>")
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
	case key == "checkpoints":
		v, err := parseBool(key, val)
		if err != nil {
			return err
		}
		cfg.Checkpoints = v
	case key == "checkpoint_keep":
		n, err := strconv.Atoi(val)
		if err != nil || n < 0 {
			return fmt.Errorf("checkpoint_keep must be zero or a positive integer")
		}
		cfg.CheckpointKeep = n
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
		kind := strings.TrimPrefix(key, "permissions.")
		if kind != "bash" && kind != "write" && kind != "edit" && kind != "outside" && kind != "network" {
			return fmt.Errorf("unknown permission class %q (bash, write, edit, outside, network)", kind)
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

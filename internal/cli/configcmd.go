package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ramsterr/Wombat/internal/config"
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
		return fmt.Errorf("usage: wombat config set <key> <value>\nkeys: provider, model, max_steps, permissions.<bash|write|edit>")
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

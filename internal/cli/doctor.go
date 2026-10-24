package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/mode"
	"github.com/ramsterr/Wombat/internal/tokens"
)

func boolWord(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func redactKey(k string) string {
	if k == "" {
		return yellow("(not set)")
	}
	if len(k) <= 8 {
		return green("(set)")
	}
	return green(k[:4]+"…"+k[len(k)-4:]) + dim(" (set)")
}

func cmdDoctor(_ []string) error {
	cfg := mustConfig()
	fmt.Println(bold("wombat doctor"))
	fmt.Println()
	fmt.Println(bold("config"))
	path := config.Path()
	fmt.Println("  file      " + path)
	if fi, err := os.Stat(path); err == nil {
		mode := fi.Mode().Perm()
		warn := ""
		if mode&0o077 != 0 {
			warn = yellow("  should be 0600")
		}
		fmt.Println("  perms     " + mode.String() + warn)
	} else {
		fmt.Println("  perms     " + dim("not created yet"))
	}

	var active string
	var activeConf *config.Provider
	var activeErr error
	if active, activeConf, activeErr = cfg.Active(); activeErr != nil {
		fmt.Println("  provider  " + red(activeErr.Error()))
	} else {
		fmt.Println("  provider  " + bold(active) + dim(fmt.Sprintf(" (%s @ %s)", activeConf.Kind, activeConf.BaseURL)))
		model := cfg.ActiveModel()
		caps := tokens.DiscoverFor(active, model)
		src := ""
		if caps.Source == "provider" {
			src = ", models.dev"
		}
		fmt.Println("  model     " + bold(model) + dim(fmt.Sprintf(" (ctx %s%s)", humanTokens(caps.Context), src)))
		fmt.Println("  api key   " + redactKey(cfg.APIKey(active, activeConf)))
	}

	fmt.Println()
	fmt.Println(bold("mcp"))
	if len(cfg.MCPServers) == 0 {
		fmt.Println("  servers   " + dim("none configured"))
	} else {
		names := sortedMCPNames(cfg.MCPServers)
		enabled := 0
		for _, name := range names {
			if cfg.MCPServers[name].EffectiveEnabled() {
				enabled++
			}
		}
		fmt.Printf("  servers   %d configured, %d enabled %s\n", len(names), enabled, dim("— `wombat mcp test` to connect"))
	}

	fmt.Println()
	fmt.Println(bold("local servers"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, p := range config.Presets {
		if !p.Local {
			continue
		}
		models, err := probeEndpoint(ctx, p.Kind, p.Base)
		if err != nil {
			fmt.Printf("  %s %-9s %s\n", red("✗"), p.Name, dim(p.Base))
			continue
		}
		preview := ""
		if len(models) > 0 {
			n := min(3, len(models))
			preview = " · " + clipLine(strings.Join(models[:n], ", "), 70)
		}
		fmt.Printf("  %s %-9s %s%s\n", green("✓"), p.Name, dim(fmt.Sprintf("%d models", len(models))), dim(preview))
	}

	fmt.Println()
	fmt.Println(bold("session"))
	m, err := mode.Parse(cfg.Mode)
	if err != nil {
		m = mode.Create
	}
	fmt.Println("  mode     " + bold(m.Label()) + dim(" — "+m.Description()))

	fmt.Println()
	fmt.Println(bold("updates"))
	printUpdateStatus(ctx)

	if activeErr == nil {
		fmt.Println()
		fmt.Println(bold("active provider"))
		prov, err := llm.New(active, activeConf, cfg.APIKey(active, activeConf))
		if err != nil {
			fmt.Println("  " + red(err.Error()))
			return nil
		}
		pctx, pcancel := context.WithTimeout(context.Background(), 12*time.Second)
		models, err := prov.ListModels(pctx)
		pcancel()
		if err != nil {
			fmt.Println("  " + red("unreachable: "+clipLine(err.Error(), 120)))
		} else {
			fmt.Println("  " + green(fmt.Sprintf("✓ %d models", len(models))))
		}
	}
	return nil
}

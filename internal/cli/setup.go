package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
)

func readLine(in *bufio.Reader) string {
	line, _ := in.ReadString('\n')
	return strings.TrimSpace(line)
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

type providerChoice struct {
	name   string
	preset config.Preset
	models []string
}

// openCodeAppKeys reads API keys stored by the OpenCode app so setup can
// reuse them without asking the user to paste anything.
func openCodeAppKeys() map[string]string {
	paths := []string{}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		paths = append(paths, filepath.Join(x, "opencode", "auth.json"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".local", "share", "opencode", "auth.json"))
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m map[string]map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		out := map[string]string{}
		for name, v := range m {
			if v["type"] == "api" {
				if k, ok := v["key"].(string); ok && k != "" {
					out[name] = k
				}
			}
		}
		return out
	}
	return map[string]string{}
}

// selectSetupKey keeps an already-saved key instead of asking the user to
// paste it again, then prefers an environment key, then an OpenCode app key.
func selectSetupKey(cfg *config.Config, name string, preset config.Preset, in *bufio.Reader) string {
	if pc := cfg.Providers[name]; pc != nil && pc.APIKey != "" {
		fmt.Printf("an API key is already saved (%s) — keep it? [%s]: ", maskKey(pc.APIKey), bold("Y"))
		if ans := strings.ToLower(readLine(in)); ans == "" || ans == "y" || ans == "yes" {
			return ""
		}
		fmt.Print("api key: ")
		return readLine(in)
	}
	envKey := ""
	if preset.KeyEnv != "" {
		envKey = os.Getenv(preset.KeyEnv)
	}
	if envKey != "" {
		fmt.Printf("found $%s in environment — use and save it? [%s]: ", preset.KeyEnv, bold("Y"))
		if ans := strings.ToLower(readLine(in)); ans == "" || ans == "y" || ans == "yes" {
			return envKey
		}
		fmt.Print("api key: ")
		return readLine(in)
	}
	if k := openCodeAppKeys()[name]; k != "" {
		fmt.Printf("found an OpenCode key in the OpenCode app (%s) — use and save it? [%s]: ", maskKey(k), bold("Y"))
		if ans := strings.ToLower(readLine(in)); ans == "" || ans == "y" || ans == "yes" {
			return k
		}
		fmt.Print("api key: ")
		return readLine(in)
	}
	fmt.Print("api key (enter to skip, can set later via env): ")
	return readLine(in)
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "set"
	}
	return key[:4] + "…" + key[len(key)-4:]
}

// saveSetupProvider writes the provider, base URL, and key to the global
// config as soon as they are entered. Setup can be interrupted while it probes
// for models, and a key the user just pasted must survive that.
func saveSetupProvider(cfg *config.Config, name, kind, base, key string) (*config.Provider, error) {
	if cfg.Providers == nil {
		cfg.Providers = map[string]*config.Provider{}
	}
	pc := cfg.Providers[name]
	if pc == nil {
		pc = &config.Provider{}
		cfg.Providers[name] = pc
	}
	pc.Kind = kind
	if base != "" {
		pc.BaseURL = base
	}
	if key = strings.TrimSpace(key); key != "" {
		pc.APIKey = key
	}
	if kind == "ollama" && pc.NumCtx == 0 {
		pc.NumCtx = 32768
	}
	if cfg.Provider != "" && cfg.Provider != name {
		// The old model belongs to the old provider; setup will pick a new one
		// after probing. Clearing it avoids a broken provider/model pair if
		// setup is interrupted.
		cfg.Model = ""
	}
	cfg.Provider = name
	if err := cfg.Save(); err != nil {
		return nil, err
	}
	return pc, nil
}

func cmdSetup(_ []string) error {
	cfg := mustConfig()
	in := bufio.NewReader(os.Stdin)
	fmt.Println(bold("wombat setup"))
	fmt.Println(dim("probing local model servers…"))

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	var choices []providerChoice
	for _, p := range config.Presets {
		if !p.Local {
			continue
		}
		models, err := probeEndpoint(ctx, p.Kind, p.Base)
		if err == nil {
			choices = append(choices, providerChoice{name: p.Name, preset: p, models: models})
		}
	}
	for _, p := range config.Presets {
		if !p.Local {
			choices = append(choices, providerChoice{name: p.Name, preset: p})
		}
	}

	fmt.Println()
	index := map[int]providerChoice{}
	n := 1
	for _, c := range choices {
		extra := ""
		if c.preset.Local {
			extra = dim(fmt.Sprintf("detected, %d models", len(c.models)))
		} else if c.preset.KeyEnv != "" && os.Getenv(c.preset.KeyEnv) != "" {
			extra = dim("$" + c.preset.KeyEnv + " found")
		}
		fmt.Printf("  %s %-12s %s\n", bold(fmt.Sprintf("%2d)", n)), c.name, extra)
		index[n] = c
		n++
	}
	fmt.Printf("  %s %-12s %s\n", bold(fmt.Sprintf("%2d)", n)), "custom", dim("any OpenAI-compatible endpoint"))
	customNum := n

	fmt.Print("\nprovider [" + bold("1") + "]: ")
	num := atoiDefault(readLine(in), 1)

	var name, kind, base, key string
	if num == customNum {
		fmt.Print("name (e.g. my-server): ")
		name = readLine(in)
		if name == "" {
			return fmt.Errorf("cancelled")
		}
		fmt.Print("base url (e.g. http://localhost:8080/v1): ")
		base = readLine(in)
		if base == "" {
			return fmt.Errorf("cancelled")
		}
		fmt.Print("api key (enter to skip): ")
		key = readLine(in)
		kind = "openai"
		if strings.HasSuffix(strings.TrimRight(base, "/"), "/v1") {
			kind = "openai"
		}
	} else {
		c, ok := index[num]
		if !ok {
			return fmt.Errorf("invalid choice")
		}
		name, kind, base = c.name, c.preset.Kind, c.preset.Base
		if !c.preset.Local {
			key = selectSetupKey(cfg, name, c.preset, in)
		}
	}

	pc, err := saveSetupProvider(cfg, name, kind, base, key)
	if err != nil {
		return err
	}
	fmt.Println(dim("saved " + name + " to " + config.Path() + " — global for every project on this device"))

	prov, err := llm.New(name, pc, cfg.APIKey(name, pc))
	if err != nil {
		return err
	}
	fmt.Println(dim("fetching models…"))
	modelCtx, mcancel := context.WithTimeout(context.Background(), 10*time.Second)
	models, lerr := prov.ListModels(modelCtx)
	mcancel()

	model := ""
	if lerr != nil {
		fmt.Println(yellow("could not fetch models: " + lerr.Error()))
		fmt.Print("enter model name manually: ")
		model = readLine(in)
	} else {
		if len(models) > 60 {
			models = models[:60]
		}
		for i, m := range models {
			fmt.Printf("  %s %s\n", bold(fmt.Sprintf("%2d)", i+1)), m)
		}
		fmt.Print("model [" + bold("1") + "]: ")
		sel := readLine(in)
		if sel == "" {
			if len(models) == 0 {
				fmt.Print("enter model name manually: ")
				model = readLine(in)
			} else {
				model = models[0]
			}
		} else if num := atoiDefault(sel, 0); num >= 1 && num <= len(models) {
			model = models[num-1]
		} else {
			model = sel
		}
		pc.Models = models
	}
	if model == "" {
		return fmt.Errorf("no model selected")
	}

	cfg.Provider = name
	cfg.Model = model
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("\n%s provider=%s model=%s\n", green("saved:"), bold(name), bold(model))
	fmt.Println(dim("config: " + config.Path()))
	fmt.Println(dim("next: run `wombat` to start, or `wombat doctor` to verify"))
	return nil
}

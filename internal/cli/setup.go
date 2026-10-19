package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
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
			envKey := ""
			if c.preset.KeyEnv != "" {
				envKey = os.Getenv(c.preset.KeyEnv)
			}
			if envKey != "" {
				fmt.Printf("found $%s in environment — use it? [%s]: ", c.preset.KeyEnv, bold("Y"))
				if ans := strings.ToLower(readLine(in)); ans == "" || ans == "y" || ans == "yes" {
					key = ""
				} else {
					fmt.Print("api key: ")
					key = readLine(in)
				}
			} else {
				fmt.Print("api key (enter to skip, can set later via env): ")
				key = readLine(in)
			}
		}
	}

	pc, ok := cfg.Providers[name]
	if !ok {
		pc = &config.Provider{}
		cfg.Providers[name] = pc
	}
	pc.Kind = kind
	pc.BaseURL = base
	if key != "" {
		pc.APIKey = key
	}
	if kind == "ollama" && pc.NumCtx == 0 {
		pc.NumCtx = 32768
	}

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

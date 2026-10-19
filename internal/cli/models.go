package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/ramsterr/Wombat/internal/llm"
)

func cmdModels(args []string) error {
	cfg := mustConfig()
	name := cfg.Provider
	if len(args) > 0 && args[0] != "" {
		name = args[0]
	}
	if name == "" {
		return fmt.Errorf("no provider configured: run `wombat setup`")
	}
	pc, ok := cfg.Providers[name]
	if !ok {
		return fmt.Errorf("provider %q not found in config: run `wombat setup`", name)
	}
	prov, err := llm.New(name, pc, cfg.APIKey(name, pc))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	models, err := prov.ListModels(ctx)
	if err != nil {
		return err
	}
	for _, m := range models {
		marker := "  "
		if m == cfg.ActiveModel() && name == cfg.Provider {
			marker = "* "
		}
		fmt.Println(marker + m)
	}
	pc.Models = models
	_ = cfg.Save()
	return nil
}

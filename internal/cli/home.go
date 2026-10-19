package cli

import (
	"fmt"
	"os"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/tui"
)

func cmdHome() error {
	for {
		cfg, err := config.Load()
		if err != nil {
			fmt.Fprintln(os.Stderr, red("error: "+err.Error()))
			return err
		}
		cwd, _ := os.Getwd()
		action, err := tui.Run(tui.Options{
			Version: Version,
			Cwd:     cwd,
			Cfg:     cfg,
		})
		if err != nil {
			return err
		}
		switch action.Kind {
		case "chat":
			if cfg.ActiveModel() == "" {
				if err := cmdSetup(nil); err != nil {
					fmt.Fprintln(os.Stderr, red("error: "+err.Error()))
					continue
				}
				continue
			}
			return cmdRepl(nil, false)
		case "resume":
			return cmdRepl([]string{action.ID}, true)
		case "configure":
			if err := cmdSetup(nil); err != nil {
				fmt.Fprintln(os.Stderr, red("error: "+err.Error()))
			}
		case "quit", "":
			return nil
		}
	}
}

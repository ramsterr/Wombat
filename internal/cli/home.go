package cli

import (
	"fmt"
	"os"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/session"
)

func cmdHome() error {
	cfg := mustConfig()
	for {
		cwd, _ := os.Getwd()
		last := ""
		if meta, err := session.Latest(cwd); err == nil {
			last = meta.ID
			if meta.Title != "" {
				last += " · " + clipLine(meta.Title, 40)
			}
		}
		action, err := runMenu(cfg, last)
		if err != nil {
			return err
		}
		switch action {
		case "chat":
			if cfg.ActiveModel() == "" {
				if err := cmdSetup(nil); err != nil {
					fmt.Fprintln(os.Stderr, red("error: "+err.Error()))
				}
				cfg, _ = config.Load()
				continue
			}
			return cmdRepl(nil, false)
		case "resume":
			return cmdRepl([]string{"last"}, true)
		case "configure":
			if err := cmdSetup(nil); err != nil {
				fmt.Fprintln(os.Stderr, red("error: "+err.Error()))
			}
			cfg, _ = config.Load()
		case "models":
			if err := cmdModels(nil); err != nil {
				fmt.Fprintln(os.Stderr, red("error: "+err.Error()))
			}
			pause()
		case "sessions":
			if err := cmdSessions(nil); err != nil {
				fmt.Fprintln(os.Stderr, red("error: "+err.Error()))
			}
			pause()
		case "doctor":
			if err := cmdDoctor(nil); err != nil {
				fmt.Fprintln(os.Stderr, red("error: "+err.Error()))
			}
			pause()
		case "quit":
			return nil
		}
	}
}

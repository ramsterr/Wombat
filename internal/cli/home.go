package cli

import (
	"fmt"
	"os"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tui"
)

func cmdHome(sessionID string) error {
	for {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if sessionID == "" && (cfg.Provider == "" || cfg.ActiveModel() == "") {
			if err := cmdSetup(nil); err != nil {
				return err
			}
			continue
		}
		cwd, _ := os.Getwd()
		action, err := tui.Run(tui.Options{
			Version:   Version,
			Cwd:       cwd,
			Cfg:       cfg,
			SessionID: sessionID,
			Mouse:     cfg.Mouse,
		})
		if err != nil {
			return err
		}
		switch action.Kind {
		case "configure":
			if err := cmdSetup(nil); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func cmdResumeTUI(args []string) error {
	cwd, _ := os.Getwd()
	id := ""
	if len(args) > 0 {
		id = args[0]
	}
	if id == "" || id == "last" {
		meta, err := session.Latest(cwd)
		if err != nil {
			return fmt.Errorf("no sessions to resume in %s", cwd)
		}
		id = meta.ID
	}
	return cmdHome(id)
}

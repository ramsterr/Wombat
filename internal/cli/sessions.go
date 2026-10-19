package cli

import (
	"fmt"
	"os"

	"github.com/ramsterr/Wombat/internal/session"
)

func cmdSessions(_ []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	metas, err := session.List(cwd)
	if err != nil {
		return err
	}
	if len(metas) == 0 {
		fmt.Println(dim("no sessions for " + cwd))
		return nil
	}
	for _, m := range metas {
		title := m.Title
		if title == "" {
			title = dim("(untitled)")
		}
		fmt.Printf("  %s  %s  %s  %s\n",
			bold(m.ID), dim(m.Updated.Format("2006-01-02 15:04")), m.Model, title)
	}
	fmt.Println(dim("\nresume with: wombat resume <id>  (or: wombat resume last)"))
	return nil
}

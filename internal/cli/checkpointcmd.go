package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/ramsterr/Wombat/internal/checkpoint"
	"github.com/ramsterr/Wombat/internal/session"
)

func cmdCheckpoint(args []string) error {
	action := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}
	var sessionID, snapshotID string
	yes := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--yes", "-y":
			yes = true
		case "--session", "-s":
			if i+1 >= len(args) {
				return fmt.Errorf("--session needs a session id")
			}
			i++
			sessionID = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag %s", args[i])
			}
			if snapshotID == "" && action == "restore" {
				snapshotID = args[i]
			} else if sessionID == "" {
				sessionID = args[i]
			} else {
				return fmt.Errorf("unexpected argument %q", args[i])
			}
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	sess, err := openSessionForCheckpoint(cwd, sessionID)
	if err != nil {
		if action == "list" && sessionID == "" {
			fmt.Println(dim("no sessions yet in " + cwd))
			return nil
		}
		return err
	}
	mgr, err := checkpoint.New(checkpoint.RootForSession(sess.Path()), cwd)
	if err != nil {
		return err
	}
	switch action {
	case "list":
		list, err := mgr.List()
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println(dim("no checkpoints for this session"))
			return nil
		}
		for _, m := range list {
			scope := "full"
			if !m.Full {
				scope = strings.Join(m.Scope, ",")
			}
			fmt.Printf("%s  %-18s  %3d files  %s\n", m.ID, scope, len(m.Files), dim(m.Created.Local().Format("2006-01-02 15:04:05")))
		}
		return nil
	case "restore":
		if snapshotID == "" {
			return fmt.Errorf("usage: wombat checkpoint restore <snapshot-id> [--session id] [--yes]")
		}
		if !yes {
			if !isTTY(os.Stdin) {
				return fmt.Errorf("refusing to restore without --yes in non-interactive mode")
			}
			fmt.Printf("restore checkpoint %s into %s? [y/N] ", bold(snapshotID), cwd)
			var answer string
			_, _ = fmt.Scanln(&answer)
			if strings.ToLower(strings.TrimSpace(answer)) != "y" {
				fmt.Println("cancelled")
				return nil
			}
		}
		if err := mgr.Restore(snapshotID); err != nil {
			return err
		}
		fmt.Println(green("restored ") + snapshotID)
		return nil
	default:
		return fmt.Errorf("usage: wombat checkpoint [list|restore]")
	}
}

func openSessionForCheckpoint(cwd, id string) (*session.Session, error) {
	if id == "" || id == "last" {
		meta, err := session.Latest(cwd)
		if err != nil {
			return nil, fmt.Errorf("no sessions in %s", cwd)
		}
		id = meta.ID
	}
	return session.Open(id, cwd)
}

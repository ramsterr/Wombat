package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ramsterr/Wombat/internal/checkpoint"
	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
)

func cmdCheckpoint(args []string) error {
	action := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}
	cfg := mustConfig()
	var sessionID, snapshotID string
	yes := false
	all := false
	keep := cfg.CheckpointKeep
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--yes", "-y":
			yes = true
		case "--all", "-a":
			all = true
		case "--session", "-s":
			if i+1 >= len(args) {
				return fmt.Errorf("--session needs a session id")
			}
			i++
			sessionID = args[i]
		case "--keep", "-k":
			if i+1 >= len(args) {
				return fmt.Errorf("--keep needs a number")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				return fmt.Errorf("--keep must be a non-negative number")
			}
			keep = n
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
	if all && action != "prune" {
		return fmt.Errorf("--all is only valid with prune")
	}
	if action == "prune" && all {
		sessions, removed, freed, err := checkpoint.PruneAll(config.Home(), keep)
		if err != nil {
			return err
		}
		fmt.Printf("pruned %d checkpoint(s) across %d session(s), kept the newest %d per session, reclaimed %s\n",
			removed, sessions, keep, llm.HumanSize(freed))
		return nil
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
	mgr.SetKeep(keep)
	switch action {
	case "list":
		if reason := mgr.DisabledReason(); reason != "" {
			fmt.Println(dim(reason))
		}
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
	case "prune":
		removed, err := mgr.Prune(keep)
		if err != nil {
			return err
		}
		fmt.Printf("pruned %d checkpoint(s), kept the newest %d\n", removed, keep)
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
		return fmt.Errorf("usage: wombat checkpoint [list|restore|prune [--keep N] [--all]]")
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

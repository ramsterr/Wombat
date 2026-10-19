package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/session"
)

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	model := fs.String("m", "", "model override")
	prov := fs.String("p", "", "provider override")
	yolo := fs.Bool("yolo", false, "auto-approve tool permissions")
	cont := fs.Bool("c", false, "continue latest session")
	quiet := fs.Bool("quiet", false, "only print assistant text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	task := strings.Join(fs.Args(), " ")
	if task == "" && !*cont {
		return fmt.Errorf(`usage: wombat run "<task>"`)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg := mustConfig()

	var sess *session.Session
	if *cont {
		meta, err := session.Latest(cwd)
		if err != nil {
			return fmt.Errorf("no sessions to continue in %s", cwd)
		}
		sess, err = session.Open(meta.ID, cwd)
		if err != nil {
			return err
		}
	} else {
		sess, err = session.Create(cwd, "", "", clipLine(task, 80))
		if err != nil {
			return err
		}
	}

	p := newPrompter(isTTY(os.Stdin), *yolo, cfg)
	r := &renderer{cfg: cfg, sess: sess, quiet: *quiet}
	ag, err := buildAgent(cfg, runOpts{model: *model, provider: *prov, yolo: *yolo, quiet: *quiet}, sess, p)
	if err != nil {
		return err
	}
	ag.Hooks = r.hooks()
	r.windowFn = func() int { return ag.ContextWindow }

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	start := time.Now()
	runErr := ag.Run(ctx, task)
	r.breakLine()
	if !*quiet {
		fmt.Fprintf(os.Stderr, "%s\n", dim(fmt.Sprintf("done in %s · in %s (cache %s) out %s · session %s",
			time.Since(start).Round(time.Millisecond),
			humanTokens(r.cumIn), humanTokens(r.cumCached), humanTokens(r.cumOut), sess.ID())))
	}
	return runErr
}

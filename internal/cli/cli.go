package cli

import (
	"fmt"
	"os"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
)

func Main() {
	llm.UserAgent = "wombat/" + Version
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd = args[0]
		args = args[1:]
	}
	var err error
	switch cmd {
	case "", "chat":
		err = cmdRepl(args, false)
	case "run":
		err = cmdRun(args)
	case "resume":
		err = cmdRepl(args, true)
	case "setup":
		err = cmdSetup(args)
	case "models":
		err = cmdModels(args)
	case "sessions":
		err = cmdSessions(args)
	case "doctor":
		err = cmdDoctor(args)
	case "config":
		err = cmdConfig(args)
	case "version", "--version", "-v":
		fmt.Println("wombat " + Version)
	case "help", "--help", "-h":
		help()
	default:
		fmt.Fprintln(os.Stderr, red("unknown command: "+cmd))
		help()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, red("error: "+err.Error()))
		os.Exit(1)
	}
}

func help() {
	fmt.Print(`wombat — a lightweight coding agent

usage:
  wombat                     interactive session (new)
  wombat resume [id|last]    resume a session
  wombat run "<task>"        one-shot headless run
  wombat setup               configure providers and models
  wombat models [provider]   list available models
  wombat sessions            list sessions for this directory
  wombat doctor              check configuration and connectivity
  wombat config              show config (redacted); use ` + "`wombat config set`" + `
  wombat version             print version

flags for run/resume:
  -m <model>       override model
  -p <provider>    override provider
  --yolo           auto-approve all tool permissions
  -c               continue: resume the latest session (run/resume)
  --quiet          only print assistant text (run)
`)
}

func mustConfig() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, red("error: "+err.Error()))
		os.Exit(1)
	}
	return cfg
}

package cli

import (
	"fmt"
	"os"
	"strings"

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
	if cmd == "" {
		for _, a := range args {
			if a == "-h" || a == "--help" || a == "help" {
				help()
				return
			}
		}
	}
	var err error
	switch cmd {
	case "":
		if len(args) == 0 && isTTY(os.Stdin) {
			err = cmdHome("")
		} else {
			err = cmdRepl(args, false)
		}
	case "home":
		err = cmdHome("")
	case "chat":
		if len(args) == 0 && isTTY(os.Stdin) {
			err = cmdHome("")
		} else {
			err = cmdRepl(args, false)
		}
	case "run":
		err = cmdRun(args)
	case "resume":
		tuiMode := isTTY(os.Stdin)
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				tuiMode = false
			}
		}
		if tuiMode {
			err = cmdResumeTUI(args)
		} else {
			err = cmdRepl(args, true)
		}
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
	case "checkpoint":
		err = cmdCheckpoint(args)
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
  wombat                     start the chat TUI
  wombat chat                same as wombat (line mode when piped)
  wombat resume [id|last]    resume a session in the TUI
  wombat run "<task>"        one-shot headless run (attach with @path)
  wombat setup               configure providers and models
  wombat models [provider]   list available models
  wombat sessions            list sessions for this directory
  wombat doctor              check configuration and connectivity
  wombat config              show config (redacted); use ` + "`wombat config set`" + `
  wombat checkpoint list     list session checkpoints
  wombat checkpoint restore  restore a checkpoint (--yes to confirm)
  wombat version             print version

flags for run/resume:
  -m <model>       override model
  -p <provider>    override provider
  --yolo           auto-approve all tool permissions
  -c               continue: resume the latest session (run/resume)
  --quiet          only print assistant text (run)
  --image <path>   attach an image (repeatable, run only)
  --video <path>   attach a video (repeatable, run only)
  --audio <path>   attach audio (repeatable, run only)
  --file <path>    attach a document/file (repeatable, run only)

attachments:
  write @path in a message (@"path with spaces" for spaces), or use
  /attach <path> in the chat TUI. Images/video/audio require a model
  that supports them; audio/video are model- and provider-specific.
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

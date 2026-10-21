package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/commands"
	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/mode"
	"github.com/ramsterr/Wombat/internal/session"
)

type renderer struct {
	mu        sync.Mutex
	cfg       *config.Config
	sess      *session.Session
	quiet     bool
	sp        *spinner
	textOpen  bool
	thinkOpen bool
	windowFn  func() int
	cumIn     int
	cumOut    int
	cumCached int
}

func (r *renderer) hooks() agent.Hooks {
	locked := func(fn func()) {
		r.mu.Lock()
		defer r.mu.Unlock()
		fn()
	}
	return agent.Hooks{
		OnText: func(s string) {
			locked(func() {
				if r.thinkOpen {
					fmt.Fprintln(os.Stderr)
					r.thinkOpen = false
				}
				if r.sp != nil && !r.textOpen {
					r.sp.Stop()
				}
				r.textOpen = true
				fmt.Print(s)
			})
		},
		OnReasoning: func(s string) {
			locked(func() {
				if r.quiet {
					return
				}
				if !r.thinkOpen {
					if r.sp != nil {
						r.sp.Stop()
					}
					if r.textOpen {
						r.breakLine()
					}
					fmt.Fprintln(os.Stderr, dim("thinking:"))
					r.thinkOpen = true
				}
				fmt.Fprint(os.Stderr, dim(s))
			})
		},
		OnTool: func(name, detail string) {
			locked(func() {
				if r.quiet {
					return
				}
				r.breakLine()
				fmt.Fprintf(os.Stderr, "%s %s %s\n", dim("⚒"), bold(name), dim(clipLine(detail, 100)))
			})
		},
		OnToolResult: func(name, result string, dur time.Duration) {
			locked(func() {
				if r.quiet {
					return
				}
				preview := ""
				if result != "" {
					preview = dim(" · " + clipLine(result, 80))
				}
				mark := green("✓")
				if strings.HasPrefix(result, "error:") {
					mark = red("✗")
				}
				fmt.Fprintf(os.Stderr, "%s %s %s%s\n", mark, dim(name), dim(dur.Round(time.Millisecond).String()), preview)
			})
		},
		OnUsage: func(u llm.Usage, history int) {
			locked(func() {
				r.cumIn += u.In
				r.cumOut += u.Out
				r.cumCached += u.Cached
				if history < 0 || r.quiet || !colorEnabled {
					return
				}
				cache := ""
				if u.Cached > 0 {
					cache = fmt.Sprintf(" (cache %s)", humanTokens(u.Cached))
				}
				fmt.Fprintf(os.Stderr, "%s\n", dim(fmt.Sprintf("· in %s%s out %s · context %s/%s",
					humanTokens(u.In), cache, humanTokens(u.Out),
					humanTokens(history), humanTokens(r.window()))))
			})
		},
		OnCompact: func(kind string, before, after int) {
			locked(func() {
				if r.quiet {
					return
				}
				fmt.Fprintf(os.Stderr, "%s\n", dim(fmt.Sprintf("⟳ %s compaction: %s → %s tokens",
					kind, humanTokens(before), humanTokens(after))))
			})
		},
	}
}

func (r *renderer) window() int {
	if r.windowFn != nil {
		return r.windowFn()
	}
	return 128000
}

func (r *renderer) breakLine() {
	if r.thinkOpen {
		fmt.Fprintln(os.Stderr)
		r.thinkOpen = false
	}
	if r.textOpen {
		fmt.Println()
		r.textOpen = false
	}
}

func cmdRepl(args []string, resume bool) error {
	fs := flag.NewFlagSet("wombat", flag.ContinueOnError)
	model := fs.String("m", "", "model override")
	prov := fs.String("p", "", "provider override")
	yolo := fs.Bool("yolo", false, "auto-approve tool permissions")
	cont := fs.Bool("c", false, "continue latest session")
	quiet := fs.Bool("quiet", false, "suppress tool output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg := mustConfig()

	var sess *session.Session
	continueLatest := resume || *cont
	if continueLatest {
		id := fs.Arg(0)
		if id == "" || id == "last" {
			meta, err := session.Latest(cwd)
			if err != nil {
				return fmt.Errorf("no sessions to resume in %s", cwd)
			}
			id = meta.ID
		}
		sess, err = session.Open(id, cwd)
		if err != nil {
			return err
		}
		fmt.Println(dim("resumed session " + sess.ID() + " (" + fmt.Sprint(len(sess.Messages)) + " messages)"))
	} else {
		sess, err = session.Create(cwd, "", "", "")
		if err != nil {
			return err
		}
	}

	p := newPrompter(isTTY(os.Stdin), *yolo, cfg)
	r := &renderer{cfg: cfg, sess: sess, quiet: *quiet}
	opts := runOpts{model: *model, provider: *prov, yolo: *yolo}

	ag, err := buildAgent(cfg, opts, sess, p)
	if err != nil {
		return err
	}
	defer ag.Env.JobManager().Close()
	defer ag.Env.CloseLSP()
	defer ag.Env.CloseBrowser()
	ag.Hooks = r.hooks()
	r.windowFn = func() int { return ag.ContextWindow }

	fmt.Printf("%s %s\n", bold("◆ wombat"), dim(VersionString()+" · "+ag.Provider.Name()+"/"+ag.Model+" · session "+sess.ID()))
	fmt.Println(dim("  /help for commands · ctrl+c interrupts a run · /exit quits"))

	in := bufio.NewReader(os.Stdin)
	firstInput := len(sess.Messages) == 0
	for {
		fmt.Print("\n" + green("❯") + " ")
		line, err := in.ReadString('\n')
		if err != nil {
			fmt.Println()
			return nil
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if looksLikeCommand(line) {
			if strings.HasPrefix(line, "/") {
				name := line
				if i := strings.IndexAny(name, " \t"); i >= 0 {
					name = name[:i]
				}
				if _, ok := commands.Find(name); !ok {
					if matches := commands.Match(name); len(matches) > 0 {
						fmt.Println(dim("did you mean:"))
						for _, m := range matches {
							fmt.Printf("  %-34s %s\n", m.Usage(), dim(m.Summary))
						}
						continue
					}
				}
			}
			done, newAg, newSess, err := handleCommand(line, cfg, ag, sess, p, r, &opts)
			if err != nil {
				fmt.Fprintln(os.Stderr, red(err.Error()))
			}
			if done {
				return nil
			}
			if newAg != nil {
				if ag.Env != nil {
					ag.Env.JobManager().Close()
					ag.Env.CloseLSP()
					ag.Env.CloseBrowser()
				}
				ag = newAg
				sess = newSess
				r.sess = newSess
				ag.Hooks = r.hooks()
			}
			continue
		}
		if path, ok := llm.MediaPath(line); ok {
			line = ""
			mentions := []string{path}
			atts, err := attachFiles(sess, mentions, *quiet)
			if err != nil {
				fmt.Fprintln(os.Stderr, red("error: "+err.Error()))
				continue
			}
			if firstInput {
				sess.Meta.Title = clipLine(atts[0].Name, 80)
				_ = sess.Flush()
				firstInput = false
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			r.sp = startSpinner("thinking…")
			r.textOpen = false
			err = ag.RunWithAttachments(ctx, line, atts)
			r.sp.Stop()
			stop()
			r.breakLine()
			if err != nil {
				fmt.Fprintln(os.Stderr, red("error: "+err.Error()))
			}
			continue
		}
		line, mentions := llm.ParseMentions(line)
		atts, err := attachFiles(sess, mentions, *quiet)
		if err != nil {
			fmt.Fprintln(os.Stderr, red("error: "+err.Error()))
			continue
		}
		if line == "" && len(atts) == 0 {
			continue
		}
		if firstInput {
			title := line
			if title == "" && len(atts) > 0 {
				title = atts[0].Name
			}
			sess.Meta.Title = clipLine(title, 80)
			_ = sess.Flush()
			firstInput = false
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		r.sp = startSpinner("thinking…")
		r.textOpen = false
		err = ag.RunWithAttachments(ctx, line, atts)
		r.sp.Stop()
		stop()
		r.breakLine()
		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				fmt.Println(yellow("interrupted"))
			} else {
				fmt.Fprintln(os.Stderr, red("error: "+err.Error()))
			}
		}
	}
}

func handleCommand(line string, cfg *config.Config, ag *agent.Agent, sess *session.Session, p *Prompter, r *renderer, opts *runOpts) (done bool, newAg *agent.Agent, newSess *session.Session, err error) {
	parts := strings.Fields(line)
	cmd := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = strings.Join(parts[1:], " ")
	}
	switch cmd {
	case "/exit", "/quit", "/q":
		return true, nil, nil, nil
	case "/help":
		fmt.Print(helpText())
	case "/model":
		if arg == "" {
			return false, nil, nil, fmt.Errorf("usage: /model <name>")
		}
		opts.model = arg
		cfg.Model = arg
		_ = cfg.Save()
		na, _, _, err := rebuild(cfg, *opts, sess, p, r)
		return false, na, sess, err
	case "/provider":
		if arg == "" {
			return false, nil, nil, fmt.Errorf("usage: /provider <name>")
		}
		if _, ok := cfg.Providers[arg]; !ok {
			return false, nil, nil, fmt.Errorf("provider %q not in config (run `wombat setup`)", arg)
		}
		opts.provider = arg
		opts.model = ""
		cfg.Provider = arg
		_ = cfg.Save()
		na, _, _, err := rebuild(cfg, *opts, sess, p, r)
		return false, na, sess, err
	case "/models":
		prov, _, _, err := buildProvider(context.Background(), cfg, opts.provider, opts.model)
		if err != nil {
			return false, nil, nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		models, err := prov.ListModels(ctx)
		if err != nil {
			return false, nil, nil, err
		}
		for _, m := range models {
			fmt.Println("  " + m)
		}
	case "/new":
		ns, err := session.Create(sess.Meta.Cwd, ag.Provider.Name(), ag.Model, "")
		if err != nil {
			return false, nil, nil, err
		}
		na, _, _, err := rebuild(cfg, *opts, ns, p, r)
		fmt.Println(dim("new session " + ns.ID()))
		return false, na, ns, err
	case "/compact":
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		if err := ag.CompactNow(ctx); err != nil {
			return false, nil, nil, err
		}
	case "/cost":
		fmt.Printf("in %s (cache %s) · out %s · context ~%s/%s\n",
			humanTokens(r.cumIn), humanTokens(r.cumCached), humanTokens(r.cumOut),
			humanTokens(ag.HistoryTokens()), humanTokens(ag.ContextWindow))
	case "/yolo":
		p.auto = !p.auto
		fmt.Println(dim(fmt.Sprintf("auto-approve: %v", p.auto)))
	case "/thinking":
		show := cfg.ShowThinking
		newState, msg, changed, err := commands.ThinkingCommand(line, show)
		if err != nil {
			return false, nil, nil, err
		}
		fmt.Println(msg)
		if changed {
			cfg.ShowThinking = newState
			if err := cfg.Save(); err != nil {
				return false, nil, nil, err
			}
		}
	case "/toolset":
		current := mode.Create
		if ag != nil && ag.Env != nil && ag.Env.Mode != "" {
			if m, perr := mode.Parse(ag.Env.Mode); perr == nil {
				current = m
			}
		}
		fmt.Println(toolsetText(ag, current, cfg.Permissions))
	case "/sources":
		fmt.Println(sourcesText(sess))
	case "/history":
		fmt.Println(historyText(sess))
	case "/mode", "/modes":
		current, err := mode.Parse(cfg.Mode)
		if err != nil {
			return false, nil, nil, err
		}
		if ag != nil && ag.Env != nil && ag.Env.Mode != "" {
			if m, perr := mode.Parse(ag.Env.Mode); perr == nil {
				current = m
			}
		}
		newMode, msg, changed, err := mode.Apply(line, current)
		if err != nil {
			return false, nil, nil, err
		}
		fmt.Println(msg)
		if !changed {
			if cmd, ok := commands.Find("/mode"); ok && len(cmd.Options) > 0 {
				fmt.Println(dim("options:"))
				for _, o := range cmd.Options {
					fmt.Printf("  %-26s %s\n", o.Value, dim(o.Summary))
				}
			}
		}
		if !changed {
			return false, nil, nil, nil
		}
		opts.mode = newMode
		opts.modeSet = true
		na, _, _, err := rebuild(cfg, *opts, sess, p, r)
		if err != nil {
			return false, nil, nil, err
		}
		return false, na, sess, nil
	case "/status":
		current := mode.Create
		if ag.Env != nil && ag.Env.Mode != "" {
			if m, perr := mode.Parse(ag.Env.Mode); perr == nil {
				current = m
			}
		}
		fmt.Printf("provider=%s model=%s session=%s\ncwd=%s\nmode=%s\n",
			ag.Provider.Name(), ag.Model, sess.ID(), sess.Meta.Cwd, current.Label())
	default:
		return false, nil, nil, fmt.Errorf("unknown command %s — /help", cmd)
	}
	return false, nil, nil, nil
}

func rebuild(cfg *config.Config, opts runOpts, sess *session.Session, p *Prompter, r *renderer) (*agent.Agent, string, string, error) {
	na, err := buildAgent(cfg, opts, sess, p)
	if err != nil {
		return nil, "", "", err
	}
	changed := sess.Meta.Model != na.Model || sess.Meta.Provider != na.Provider.Name()
	if sess.Meta.Model != "" {
		sess.Meta.Model = na.Model
	}
	sess.Meta.Provider = na.Provider.Name()
	_ = sess.Flush()
	if changed {
		fmt.Println(dim("now using " + na.Provider.Name() + "/" + na.Model))
	}
	return na, na.Provider.Name(), na.Model, nil
}

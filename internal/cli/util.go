package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/config"
)

const Version = "0.1.0"

var colorEnabled = os.Getenv("NO_COLOR") == "" && isTTY(os.Stdout)

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func dim(s string) string    { return wrap(s, "2") }
func bold(s string) string   { return wrap(s, "1") }
func green(s string) string  { return wrap(s, "32") }
func red(s string) string    { return wrap(s, "31") }
func yellow(s string) string { return wrap(s, "33") }
func cyan(s string) string   { return wrap(s, "36") }

func wrap(s, code string) string {
	if !colorEnabled {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

type Prompter struct {
	always map[string]bool
	in     *bufio.Reader
	tty    bool
	auto   bool
	perms  map[string]string
}

func newPrompter(tty, auto bool, cfg *config.Config) *Prompter {
	return &Prompter{
		always: map[string]bool{},
		in:     bufio.NewReader(os.Stdin),
		tty:    tty,
		auto:   auto,
		perms:  cfg.Permissions,
	}
}

func (p *Prompter) Permit(kind, detail string) bool {
	if v, ok := p.perms[kind]; ok {
		switch v {
		case "allow":
			return true
		case "deny":
			fmt.Fprintln(os.Stderr, dim("[permission] "+kind+" is denied by config: "+clipLine(detail, 120)))
			return false
		}
	}
	if p.auto || p.always[kind] {
		return true
	}
	if !p.tty {
		fmt.Fprintln(os.Stderr, dim("[permission] "+kind+" requires approval: "+clipLine(detail, 120)+" — denied (non-interactive; rerun with --yolo)"))
		return false
	}
	fmt.Fprintf(os.Stderr, "%s %s\n  %s\n  %s / %s / %s: ",
		yellow("?"), "allow "+kind, clipLine(detail, 200),
		bold("y")+"es", bold("a")+"lways "+kind, bold("n")+"o")
	line, err := p.in.ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "a", "always":
		p.always[kind] = true
		return true
	default:
		return false
	}
}

func clipLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

type spinner struct {
	stop chan struct{}
	done chan struct{}
	on   bool
}

func startSpinner(label string) *spinner {
	s := &spinner{stop: make(chan struct{}), done: make(chan struct{}), on: isTTY(os.Stderr) && colorEnabled}
	if !s.on {
		close(s.done)
		return s
	}
	go func() {
		defer close(s.done)
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				fmt.Fprint(os.Stderr, "\r\033[2K")
				return
			case <-t.C:
				fmt.Fprintf(os.Stderr, "\r\033[2K%s %s", frames[i], dim(label))
				i = (i + 1) % len(frames)
			}
		}
	}()
	return s
}

func (s *spinner) Stop() {
	if s.on {
		close(s.stop)
	}
	<-s.done
}

func humanTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

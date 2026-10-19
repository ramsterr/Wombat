package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ramsterr/Wombat/internal/config"
)

type menuItem struct {
	label  string
	hint   string
	action string
}

func homeItems(cfg *config.Config, lastSession string) []menuItem {
	model := cfg.ActiveModel()
	items := []menuItem{{
		label:  "Chat",
		hint:   providerModelHint(cfg, model),
		action: "chat",
	}}
	if lastSession != "" {
		items = append(items, menuItem{
			label:  "Resume last session",
			hint:   lastSession,
			action: "resume",
		})
	}
	items = append(items,
		menuItem{label: "Configure provider & model", hint: "", action: "configure"},
		menuItem{label: "List models", hint: "", action: "models"},
		menuItem{label: "Sessions", hint: "", action: "sessions"},
		menuItem{label: "Doctor", hint: "", action: "doctor"},
		menuItem{label: "Quit", hint: "", action: "quit"},
	)
	return items
}

func providerModelHint(cfg *config.Config, model string) string {
	if model == "" {
		return "no provider configured"
	}
	return cfg.Provider + "/" + model
}

func renderMenu(w io.Writer, cfg *config.Config, items []menuItem, idx int) {
	fmt.Fprintf(w, "%s %s\n\n", bold("◆ wombat"), dim(Version))
	for i, it := range items {
		cursor := "  "
		label := it.label
		if i == idx {
			cursor = cyan("❯ ")
			label = bold(label)
		}
		line := cursor + label
		if it.hint != "" {
			line += "  " + dim(it.hint)
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintf(w, "\n%s\n", dim("  ↑/↓ move · enter select · q quit"))
}

const menuLines = 12

func runMenu(cfg *config.Config, lastSession string) (string, error) {
	items := homeItems(cfg, lastSession)
	start := 0
	if cfg.ActiveModel() == "" {
		for i, it := range items {
			if it.action == "configure" {
				start = i
			}
		}
	}
	if !isTTY(os.Stdin) {
		return runMenuText(os.Stdin, os.Stdout, items, start)
	}
	return runMenuTTY(cfg, items, start)
}

func runMenuTTY(cfg *config.Config, items []menuItem, start int) (string, error) {
	restore, err := makeRaw(os.Stdin)
	if err != nil {
		return runMenuText(os.Stdin, os.Stdout, items, start)
	}
	defer restore()

	fmt.Print("\033[?25l")
	idx := start
	renderMenu(os.Stdout, cfg, items, idx)
	redraw := func(i int) {
		fmt.Printf("\033[%dA", menuLines)
		renderMenu(os.Stdout, cfg, items, i)
	}
	sel, err := menuKeyLoop(bufio.NewReader(os.Stdin), len(items), start, redraw)
	if err != nil {
		fmt.Print("\033[?25h")
		fmt.Println()
		return "", err
	}
	fmt.Print("\033[?25h")
	clearMenu()
	if sel < 0 {
		return "quit", nil
	}
	return items[sel].action, nil
}

func menuKeyLoop(in *bufio.Reader, n, start int, render func(int)) (int, error) {
	idx := start
	for {
		b, err := in.ReadByte()
		if err != nil {
			return -1, err
		}
		switch {
		case b == '\r' || b == '\n':
			return idx, nil
		case b == 'q' || b == 'Q':
			return -1, nil
		case b == 'k':
			idx = (idx + n - 1) % n
		case b == 'j':
			idx = (idx + 1) % n
		case b >= '1' && b <= '9' && int(b-'1') < n:
			idx = int(b - '1')
		case b == 27:
			b2, err := in.ReadByte()
			if err != nil || b2 != '[' {
				continue
			}
			b3, err := in.ReadByte()
			if err != nil {
				continue
			}
			if b3 == 'A' {
				idx = (idx + n - 1) % n
			}
			if b3 == 'B' {
				idx = (idx + 1) % n
			}
		}
		render(idx)
	}
}

func clearMenu() {
	fmt.Printf("\033[%dA\033[J", menuLines)
}

func runMenuText(in io.Reader, out io.Writer, items []menuItem, start int) (string, error) {
	r := bufio.NewReader(in)
	for {
		fmt.Fprintf(out, "%s %s\n\n", bold("◆ wombat"), dim(Version))
		for i, it := range items {
			hint := ""
			if it.hint != "" {
				hint = "  " + dim(it.hint)
			}
			fmt.Fprintf(out, "  %d) %s%s\n", i+1, it.label, hint)
		}
		def := start + 1
		fmt.Fprintf(out, "\nselect [%d]: ", def)
		line, err := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if err != nil && line == "" {
			return "quit", nil
		}
		if line == "q" || line == "quit" {
			return "quit", nil
		}
		if line == "" {
			return items[start].action, nil
		}
		num := atoiDefault(line, 0)
		if num >= 1 && num <= len(items) {
			return items[num-1].action, nil
		}
		fmt.Fprintln(out, red("invalid choice"))
	}
}

func pause() {
	if !isTTY(os.Stdin) {
		return
	}
	fmt.Print(dim("\npress enter to return to menu…"))
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

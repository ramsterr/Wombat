package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// chromeLine places a left and right group on one line, keeping the right
// group visible even on narrow terminals.
func chromeLine(left, right string, width int) string {
	if width <= 0 {
		return left
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		return " " + clipANSI(left, max(1, width-2)) + " "
	}
	return " " + left + strings.Repeat(" ", gap) + right + " "
}

// rule draws a subtle divider across the terminal.
func rule(width int) string {
	if width <= 0 {
		return ""
	}
	return styleRule.Render(strings.Repeat("─", width))
}

// sectionLabel renders a small uppercase heading for a card or panel.
func sectionLabel(s string) string {
	return styleSection.Render(strings.ToUpper(s))
}

// statChip renders a compact label/value pair.
func statChip(label, value string) string {
	return styleChip.Render(label) + " " + styleTitle.Render(value)
}

// statusDot renders a coloured status dot and an optional label.
func statusDot(color lipgloss.TerminalColor, label string) string {
	dot := lipgloss.NewStyle().Foreground(color).Render("●")
	if label == "" {
		return dot
	}
	return dot + " " + styleDim.Render(label)
}

// shortPath collapses the user's home directory and clips long paths from
// the left, which keeps the interesting part of a deep path visible.
func shortPath(path string, width int) string {
	if home, err := os.UserHomeDir(); err == nil {
		if path == home {
			path = "~"
		} else if strings.HasPrefix(path, home+"/") {
			path = "~" + strings.TrimPrefix(path, home)
		}
	}
	if width <= 0 {
		return path
	}
	runes := []rune(path)
	if len(runes) <= width {
		return path
	}
	if width <= 1 {
		return string(runes[len(runes)-width:])
	}
	return "…" + string(runes[len(runes)-width+1:])
}

// GitBranch exposes the current branch to other frontends.
func GitBranch(dir string) string { return gitBranch(dir) }

// gitBranch reads the current branch from the nearest .git directory. It is
// deliberately cheap and silent: no git process, no errors on disk.
func gitBranch(dir string) string {
	for i := 0; i < 8 && dir != ""; i++ {
		head := filepath.Join(dir, ".git", "HEAD")
		if data, err := os.ReadFile(head); err == nil {
			line := strings.TrimSpace(string(data))
			if strings.HasPrefix(line, "ref: refs/heads/") {
				return strings.TrimPrefix(line, "ref: refs/heads/")
			}
			if len(line) >= 7 {
				return line[:7]
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// contextUsage renders the opencode-style context readout.
func contextUsage(used, total int) string {
	if total <= 0 {
		return styleFaint.Render("ctx —")
	}
	pct := used * 100 / total
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	return styleDim.Render(humanContext(used)) + styleFaint.Render(fmt.Sprintf(" (%d%%)", pct))
}

func humanContext(n int) string {
	if n >= 1_000_000 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1_000_000), ".0") + "M"
	}
	if n >= 1_000 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1_000), ".0") + "K"
	}
	return fmt.Sprintf("%d", n)
}

// modeBadge is the small mode chip used in the header.
func modeBadge(label string) string {
	return styleKey.Render(" " + strings.ToUpper(label) + " ")
}

// keyHint renders one keyboard hint: the key in the accent, the action quiet.
func keyHint(key, label string) string {
	return styleAccent.Bold(true).Render(key) + " " + styleFaint.Render(label)
}

// screenTabs is the compact navigation bar shared by the secondary screens.
func screenTabs(active screenID) string {
	items := []struct {
		id    screenID
		label string
	}{
		{scrChat, "chat"},
		{scrSessions, "sessions"},
		{scrProviders, "configure"},
		{scrModels, "models"},
		{scrUsage, "usage"},
		{scrDoctor, "doctor"},
	}
	parts := []string{styleBrand.Render("◈")}
	for _, it := range items {
		if it.id == active || (it.id == scrProviders && active == scrProviderForm) {
			parts = append(parts, styleButton.Render(" "+it.label+" "))
		} else {
			parts = append(parts, styleFaint.Render(it.label))
		}
	}
	return strings.Join(parts, " ")
}

// keyList renders a wrapped sequence of key hints.
func keyList(width int, pairs ...[2]string) string {
	var parts []string
	for _, p := range pairs {
		parts = append(parts, keyHint(p[0], p[1]))
	}
	if width <= 0 {
		return strings.Join(parts, "   ")
	}
	var lines []string
	line := ""
	for _, p := range parts {
		w := lipgloss.Width(p)
		if line == "" {
			line = p
			continue
		}
		if lipgloss.Width(line)+3+w > width {
			lines = append(lines, line)
			line = p
			continue
		}
		line += "   " + p
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// fitLines pads or trims a block of lines to an exact height.
func fitLines(lines []string, height int) []string {
	if height <= 0 {
		return nil
	}
	if len(lines) > height {
		return lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines
}

// centerBlock centres a multi-line block as one drawing. lipgloss.Place
// centres each line on its own, which would shear the wombat art and the
// block wordmark apart.
func centerBlock(width int, block string) string {
	if width <= 0 || block == "" {
		return block
	}
	lines := strings.Split(block, "\n")
	contentW := 0
	for _, line := range lines {
		if n := lipgloss.Width(line); n > contentW {
			contentW = n
		}
	}
	gap := width - contentW
	if gap <= 0 {
		return block
	}
	prefix := strings.Repeat(" ", gap/2)
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

// centerBlockIn centres a block horizontally and vertically inside a
// rectangle. The rectangle is filled with blank lines so the drawing sits
// exactly in the middle of the page.
func centerBlockIn(width, height int, block string) string {
	block = centerBlock(width, block)
	lines := strings.Split(block, "\n")
	if height <= 0 || len(lines) >= height {
		return strings.Join(lines, "\n")
	}
	top := (height - len(lines)) / 2
	bottom := height - len(lines) - top
	out := make([]string, 0, height)
	for i := 0; i < top; i++ {
		out = append(out, "")
	}
	out = append(out, lines...)
	for i := 0; i < bottom; i++ {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

// clipANSI truncates styled text to a width while preserving the visible
// content. It is a small ANSI-aware helper for one-line rows.
func clipANSI(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	var out strings.Builder
	visible := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			next := ansiEscapeEnd(s, i)
			out.WriteString(s[i:next])
			i = next
			continue
		}
		if visible >= width-1 {
			break
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if size <= 0 {
			break
		}
		out.WriteRune(r)
		visible++
		i += size
	}
	return out.String() + "\x1b[0m" + styleFaint.Render("…")
}

// elapsedLabel formats a run duration for the live status line.
func elapsedLabel(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

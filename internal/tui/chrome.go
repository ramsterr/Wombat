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

// accentRule is a divider with the theme's signature colours at both ends.
func accentRule(width int) string {
	if width <= 0 {
		return ""
	}
	if width < 8 {
		return styleAccent.Render(strings.Repeat("─", width))
	}
	left, right := 3, 3
	mid := width - left - right
	return styleAccent.Render(strings.Repeat("━", left)) +
		styleRule.Render(strings.Repeat("─", mid)) +
		styleAccent2.Render(strings.Repeat("━", right))
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

// miniBar renders a small horizontal progress bar.
func miniBar(used, total, cells int) string {
	if total <= 0 || cells <= 0 {
		return styleFaint.Render(strings.Repeat("▱", max(0, cells)))
	}
	pct := used * 100 / total
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	fill := pct * cells / 100
	if fill == 0 && used > 0 {
		fill = 1
	}
	color := styleGood
	switch {
	case pct >= 90:
		color = styleBad
	case pct >= 70:
		color = styleWarn
	}
	return color.Render(strings.Repeat("▰", fill)) +
		styleFaint.Render(strings.Repeat("▱", cells-fill))
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

// contextGauge renders a tiny usage meter for the status line.
func contextGauge(used, total int) string {
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
	bar := miniBar(used, total, 8)
	return styleFaint.Render("ctx ") + bar + " " + styleFaint.Render(fmt.Sprintf("%d%%", pct))
}

// contextGaugeTokens is the wider status gauge with absolute token counts.
func contextGaugeTokens(used, total int) string {
	if total <= 0 {
		return styleFaint.Render("context —")
	}
	pct := used * 100 / total
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	return styleFaint.Render("ctx ") + miniBar(used, total, 8) + " " +
		styleDim.Render(humanN(used)+"/"+humanN(total)) + " " +
		styleFaint.Render(fmt.Sprintf("%d%%", pct))
}

// modeBadge is the small mode chip used in the header.
func modeBadge(label string) string {
	return styleKey.Render(" " + strings.ToUpper(label) + " ")
}

// keyHint renders one keyboard hint: a key chip followed by a description.
func keyHint(key, label string) string {
	return styleKey.Render(" "+key+" ") + " " + styleFaint.Render(label)
}

// screenTabs is the compact navigation bar shared by the secondary screens.
func screenTabs(active screenID) string {
	items := []struct {
		id    screenID
		label string
	}{
		{scrChat, "chat"},
		{scrSessions, "sessions"},
		{scrModels, "models"},
		{scrUsage, "usage"},
		{scrDoctor, "doctor"},
	}
	parts := []string{styleBrand.Render("◈")}
	for _, it := range items {
		if it.id == active {
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

package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// welcome is the session-opening plate: the Wombat figure with a large block
// WOMBAT wordmark. The whole block lives in the transcript and scrolls upward
// as the conversation starts.
func (c *chat) welcome() string {
	w := c.contentWidth()
	if w <= 0 {
		return ""
	}
	fig := wombatColored(wombatFigure)
	word := wombatColored(wombatWordmarkLarge)
	figW := 0
	for _, line := range fig {
		figW = max(figW, lipgloss.Width(line))
	}
	wordW := 0
	for _, line := range word {
		wordW = max(wordW, lipgloss.Width(line))
	}

	var lines []string
	if w >= figW+4+wordW {
		padTop := max(0, (len(fig)-len(word))/2)
		wordBlock := strings.Repeat("\n", padTop) + strings.Join(word, "\n")
		block := lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(fig, "\n"), "    ", wordBlock)
		block = lipgloss.Place(w, lipgloss.Height(block), lipgloss.Center, lipgloss.Top, block)
		lines = strings.Split(block, "\n")
	} else {
		figBlock := lipgloss.Place(w, len(fig), lipgloss.Center, lipgloss.Top, strings.Join(fig, "\n"))
		lines = strings.Split(figBlock, "\n")
		lines = append(lines, "")
		for _, line := range word {
			lines = append(lines, lipgloss.PlaceHorizontal(w, lipgloss.Center, line))
		}
	}
	for i := range lines {
		lines[i] = clipANSI(lines[i], w)
	}
	return strings.Join(lines, "\n")
}

package tui

import "strings"

// welcome is the session-opening splash. It is the Wombat banner exactly as
// provided, shown once at the top of a new session.
func (c *chat) welcome() string {
	w := c.contentWidth()
	if w <= 0 {
		return ""
	}
	lines := wombatColored(wombatBanner)
	for i := range lines {
		lines[i] = clipANSI(lines[i], w)
	}
	return strings.Join(lines, "\n")
}

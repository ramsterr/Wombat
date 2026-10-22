package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	clipboard "github.com/ramsterr/Wombat/internal/clip"
)

// transcriptText collects the human-readable turns, newest last.
func (c *chat) transcriptText() string {
	var b strings.Builder
	for _, e := range c.entries {
		switch e.kind {
		case entryUser:
			if strings.TrimSpace(e.text) == "" {
				continue
			}
			b.WriteString("You:\n" + e.text + "\n\n")
		case entryAssistant:
			if strings.TrimSpace(e.text) == "" {
				continue
			}
			b.WriteString("Wombat:\n" + e.text + "\n\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// lastAssistantText is the most recent non-empty reply.
func (c *chat) lastAssistantText() string {
	for i := len(c.entries) - 1; i >= 0; i-- {
		e := c.entries[i]
		if e.kind == entryAssistant && strings.TrimSpace(e.text) != "" {
			return e.text
		}
	}
	return ""
}

// copyLastReply puts the last reply (or the whole transcript) on the clipboard.
func (c *chat) copyLastReply(all bool) tea.Cmd {
	text := c.lastAssistantText()
	what := "last reply"
	if all {
		text = c.transcriptText()
		what = "transcript"
	}
	if strings.TrimSpace(text) == "" {
		c.note("nothing to copy yet", true)
		return nil
	}
	if err := clipboard.Text(text); err != nil {
		c.note("copy failed: "+err.Error(), true)
		return nil
	}
	c.note("copied "+what+" to the clipboard", false)
	return nil
}

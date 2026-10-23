package tui

import (
	"encoding/base64"
	"fmt"
	"os"
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

// copyItem is one selectable block in the copy picker.
type copyItem struct {
	label string
	text  string
}

// copyableMessages returns recent user, agent, and tool output, newest first.
func (c *chat) copyableMessages() []copyItem {
	var out []copyItem
	for i := len(c.entries) - 1; i >= 0 && len(out) < 20; i-- {
		e := c.entries[i]
		switch e.kind {
		case entryUser:
			if strings.TrimSpace(e.text) != "" {
				out = append(out, copyItem{label: "you", text: e.text})
			}
		case entryAssistant:
			if strings.TrimSpace(e.text) != "" {
				out = append(out, copyItem{label: "wombat", text: e.text})
			}
		case entryTool:
			if strings.TrimSpace(e.result) != "" {
				label := "tool · " + e.name
				out = append(out, copyItem{label: label, text: e.result})
			}
		}
	}
	return out
}

// copyToClipboard writes text to the system clipboard, falling back to the
// terminal's OSC 52 clipboard when the platform helper is unavailable.
func (c *chat) copyToClipboard(text, what string) tea.Cmd {
	if strings.TrimSpace(text) == "" {
		c.note("nothing to copy yet", true)
		return nil
	}
	if err := clipboard.Text(text); err == nil {
		c.note("copied "+what+" to the clipboard", false)
		return nil
	}
	fmt.Fprintf(os.Stdout, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text)))
	c.note("copied "+what+" via the terminal clipboard", false)
	return nil
}

// copyLastReply puts the last reply on the clipboard.
func (c *chat) copyLastReply(all bool) tea.Cmd {
	if all {
		return c.copyToClipboard(c.transcriptText(), "transcript")
	}
	return c.copyToClipboard(c.lastAssistantText(), "last reply")
}

// openCopyPicker lists recent replies so any of them can be copied.
func (c *chat) openCopyPicker() tea.Cmd {
	items := c.copyableMessages()
	if len(items) == 0 {
		c.note("nothing to copy yet", true)
		return nil
	}
	c.copyItems = items
	c.copySel = 0
	c.modal = modalCopy
	c.clearPalette()
	c.closeFilePalette()
	c.layout()
	c.refresh()
	return nil
}

func (c *chat) copyKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		c.modal = modalNone
		c.layout()
		c.refresh()
	case "up", "k", "ctrl+p":
		if c.copySel > 0 {
			c.copySel--
		}
		c.layout()
		c.refresh()
	case "down", "j", "ctrl+n":
		if c.copySel < len(c.copyItems)-1 {
			c.copySel++
		}
		c.layout()
		c.refresh()
	case "enter":
		if c.copySel < 0 || c.copySel >= len(c.copyItems) {
			return nil
		}
		item := c.copyItems[c.copySel]
		c.modal = modalNone
		return c.copyToClipboard(item.text, item.label)
	case "a":
		c.modal = modalNone
		return c.copyToClipboard(c.transcriptText(), "transcript")
	}
	return nil
}

func (c *chat) copyCard() string {
	mainW := c.mainWidth()
	cardW := min(max(44, mainW-8), 84)
	innerW := max(30, cardW-6)
	lines := []string{
		styleAccent.Bold(true).Render("◈  COPY") + "  " + styleFaint.Render(itoa(len(c.copyItems))+" replies"),
		styleDim.Render("pick a reply with ↑↓; enter copies it"),
		"",
	}
	limit := min(len(c.copyItems), max(3, c.vp.Height-8))
	start := 0
	if c.copySel >= limit {
		start = c.copySel - limit + 1
	}
	end := min(len(c.copyItems), start+limit)
	for i := start; i < end; i++ {
		item := c.copyItems[i]
		snippet := clip(oneline(item.text), max(12, innerW-18))
		text := fmt.Sprintf("%2d  %-14s %s", i+1, clip(item.label, 14), snippet)
		if i == c.copySel {
			lines = append(lines, styleSelected.Render(" > "+pad(text, innerW-3)))
		} else {
			lines = append(lines, "   "+styleDim.Render(text))
		}
	}
	if end < len(c.copyItems) {
		lines = append(lines, styleFaint.Render(fmt.Sprintf("  … %d more below", len(c.copyItems)-end)))
	}
	lines = append(lines, "", " "+styleFaint.Render("↑↓ move · enter copy · a copy all · esc close"))
	for i := range lines {
		lines[i] = clipANSI(lines[i], innerW)
	}
	return styleModal.Width(innerW + 4).Render(strings.Join(lines, "\n"))
}

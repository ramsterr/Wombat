package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ramsterr/Wombat/internal/modelinfo"
)

var reasoningLevels = []string{"off", "low", "medium", "high", "max"}

// maybeOpenReasoningPicker shows the reasoning levels right after a model is
// selected, when the model supports them.
func (c *chat) maybeOpenReasoningPicker() tea.Cmd {
	if c.provName == "" || c.modelName == "" {
		return nil
	}
	info, ok := modelinfo.Lookup(c.provName, c.modelName)
	supported := ok && info.Reasoning
	if !ok {
		// A catalogue miss should not hide the control on models that
		// obviously reason.
		lower := strings.ToLower(c.modelName)
		supported = strings.Contains(lower, "o1") || strings.Contains(lower, "o3") ||
			strings.Contains(lower, "o4") || strings.Contains(lower, "gpt-5") ||
			strings.Contains(lower, "reason")
	}
	if !supported {
		return nil
	}
	c.reasoningOptions = reasoningLevels
	current := strings.ToLower(strings.TrimSpace(c.cfg.Reasoning))
	if current == "" {
		current = "medium"
	}
	c.reasoningSel = 0
	for i, level := range c.reasoningOptions {
		if level == current {
			c.reasoningSel = i
			break
		}
	}
	c.modal = modalReasoning
	c.layout()
	c.refresh()
	return nil
}

func (c *chat) reasoningKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		c.modal = modalNone
		c.layout()
		c.refresh()
	case "up", "k", "ctrl+p":
		if c.reasoningSel > 0 {
			c.reasoningSel--
		}
		c.layout()
		c.refresh()
	case "down", "j", "ctrl+n":
		if c.reasoningSel < len(c.reasoningOptions)-1 {
			c.reasoningSel++
		}
		c.layout()
		c.refresh()
	case "enter":
		c.applyReasoning()
	}
	if len(msg.Runes) == 1 {
		if n := int(msg.Runes[0] - '1'); n >= 0 && n < len(c.reasoningOptions) {
			c.reasoningSel = n
			c.applyReasoning()
		}
	}
	return nil
}

func (c *chat) applyReasoning() {
	if c.reasoningSel < 0 || c.reasoningSel >= len(c.reasoningOptions) {
		return
	}
	level := c.reasoningOptions[c.reasoningSel]
	c.cfg.Reasoning = level
	_ = c.cfg.Save()
	c.modal = modalNone
	c.note("reasoning: "+level, false)
	c.layout()
	c.refresh()
}

func (c *chat) reasoningCard() string {
	mainW := c.mainWidth()
	cardW := min(max(40, mainW-10), 56)
	innerW := max(28, cardW-6)
	lines := []string{
		styleAccent.Bold(true).Render("◈  REASONING") + "  " + styleFaint.Render(clip(c.modelName, innerW-14)),
		styleDim.Render("how much thinking this model should do"),
		"",
	}
	desc := map[string]string{
		"off":    "no extra thinking",
		"low":    "quick, light reasoning",
		"medium": "balanced (default)",
		"high":   "deeper reasoning",
		"max":    "the deepest setting",
	}
	for i, level := range c.reasoningOptions {
		text := fmt.Sprintf("%d) %-7s %s", i+1, level, desc[level])
		if i == c.reasoningSel {
			lines = append(lines, styleSelected.Render(" > "+pad(text, innerW-3)))
		} else {
			lines = append(lines, "   "+styleDim.Render(text))
		}
	}
	lines = append(lines, "", " "+styleFaint.Render("enter apply · esc keep current"))
	for i := range lines {
		lines[i] = clipANSI(lines[i], innerW)
	}
	return styleModal.Width(innerW + 4).Render(strings.Join(lines, "\n"))
}

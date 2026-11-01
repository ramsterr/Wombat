package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/ramsterr/Wombat/internal/todo"
)

// maxColumn is the widest the conversation column grows. Wide terminals get
// quiet gutters instead of very long lines, the way opencode reads.
const maxColumn = 104

// columnWidth is the conversation column: the transcript, prompt, and footer
// chrome all share this width and are centred inside the terminal.
func (c *chat) columnWidth() int {
	if c.w <= 0 {
		return 0
	}
	w := c.w - 4
	if w > maxColumn {
		w = maxColumn
	}
	if w < 16 {
		w = c.w
	}
	return max(1, w)
}

// mainWidth is the width of the main pane, which is the conversation column.
func (c *chat) mainWidth() int { return max(1, c.columnWidth()) }

// contentWidth is the transcript width, excluding the one-column scrollbar.
// Overlays use the full main pane.
func (c *chat) contentWidth() int {
	if c.modal != modalNone {
		return max(20, c.columnWidth())
	}
	return max(20, c.columnWidth()-1)
}

// bodyView renders the transcript with a slim scrollbar on its right edge.
func (c *chat) bodyView() string {
	if c.w <= 0 || c.h <= 0 {
		return ""
	}
	lines := strings.Split(c.vp.View(), "\n")
	bars := c.scrollbarLines(len(lines))
	for i := range lines {
		if i < len(bars) {
			lines[i] += bars[i]
		}
	}
	return strings.Join(lines, "\n")
}

// scrollbarLines draws the viewport track and thumb. It is intentionally
// quiet when the transcript fits, and bright while there is more to read.
func (c *chat) scrollbarLines(height int) []string {
	out := make([]string, max(0, height))
	total := c.vp.TotalLineCount()
	if total <= height || height <= 0 {
		for i := range out {
			out[i] = " "
		}
		return out
	}
	thumb := max(1, height*height/total)
	maxOff := total - height
	top := 0
	if maxOff > 0 {
		top = c.vp.YOffset * (height - thumb) / maxOff
	}
	if top < 0 {
		top = 0
	}
	if top+thumb > height {
		top = height - thumb
	}
	for i := range out {
		if i >= top && i < top+thumb {
			out[i] = styleAccent2.Render("┃")
		} else {
			out[i] = styleFaint.Render("│")
		}
	}
	return out
}

// footerMark is the WOMBAT mark that anchors the bottom of the chat: three
// block rows when the terminal is tall enough, one quiet line otherwise.
func (c *chat) footerMark() []string {
	if c.w >= lipgloss.Width(wombatWordmark[0])+4 && c.h >= 30 {
		return wombatGradient(wombatWordmark)
	}
	label := "WOMBAT"
	if v := versionLabel(c.opt.Version); v != "" && c.w >= 44 {
		label += "  " + v
	}
	return []string{styleAccent.Bold(true).Render(label)}
}

// footerHeight is the rule, the status line, and the wordmark. Very short
// terminals keep only the wordmark so the prompt still has room to breathe.
func (c *chat) footerHeight() int {
	if c.w <= 0 {
		return 0
	}
	if c.h > 0 && c.h < 10 {
		return 1
	}
	return 2 + len(c.footerMark())
}

// footerView pins the wordmark to the bottom of the chat and keeps the live
// status just above it, in the spirit of opencode's footer.
func (c *chat) footerView() string {
	if c.w <= 0 {
		return ""
	}
	mark := c.footerMark()
	for i := range mark {
		mark[i] = clipANSI(mark[i], c.w)
	}
	block := centerBlock(c.w, strings.Join(mark, "\n"))
	if c.h > 0 && c.h < 10 {
		return block
	}
	lines := []string{rule(c.w), c.statusLine()}
	lines = append(lines, strings.Split(block, "\n")...)
	return strings.Join(lines, "\n")
}

// statusLine places the contextual key hints on the left and the model,
// mode, and context gauge on the right. The model readout wins when space is
// tight, because it is the part people look for most often.
func (c *chat) statusLine() string {
	left := c.hint()
	right := c.footerMeta()
	if c.w <= 0 {
		return " " + left
	}
	if lipgloss.Width(right) > c.w-4 {
		right = ""
	}
	if lipgloss.Width(left)+lipgloss.Width(right)+4 > c.w {
		avail := c.w - lipgloss.Width(right) - 4
		if avail >= 1 {
			left = clipANSI(left, avail)
		} else {
			right = ""
		}
	}
	return chromeLine(left, right, c.w)
}

// footerMeta is the compact session readout in the footer.
func (c *chat) footerMeta() string {
	var parts []string
	model := c.provName + "/" + c.modelName
	if model == "/" || strings.Trim(model, "/") == "" {
		model = "no model"
	}
	if c.w >= 58 {
		parts = append(parts, styleDim.Render(clip(model, 30)))
	}
	if c.missingKey != "" {
		parts = append(parts, styleWarn.Render("no key"))
	}
	if c.auto {
		parts = append(parts, styleWarn.Render("yolo"))
	}
	if c.w >= 72 {
		parts = append(parts, modeBadge(c.modeLabel()))
	}
	if c.window > 0 && c.w >= 56 {
		if c.w >= 96 {
			parts = append(parts, contextGaugeTokens(c.history, c.window))
		} else {
			parts = append(parts, contextGauge(c.history, c.window))
		}
	}
	if v := versionLabel(c.opt.Version); v != "" && c.w >= 110 {
		parts = append(parts, styleFaint.Render(v))
	}
	return strings.Join(parts, "  ")
}

// versionLabel is the short version shown beside the footer mark.
func versionLabel(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// todos returns the current task plan.
func (c *chat) todos() []todo.Item {
	if c.ag == nil {
		return nil
	}
	if c.ag.Plan != nil {
		return c.ag.Plan()
	}
	if c.ag.Env == nil {
		return nil
	}
	return c.ag.Env.Todos()
}

// todoBarVisible keeps the compact plan visible above the prompt.
func (c *chat) todoBarVisible() bool {
	return len(c.todos()) > 0
}

// todoBar is the one-line plan shown above the prompt.
func (c *chat) todoBar() string {
	if !c.todoBarVisible() {
		return ""
	}
	items := c.todos()
	done, total := todo.Counts(items)
	current := ""
	for _, item := range items {
		if item.Status == todo.InProgress {
			current = item.Content
			break
		}
	}
	if current == "" {
		for _, item := range items {
			if item.Status == todo.Pending {
				current = item.Content
				break
			}
		}
	}
	w := c.columnWidth()
	left := styleAccent2.Render("☰ todos ") + styleDim.Render(fmt.Sprintf("%d/%d", done, total))
	if current != "" {
		left += "  " + styleFaint.Render(clip(current, max(12, w-20)))
	}
	return lipgloss.NewStyle().Background(cPanel).Width(w).Render(clipANSI(" "+left+" ", w))
}

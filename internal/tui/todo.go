package tui

import (
	"fmt"

	"github.com/ramsterr/Wombat/internal/todo"
)

// todoSection renders the session's live todo list. It is the anchor the user
// watches while the plan and its parallel workers run.
func (c *chat) todoSection(width int) []string {
	if c.todos == nil {
		return nil
	}
	_ = c.todos.Reload()
	items := c.todos.Items()
	if len(items) == 0 {
		return nil
	}
	done, total := c.todos.Progress()
	lines := []string{styleFaint.Render(fmt.Sprintf("%d/%d done", done, total))}
	if total > 0 {
		lines = append(lines, miniBar(done, total, max(6, width-2)))
	}
	limit := 8
	if len(items) > limit {
		items = items[:limit]
	}
	for _, item := range items {
		glyph, style := todoGlyph(item.Status)
		content := oneLineText(item.Content, max(8, width-4))
		lines = append(lines, style.Render(glyph)+" "+styleDim.Render(clip(content, max(8, width-4))))
	}
	if total > limit {
		lines = append(lines, styleFaint.Render(fmt.Sprintf("+%d more", total-limit)))
	}
	return lines
}

func todoGlyph(status todo.Status) (string, interface{ Render(...string) string }) {
	switch status {
	case todo.Completed:
		return "✓", styleGood
	case todo.InProgress:
		return "▸", styleWarn
	case todo.Blocked:
		return "!", styleBad
	case todo.Cancelled:
		return "×", styleFaint
	default:
		return "○", styleFaint
	}
}

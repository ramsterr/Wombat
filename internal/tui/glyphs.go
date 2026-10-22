package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// toolGlyph returns a compact icon and colour for a tool. Workers prefix their
// tool names with "worker:", so the prefix is stripped before matching.
func toolGlyph(name string) (string, lipgloss.Style) {
	base := name
	if i := strings.LastIndex(base, ":"); i >= 0 {
		base = base[i+1:]
	}
	switch base {
	case "bash", "shell", "run", "job":
		return "❯", styleGood
	case "read", "view":
		return "◫", styleLink
	case "write", "create":
		return "✚", styleAccent2
	case "edit", "multi_edit", "apply_patch", "replace", "refactor", "rename":
		return "✎", styleAccent
	case "glob", "grep", "search", "symbols", "semantic_search", "find":
		return "⌕", styleLink
	case "websearch", "webfetch":
		return "⌁", styleLink
	case "browser", "screenshot":
		return "◉", styleAccent2
	case "image", "imagegen", "image_edit":
		return "▣", styleSandbox
	case "delegate_many", "delegate", "delegate_write", "worker":
		return "⑂", styleSandbox
	case "resume_worker":
		return "↻", styleSandbox
	case "update_plan", "run_plan", "plan":
		return "☰", styleAccent2
	case "question", "ask":
		return "?", styleWarn
	case "lsp", "diagnostics", "completions":
		return "λ", styleAccent2
	case "checkpoint", "undo", "restore":
		return "◷", styleWarn
	case "verify_site", "verify", "checks":
		return "✓", styleGood
	case "submit_report", "submit_review":
		return "✦", styleAccent
	default:
		return "•", styleDim
	}
}

// resultStyle colours a line of tool output, including unified diffs.
func resultStyle(line string) lipgloss.Style {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "+++"), strings.HasPrefix(trimmed, "---"):
		return styleFaint
	case strings.HasPrefix(trimmed, "@@"):
		return styleDiffHunk
	case strings.HasPrefix(trimmed, "+"):
		return styleDiffAdd
	case strings.HasPrefix(trimmed, "-"):
		return styleDiffDel
	}
	return styleFaint
}

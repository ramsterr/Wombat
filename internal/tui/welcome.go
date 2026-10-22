package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// welcome is the landing screen: the wombat mark, the wordmark, and the
// workspace facts. It deliberately has no key list or framed card; the footer
// already carries the keys.
func (c *chat) welcome() string {
	w := c.contentWidth()
	h := c.vp.Height
	if w <= 0 || h <= 0 {
		return ""
	}
	title := styleAccent.Bold(true).Render("W O M B A T")
	subtitle := styleDim.Render("a coding agent for your terminal")

	if h < 11 || w < 20 {
		rows := []string{title, subtitle, "", styleFaint.Render("type a message, or / for commands")}
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, strings.Join(rows, "\n"))
	}

	var art []string
	switch {
	case h >= 46 && w >= 91:
		art = wombatFull
	case h >= 26 && w >= 47:
		art = wombatMedium
	case h >= 18 && w >= 47:
		art = wombatSmall
	case h >= 14 && w >= 25:
		art = wombatMini
	case h >= 11 && w >= 20:
		art = wombatTiny
	}

	info := c.welcomeInfo(min(w-2, 80))
	var content []string
	if len(art) > 0 {
		artBlock := strings.Join(wombatColored(art), "\n")
		artBlock = lipgloss.Place(w, len(art), lipgloss.Center, lipgloss.Top, artBlock)
		content = append(content, strings.Split(artBlock, "\n")...)
	}
	content = append(content, lipgloss.PlaceHorizontal(w, lipgloss.Center, title))
	content = append(content, strings.Split(lipgloss.Place(w, lipgloss.Height(info), lipgloss.Center, lipgloss.Top, info), "\n")...)
	if h >= len(content)+2 {
		content = append(content, "", lipgloss.PlaceHorizontal(w, lipgloss.Center, subtitle))
	}
	block := strings.Join(content, "\n")
	if lipgloss.Width(block) > w {
		block = clipANSI(block, w)
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, block)
}

// welcomeInfo is the compact two-column metadata block.
func (c *chat) welcomeInfo(width int) string {
	model := c.provName + "/" + c.modelName
	if strings.TrimSpace(model) == "/" || strings.TrimSpace(model) == "" {
		model = "not configured"
	}
	colW := max(18, width/2)
	cell := func(label, value string) string {
		return styleFaint.Render(pad(label, 8)) + value
	}
	row := func(a, b string) string {
		return padRight(a, colW) + b
	}
	return strings.Join([]string{
		row(cell("theme", styleAccent.Render(themeLabel)), cell("model", styleTitle.Render(clip(model, max(8, colW-9))))),
		row(cell("mode", styleAccent.Render(c.modeLabel())), cell("dir", styleDim.Render(shortPath(c.opt.Cwd, max(8, colW-9))))),
	}, "\n")
}

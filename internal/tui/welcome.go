package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var welcomeTips = []string{
	"type / to browse every command, with options inline",
	"type @ to pick a file, then Tab to complete the path",
	"ctrl+b opens the inspector; ctrl+o opens the newest preview",
	"ctrl+t cycles themes; /theme opens the live picker",
	"esc stops a run; your work is checkpointed first",
	"/sessions resumes an earlier conversation in this directory",
	"ctrl+y copies the last reply; /mouse restores normal selection",
	"workers run in parallel git worktrees — ideal for independent files",
}

// welcome is the first screen: a field-guide plate with the theme's wombat,
// the workspace facts, and the few keys worth knowing immediately.
func (c *chat) welcome() string {
	w := c.contentWidth()
	h := c.vp.Height
	if w <= 0 || h <= 0 {
		return ""
	}
	left, right := themeGradient()
	mark := themeMark(activeTheme)
	title := gradientText(mark+"  W O M B A T", left, right)
	subtitle := styleDim.Render("a coding agent for your terminal")
	if c.opt.Version != "" {
		subtitle += styleFaint.Render("  ·  " + c.opt.Version)
	}

	if h < 12 || w < 40 {
		rows := []string{title, subtitle, "", styleFaint.Render("type a message, or / for commands")}
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, strings.Join(rows, "\n"))
	}

	full := w >= 70 && (h >= 24 || (h >= 17 && h < 21))
	size := artSmall
	if full {
		size = artFull
	}
	artW := 36
	if full {
		artW = 60
	}
	cardW := min(max(44, w-2), 80)
	innerW := cardW - 6
	if innerW < artW && size == artFull {
		size = artSmall
		artW = 36
	}
	if innerW < artW {
		size = artMini
		artW = 24
	}
	art := themeArt(activeTheme, size)

	info := c.welcomeInfo(innerW, full)
	keys := keyList(innerW,
		[2]string{"/", "commands"},
		[2]string{"@", "files"},
		[2]string{"ctrl+b", "inspector"},
		[2]string{"ctrl+g", "help"},
	)
	if lipgloss.Height(keys) > 1 {
		keys = keyList(innerW, [2]string{"/", "commands"}, [2]string{"@", "files"}, [2]string{"ctrl+g", "help"})
	}

	artBlock := strings.Join(art, "\n")
	artBlock = lipgloss.Place(innerW, len(art), lipgloss.Center, lipgloss.Top, artBlock)
	infoBlock := lipgloss.Place(innerW, lipgloss.Height(info), lipgloss.Center, lipgloss.Top, info)
	keysBlock := lipgloss.Place(innerW, lipgloss.Height(keys), lipgloss.Center, lipgloss.Top, keys)

	compactFull := full && h < 21
	var content []string
	if compactFull {
		info = c.welcomeInfo(innerW, false)
		infoBlock = lipgloss.Place(innerW, lipgloss.Height(info), lipgloss.Center, lipgloss.Top, info)
		content = []string{lipgloss.PlaceHorizontal(innerW, lipgloss.Center, title)}
		content = append(content, strings.Split(artBlock, "\n")...)
		content = append(content, strings.Split(infoBlock, "\n")...)
		content = append(content, strings.Split(keysBlock, "\n")...)
	} else {
		content = []string{
			lipgloss.PlaceHorizontal(innerW, lipgloss.Center, title),
			lipgloss.PlaceHorizontal(innerW, lipgloss.Center, subtitle),
		}
		content = append(content, strings.Split(artBlock, "\n")...)
		content = append(content, "")
		content = append(content, strings.Split(infoBlock, "\n")...)
		content = append(content, "")
		content = append(content, strings.Split(keysBlock, "\n")...)
		if !full && h >= 24 && innerW >= 54 {
			content = append(content, "",
				styleFaint.Render("tip  ")+styleDim.Render(clip(welcomeTips[int(time.Now().Unix()/45)%len(welcomeTips)], innerW-6)))
		}
	}
	block := strings.Join(content, "\n")

	useCard := h >= 21 && w >= 56
	if useCard {
		hero := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cAccent).
			Padding(1, 2).
			Width(innerW + 4)
		block = hero.Render(block)
	}
	if lipgloss.Width(block) > w {
		block = clipANSI(block, w)
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, block)
}

// welcomeInfo is the two-column metadata block.
func (c *chat) welcomeInfo(width int, full bool) string {
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
	rows := []string{
		row(cell("theme", styleAccent.Render(themeLabel)), cell("model", styleTitle.Render(clip(model, max(8, colW-9))))),
		row(cell("mode", styleAccent.Render(c.modeLabel())), cell("dir", styleDim.Render(shortPath(c.opt.Cwd, max(8, colW-9))))),
	}
	if full {
		branch := "—"
		if c.branch != "" {
			branch = c.branch
		}
		rows = append(rows, row(cell("branch", styleAccent2.Render(clip(branch, max(8, colW-9)))),
			cell("session", styleDim.Render(clip(c.sessionID(), max(8, colW-9))))))
	}
	return strings.Join(rows, "\n")
}

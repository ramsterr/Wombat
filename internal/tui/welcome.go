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
	"ctrl+t cycles themes; /theme opens the picker",
	"esc stops a run; your work is checkpointed first",
	"/sessions resumes an earlier conversation in this directory",
	"ctrl+y copies the last reply; /copy picks any reply",
	"text selection works by default; /mouse enables wheel scrolling",
	"workers run in parallel git worktrees — ideal for independent files",
}

// welcome is the landing screen: one sitting wombat, the wordmark, the
// workspace facts, and the keys worth knowing immediately.
func (c *chat) welcome() string {
	w := c.contentWidth()
	h := c.vp.Height
	if w <= 0 || h <= 0 {
		return ""
	}
	title := styleAccent.Bold(true).Render("W O M B A T")
	subtitle := styleDim.Render("a coding agent for your terminal")
	if c.opt.Version != "" {
		subtitle += styleFaint.Render("  ·  " + c.opt.Version)
	}

	if h < 12 || w < 40 {
		rows := []string{title, subtitle, "", styleFaint.Render("type a message, or / for commands")}
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, strings.Join(rows, "\n"))
	}

	showArt := h >= 18 && w >= 52
	cardW := min(max(44, w-4), 70)
	innerW := max(32, cardW-6)
	info := c.welcomeInfo(innerW, h >= 24)
	keys := keyList(innerW,
		[2]string{"/", "commands"},
		[2]string{"@", "files"},
		[2]string{"ctrl+y", "copy"},
		[2]string{"ctrl+g", "help"},
	)
	if lipgloss.Height(keys) > 1 {
		keys = keyList(innerW, [2]string{"/", "commands"}, [2]string{"@", "files"}, [2]string{"ctrl+g", "help"})
	}

	compact := h < 20
	var content []string
	if showArt {
		art := strings.Join(wombatArt(), "\n")
		art = lipgloss.Place(innerW, len(wombatMascot), lipgloss.Center, lipgloss.Top, art)
		content = append(content, strings.Split(art, "\n")...)
		if !compact {
			content = append(content, "")
		}
	}
	content = append(content, lipgloss.PlaceHorizontal(innerW, lipgloss.Center, title))
	if !compact {
		content = append(content, lipgloss.PlaceHorizontal(innerW, lipgloss.Center, subtitle), "")
	} else {
		content = append(content, "")
	}
	content = append(content, strings.Split(lipgloss.Place(innerW, lipgloss.Height(info), lipgloss.Center, lipgloss.Top, info), "\n")...)
	if !compact {
		content = append(content, "")
	}
	content = append(content, strings.Split(lipgloss.Place(innerW, lipgloss.Height(keys), lipgloss.Center, lipgloss.Top, keys), "\n")...)
	if h >= 26 && innerW >= 54 {
		content = append(content, "",
			styleFaint.Render("tip  ")+styleDim.Render(clip(welcomeTips[int(time.Now().Unix()/45)%len(welcomeTips)], innerW-6)))
	}
	block := strings.Join(content, "\n")

	if h >= 23 && w >= 56 {
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

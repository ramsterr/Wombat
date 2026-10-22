package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// mascot is the small terminal wombat. It is deliberately compact so it still
// fits above the input on a 24-row terminal.
var mascot = []string{
	"      ▄▄         ▄▄",
	"     ▟██▙       ▟██▙",
	"    ▟███████████████▙",
	"   ▐█████████████████▌",
	"   ▐███   ◉     ◉   ███▌",
	"    ▜███████████████▛",
	"     ▝▀▀█████████▀▀▘",
	"          ▀▀▀▀▀",
}

var welcomeTips = []string{
	"type / to browse every command, with options inline",
	"type @ to pick a file, then Tab to complete the path",
	"ctrl+b opens the inspector; ctrl+o opens the live preview",
	"ctrl+t cycles themes; /theme opens the full picker",
	"esc stops a run; your work is checkpointed first",
	"/sessions resumes an earlier conversation in this directory",
	"paste a screenshot with ctrl+v and ask about it",
	"workers run in parallel git worktrees — ideal for independent files",
}

// welcome is the first screen: a calm, informative landing card. It keeps
// the most useful settings visible without turning the chat into a dashboard.
func (c *chat) welcome() string {
	w := c.contentWidth()
	h := c.vp.Height
	if w <= 0 || h <= 0 {
		return ""
	}
	compact := w < 56 || h < 15
	sideBySide := w >= 100 && h >= 18

	model := c.provName + "/" + c.modelName
	if strings.TrimSpace(model) == "/" || strings.TrimSpace(model) == "" {
		model = "not configured"
	}
	session := c.sessionID()
	if session == "new" {
		session = "fresh session"
	}
	cwd := shortPath(c.opt.Cwd, max(10, w-16))

	left, right := themeGradient()
	var title string
	if compact {
		title = styleBrand.Render("◈ ") + gradientText("WOMBAT", left, right)
	} else {
		title = gradientText("◈  W O M B A T", left, right)
	}
	subtitle := styleDim.Render("a coding agent for your terminal")
	if c.opt.Version != "" {
		subtitle += styleFaint.Render("  ·  " + c.opt.Version)
	}

	infoWidth := max(24, w-6)
	rows := []string{
		title,
		subtitle,
		"",
		styleFaint.Render("model    ") + styleTitle.Render(clip(model, infoWidth-10)),
		styleFaint.Render("mode     ") + styleAccent.Render(c.modeLabel()) +
			styleFaint.Render("   ·   theme ") + styleDim.Render(themeLabel),
		styleFaint.Render("session  ") + styleDim.Render(clip(session, infoWidth-10)),
		styleFaint.Render("dir      ") + styleDim.Render(cwd),
	}
	if c.branch != "" {
		rows = append(rows, styleFaint.Render("branch   ")+styleAccent2.Render(clip(c.branch, infoWidth-10)))
	}
	rows = append(rows,
		"",
		keyList(min(infoWidth, 70),
			[2]string{"/", "commands"},
			[2]string{"@", "files"},
			[2]string{"ctrl+b", "inspector"},
			[2]string{"ctrl+o", "preview"},
			[2]string{"ctrl+g", "help"},
		),
		"",
		styleFaint.Render("tip  ")+styleDim.Render(clip(welcomeTips[int(time.Now().Unix()/45)%len(welcomeTips)], infoWidth-6)),
		"",
		styleFaint.Render("type a message to begin"),
	)

	if h < 14 {
		rows = []string{
			title,
			subtitle,
			"",
			styleFaint.Render("type a message, or / for commands"),
		}
	}
	info := strings.Join(rows, "\n")
	card := info
	var art []string
	for _, line := range mascot {
		art = append(art, gradientText(line, left, right))
	}
	artBlock := strings.Join(art, "\n")
	switch {
	case sideBySide:
		card = lipgloss.JoinHorizontal(lipgloss.Center,
			artBlock,
			strings.Repeat(" ", 3),
			info,
		)
	case !compact && h >= 26:
		card = lipgloss.JoinVertical(lipgloss.Center, artBlock, "", info)
	}
	if lipgloss.Width(card) > w {
		card = clipANSI(card, w)
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, card)
}

package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ramsterr/Wombat/internal/commands"
)

// runDuration is the elapsed time of the current turn.
func (c *chat) runDuration() time.Duration {
	if c.runStart.IsZero() {
		return 0
	}
	return time.Since(c.runStart)
}

// openHelp shows the keyboard and command reference.
func (c *chat) openHelp() tea.Cmd {
	c.modal = modalHelp
	c.helpTop = 0
	c.clearPalette()
	c.closeFilePalette()
	c.layout()
	c.refresh()
	return nil
}

// openThemePicker previews themes as the selection moves.
func (c *chat) openThemePicker() tea.Cmd {
	c.modal = modalTheme
	c.themeOrig = activeTheme
	c.themeSel = 0
	for i, name := range themeOrder {
		if name == activeTheme {
			c.themeSel = i
		}
	}
	c.closeFilePalette()
	c.layout()
	c.refresh()
	return nil
}

// previewTheme applies a theme without saving it. It is used for live hover
// in the theme picker.
func (c *chat) previewTheme(name string) {
	if _, ok := themeCatalog[name]; !ok {
		return
	}
	applyTheme(name)
	c.applyThemeStyles()
	c.layout()
	c.refresh()
}

// commitTheme applies and persists the highlighted theme.
func (c *chat) commitTheme() {
	if c.themeSel < 0 || c.themeSel >= len(themeOrder) {
		return
	}
	name := themeOrder[c.themeSel]
	c.setTheme(name)
	c.modal = modalNone
	c.note("theme: "+themeLabel+" ("+name+")", false)
	c.layout()
	c.refresh()
}

// restoreAndClose reverses a theme preview and closes the overlay.
func (c *chat) restoreAndClose() {
	if c.modal == modalTheme && c.themeOrig != "" {
		c.previewTheme(c.themeOrig)
	}
	c.modal = modalNone
	c.helpTop = 0
	c.layout()
	c.refresh()
}

// modalKey handles keys for the help and theme overlays.
func (c *chat) modalKey(msg tea.KeyMsg) tea.Cmd {
	if c.modal == modalCopy {
		return c.copyKey(msg)
	}
	if c.modal == modalPreviews {
		return c.previewKey(msg)
	}
	if c.modal == modalSources {
		return c.sourceKey(msg)
	}
	if c.modal == modalReference {
		switch msg.String() {
		case "esc", "ctrl+c":
			c.refInput.Blur()
			c.modal = modalNone
			c.layout()
			c.refresh()
		case "enter":
			value := strings.TrimSpace(c.refInput.Value())
			if value != "" {
				if err := c.addReference(value); err != nil {
					c.note("error: "+err.Error(), true)
				} else {
					c.note("reference added: "+value, false)
				}
			}
			c.refInput.Reset()
			c.refInput.Blur()
			c.modal = modalNone
			c.layout()
			c.refresh()
		default:
			var cmd tea.Cmd
			c.refInput, cmd = c.refInput.Update(msg)
			return cmd
		}
		return nil
	}
	if c.modal == modalTheme {
		switch msg.String() {
		case "esc", "q", "ctrl+c":
			c.restoreAndClose()
		case "enter":
			c.commitTheme()
		case "up", "k", "ctrl+p":
			if c.themeSel > 0 {
				c.themeSel--
			}
			c.previewTheme(themeOrder[c.themeSel])
		case "down", "j", "ctrl+n":
			if c.themeSel < len(themeOrder)-1 {
				c.themeSel++
			}
			c.previewTheme(themeOrder[c.themeSel])
		case "t", "tab":
			c.themeSel = (c.themeSel + 1) % len(themeOrder)
			c.previewTheme(themeOrder[c.themeSel])
		}
		if len(msg.Runes) == 1 {
			if n := int(msg.Runes[0] - '1'); n >= 0 && n < len(themeOrder) {
				c.themeSel = n
				c.previewTheme(themeOrder[c.themeSel])
			}
		}
		c.layout()
		c.refresh()
		return nil
	}

	// help
	step := max(1, c.vp.Height-8)
	switch msg.String() {
	case "esc", "q", "?", "ctrl+g", "ctrl+c":
		c.restoreAndClose()
	case "up", "k", "ctrl+p":
		if c.helpTop > 0 {
			c.helpTop--
		}
	case "down", "j", "ctrl+n":
		c.helpTop++
	case "pgup":
		c.helpTop -= step
	case "pgdown":
		c.helpTop += step
	case "home", "g":
		c.helpTop = 0
	case "end", "G":
		c.helpTop = 1 << 20
	}
	if c.helpTop < 0 {
		c.helpTop = 0
	}
	c.layout()
	c.refresh()
	return nil
}

// helpCard renders the scrollable help panel.
func (c *chat) helpCard() string {
	mainW := c.mainWidth()
	cardW := min(max(40, mainW-6), 96)
	innerW := max(28, cardW-6)
	cardH := min(max(10, c.vp.Height-2), 24)
	innerH := max(6, cardH-4)

	lines := c.helpLines(innerW)
	scrolled := len(lines) > innerH
	if scrolled {
		innerH = max(3, innerH-1)
	}
	maxTop := max(0, len(lines)-innerH)
	if c.helpTop > maxTop {
		c.helpTop = maxTop
	}
	if c.helpTop < 0 {
		c.helpTop = 0
	}
	end := min(len(lines), c.helpTop+innerH)
	visible := append([]string{}, lines[c.helpTop:end]...)
	visible = fitLines(visible, innerH)
	for i := range visible {
		visible[i] = clipANSI(visible[i], innerW)
	}
	body := strings.Join(visible, "\n")
	if scrolled {
		body += "\n" + styleFaint.Render(fmt.Sprintf("%d–%d of %d · j/k scroll", c.helpTop+1, end, len(lines)))
	}
	return styleModal.Width(innerW + 4).Render(body)
}

// helpLines builds the full help document; the card windows it.
func (c *chat) helpLines(width int) []string {
	var out []string
	out = append(out,
		styleAccent.Bold(true).Render("◈  WOMBAT")+"  "+styleFaint.Render("command center"),
		styleDim.Render("keys, commands, and the parts of the interface you can drive."),
		"",
		sectionLabel("keys"),
	)
	keys := [][2]string{
		{"enter", "send message"},
		{"alt+enter", "new line"},
		{"esc", "stop a run / close a panel"},
		{"/", "command palette"},
		{"@", "file picker"},
		{"ctrl+v", "attach an image"},
		{"ctrl+b", "inspector panel"},
		{"ctrl+o", "open the preview"},
		{"ctrl+g", "this help"},
		{"ctrl+r", "add a reference"},
		{"ctrl+y", "copy the last reply"},
		{"ctrl+t", "cycle theme"},
		{"ctrl+p/n", "prompt history"},
		{"alt+1..5", "chat · sessions · models · usage · doctor"},
		{"pgup/pgdn", "scroll transcript"},
		{"ctrl+up/down", "scroll one line"},
	}
	if width < 56 {
		for _, k := range keys {
			out = append(out, " "+styleKey.Render(" "+k[0]+" ")+" "+styleDim.Render(k[1]))
		}
	} else {
		colW := max(18, width/2)
		for i := 0; i < len(keys); i += 2 {
			row := styleKey.Render(" "+keys[i][0]+" ") + " " + styleDim.Render(keys[i][1])
			if i+1 < len(keys) {
				second := styleKey.Render(" "+keys[i+1][0]+" ") + " " + styleDim.Render(keys[i+1][1])
				row = padRight(row, colW) + second
			}
			out = append(out, " "+row)
		}
	}
	out = append(out, "", sectionLabel("prompt syntax"))
	out = append(out,
		" "+styleAccent.Render("/")+"  command palette, options appear inline",
		" "+styleAccent.Render("@")+"  attach a file; tab completes the path",
		" "+styleAccent.Render("/reference")+"  keep paths and links in the inspector",
		" "+styleAccent.Render("!")+"  shell commands stay in the agent loop",
		"",
		sectionLabel("commands"),
	)
	order := []string{"Session", "Models", "Context", "Tools", "Files", "Appearance", "Safety", "Help", "System"}
	groups := map[string][]commands.Command{}
	for _, cmd := range commands.List {
		g := cmd.Group
		if g == "" {
			g = "System"
		}
		groups[g] = append(groups[g], cmd)
	}
	for _, g := range order {
		list := groups[g]
		if len(list) == 0 {
			continue
		}
		out = append(out, styleSection.Render(g))
		for _, cmd := range list {
			usage := pad(cmd.Usage(), 26)
			row := " " + styleAccent.Render(usage) + styleDim.Render(cmd.Summary)
			out = append(out, row)
			if len(cmd.Options) > 0 {
				var opts []string
				for _, o := range cmd.Options {
					opts = append(opts, o.Value)
				}
				out = append(out, "   "+styleFaint.Render("↳ "+strings.Join(opts, " · ")))
			}
		}
		out = append(out, "")
	}
	out = append(out,
		"",
		sectionLabel("terminal"),
		" "+styleDim.Render("Wombat uses your terminal's font and background."),
		" "+styleDim.Render("For the full glyph set, use a Nerd Font such as JetBrains Mono,"),
		" "+styleDim.Render("Berkeley Mono, or MesloLGS NF in your terminal settings."),
		"",
		styleFaint.Render("esc closes this panel · the full command list is always one / away"),
	)
	return out
}

// themeCard renders the theme picker as an adaptive grid so every theme is
// visible even on short or narrow terminals.
func (c *chat) themeCard() string {
	mainW := c.mainWidth()
	cardW := min(max(44, mainW-6), 72)
	innerW := max(32, cardW-6)
	themes := themeEntries()

	// Pick the widest column count that still leaves room for all rows.
	cols := 1
	for cand := 4; cand >= 1; cand-- {
		colW := (innerW - (cand-1)*2) / cand
		if colW < 12 {
			continue
		}
		rows := (len(themes) + cand - 1) / cand
		if rows+8 <= c.vp.Height || cand == 1 {
			cols = cand
			break
		}
	}
	colW := max(12, (innerW-(cols-1)*2)/cols)
	rows := (len(themes) + cols - 1) / cols

	lines := []string{styleAccent.Bold(true).Render("◈  THEMES") + "  " + styleFaint.Render("ctrl+t cycles")}
	if rows+10 <= c.vp.Height {
		lines = append(lines, styleDim.Render("select with ↑↓, apply with enter, cancel with esc"))
	}
	lines = append(lines, "")
	if rows+8 > c.vp.Height {
		// Too small for a grid: wrap the names so every theme is still listed.
		var parts []string
		for i, t := range themes {
			marker := "  "
			if i == c.themeSel {
				marker = "▸ "
			}
			parts = append(parts, marker+t.label)
		}
		for _, ln := range wrap(strings.Join(parts, "   "), max(10, innerW-2)) {
			lines = append(lines, " "+ln)
		}
	} else {
		for r := 0; r < rows; r++ {
			var cells []string
			for col := 0; col < cols; col++ {
				i := r*cols + col
				if i >= len(themes) {
					break
				}
				cells = append(cells, padRight(themeCell(i, c.themeSel, colW), colW))
			}
			lines = append(lines, " "+strings.Join(cells, "  "))
		}
	}
	if rows+11 <= c.vp.Height {
		sel := themes[c.themeSel]
		lines = append(lines, "", " "+styleAccent.Render(sel.label)+"  "+styleDim.Render(clip(sel.blurb, innerW-4)))
	}
	lines = append(lines, "", " "+styleFaint.Render("1-9 jump · enter apply · esc restores the previous theme"))
	for i := range lines {
		lines[i] = clipANSI(lines[i], innerW)
	}
	return styleModal.Width(innerW + 4).Render(strings.Join(lines, "\n"))
}

// themeCell is one cell in the theme grid.
func themeCell(i, sel, width int) string {
	t := themeEntries()[i]
	nameW := min(14, max(6, width-1))
	name := padRight(t.label, nameW)
	row := "  " + styleDim.Render(name)
	if width >= nameW+8 {
		row += " " + swatches(t)
	}
	if i == sel {
		row = " > " + name
		if width >= nameW+8 {
			row += " " + swatches(t)
		}
		return styleSelected.Render(pad(clip(row, width), max(8, width)))
	}
	return clipANSI(row, width)
}

// modalView places the active overlay in the transcript region.
func (c *chat) modalView() string {
	var card string
	switch c.modal {
	case modalHelp:
		card = c.helpCard()
	case modalTheme:
		card = c.themeCard()
	case modalReference:
		card = c.referenceCard()
	case modalSources:
		card = c.sourcesCard()
	case modalPreviews:
		card = c.previewsCard()
	case modalCopy:
		card = c.copyCard()
	default:
		return ""
	}
	w := c.mainWidth()
	h := c.vp.Height
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, card)
}

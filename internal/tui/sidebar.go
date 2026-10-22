package tui

import (
	"fmt"
	"strings"
)

// previewRailWidth is the total width of the left preview rail.
func (c *chat) previewRailWidth() int { return 24 }

// previewRailVisible keeps previews on the left once there is enough room for
// a left rail, the transcript, and the inspector.
func (c *chat) previewRailVisible() bool {
	return len(c.previews) > 0 && c.w >= 100 && c.vp.Height >= 8
}

// availableWidth is the terminal width minus the left rail.
func (c *chat) availableWidth() int {
	w := c.w
	if c.previewRailVisible() {
		w -= c.previewRailWidth() + 1
	}
	return w
}

// panelWidth is the total width of the inspector, including its border.
func (c *chat) panelWidth() int {
	avail := c.availableWidth()
	pw := avail / 4
	if pw < 32 {
		pw = 32
	}
	if pw > 44 {
		pw = 44
	}
	if avail-pw-1 < 44 {
		pw = avail - 45
	}
	if pw < 24 {
		pw = 24
	}
	return pw
}

// panelVisible reports whether the inspector has room to render.
func (c *chat) panelVisible() bool {
	if !c.panel || c.w < 96 || c.vp.Height < 8 {
		return false
	}
	// On narrow wide screens the preview rail is the priority; the right
	// inspector returns once there is room for both.
	if c.previewRailVisible() && c.w < 120 {
		return false
	}
	return true
}

// mainWidth is the width available to the transcript, excluding the inspector.
func (c *chat) mainWidth() int {
	w := c.w
	if c.previewRailVisible() {
		w -= c.previewRailWidth() + 1
	}
	if c.panelVisible() {
		w -= c.panelWidth() + 1
	}
	return w
}

// contentWidth is the transcript width, excluding the inspector and the
// one-column scrollbar. Overlays use the full main pane.
func (c *chat) contentWidth() int {
	if c.modal != modalNone {
		return max(20, c.mainWidth())
	}
	return max(20, c.mainWidth()-1)
}

// bodyView renders the transcript with a slim scrollbar on its right edge.
func (c *chat) bodyView() string {
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

// togglePanel flips the inspector and persists the choice.
func (c *chat) togglePanel() {
	c.panel = !c.panel
	if c.cfg != nil {
		c.cfg.Panel = c.panel
		_ = c.cfg.Save()
	}
	c.layout()
	c.refresh()
	if c.panelVisible() {
		c.note("inspector on — ctrl+b to hide", false)
	} else if c.panel {
		c.note("inspector needs a wider terminal (96 columns)", true)
	} else {
		c.note("inspector off", false)
	}
}

// previewRailView is the left preview list. The right inspector keeps
// references, sources, context, and workspace facts.
func (c *chat) previewRailView() string {
	w := c.previewRailWidth()
	inner := max(12, w-4)
	h := c.vp.Height
	lines := []string{stylePanelTitle.Render("PREVIEWS"), ""}
	for i, p := range c.previews {
		if i >= 8 {
			lines = append(lines, styleFaint.Render(fmt.Sprintf("+%d more", len(c.previews)-i)))
			break
		}
		glyph, gs := previewGlyph(p.Kind)
		lines = append(lines, styleFaint.Render(fmt.Sprintf("%d ", i+1))+
			gs.Render(glyph)+" "+styleDim.Render(clip(previewLabel(p), inner-4)))
	}
	lines = append(lines, "", styleFaint.Render("ctrl+o open"), styleFaint.Render("/previews list"))
	lines = fitLines(lines, max(1, h-2))
	return stylePanel.
		Width(inner + 2).
		Height(max(1, h-2)).
		Render(strings.Join(lines, "\n"))
}

// panelView renders the right-hand inspector: session facts, context usage,
// live preview, pending files, recent tool calls, and the key map.
func (c *chat) panelView() string {
	pw := c.panelWidth()
	contentW := max(12, pw-4)
	h := c.vp.Height
	var lines []string

	add := func(s string) { lines = append(lines, clipANSI(s, contentW)) }
	blank := func() {
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
	}

	add(stylePanelTitle.Render("INSPECTOR"))
	blank()

	add(sectionLabel("session"))
	title := "fresh session"
	if c.sess != nil {
		if c.sess.Meta.Title != "" {
			title = c.sess.Meta.Title
		} else {
			title = c.sess.ID()
		}
	}
	for _, ln := range wrap(title, contentW) {
		add(styleDim.Render(ln))
	}
	add(styleFaint.Render("id ") + styleDim.Render(clip(c.sessionID(), contentW-4)))
	if c.sess != nil && c.sess.Meta.Turns > 0 {
		add(styleFaint.Render("turns ") + styleDim.Render(itoa(c.sess.Meta.Turns)))
	}
	model := c.provName + "/" + c.modelName
	if model == "/" {
		model = "not configured"
	}
	add(styleFaint.Render("model ") + styleDim.Render(clip(model, contentW-6)))
	if c.branch != "" {
		add(styleFaint.Render("branch ") + styleAccent2.Render(clip(c.branch, contentW-7)))
	}
	blank()

	for _, line := range c.referencesSection(contentW) {
		add(line)
	}
	blank()

	add(sectionLabel("context"))
	if c.window > 0 {
		pct := c.history * 100 / c.window
		if pct > 100 {
			pct = 100
		}
		add(styleDim.Render(fmt.Sprintf("%s / %s", humanN(c.history), humanN(c.window))))
		add(miniBar(c.history, c.window, max(6, contentW-8)) + " " + styleFaint.Render(fmt.Sprintf("%d%%", pct)))
	} else {
		add(styleFaint.Render("window unknown"))
	}
	if c.cumIn+c.cumOut > 0 {
		add(styleFaint.Render("tokens ") + styleDim.Render(
			fmt.Sprintf("in %s · out %s", humanN(c.cumIn), humanN(c.cumOut))))
	}
	blank()

	if !c.previewRailVisible() {
		for _, line := range c.previewsSection(contentW) {
			add(line)
		}
		blank()
	}

	add(sectionLabel("sources"))
	if c.sourceSites+c.sourceFiles == 0 {
		add(styleFaint.Render("none · /sources to browse"))
	} else {
		add(styleLink.Render(itoa(c.sourceSites)+" sites") + " · " +
			styleAccent2.Render(itoa(c.sourceFiles)+" files"))
		add(styleFaint.Render("/sources to browse"))
	}
	blank()

	if len(c.pending) > 0 {
		add(sectionLabel("attached"))
		for i, a := range c.pending {
			if i >= 4 {
				add(styleFaint.Render(fmt.Sprintf("+%d more", len(c.pending)-i)))
				break
			}
			add(styleAccent2.Render("↳ ") + styleDim.Render(clip(a.Kind+" "+a.Name, contentW-3)))
		}
		blank()
	}

	recent := c.recentTools(4)
	if len(recent) > 0 && len(lines)+len(recent)+2 <= max(1, h-2) {
		add(sectionLabel("recent tools"))
		for _, e := range recent {
			glyph, st := toolGlyph(e.name)
			mark := styleGood.Render("✓")
			if e.running {
				mark = c.sp.View()
			} else if !e.ok {
				mark = styleBad.Render("✗")
			}
			add(mark + " " + st.Render(glyph) + " " + styleDim.Render(clip(e.name, contentW-6)))
		}
		blank()
	}

	if c.streaming && len(lines)+4 <= max(1, h-2) {
		add(sectionLabel("run"))
		add(c.sp.View() + " " + styleWarn.Render("working") + " " +
			styleFaint.Render(elapsedLabel(c.runDuration())))
		if c.runTools > 0 {
			add(styleFaint.Render(fmt.Sprintf("%d tool calls this turn", c.runTools)))
		}
		blank()
	}

	if len(lines)+7 <= max(1, h-2) {
		add(sectionLabel("keys"))
		for _, pair := range [][2]string{
			{"ctrl+b", "hide"},
			{"ctrl+o", "preview"},
			{"ctrl+g", "help"},
			{"ctrl+t", "theme"},
			{"ctrl+r", "reference"},
		} {
			add(styleFaint.Render(pad(pair[0], 8)) + styleDim.Render(pair[1]))
		}
	}

	lines = fitLines(lines, max(1, h-2))
	body := strings.Join(lines, "\n")
	return stylePanel.
		Width(max(1, contentW+2)).
		Height(max(1, h-2)).
		Render(body)
}

// recentTools returns the newest tool entries, in display order.
func (c *chat) recentTools(limit int) []*chatEntry {
	var out []*chatEntry
	for i := len(c.entries) - 1; i >= 0 && len(out) < limit; i-- {
		if c.entries[i].kind == entryTool {
			out = append(out, c.entries[i])
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// inspectorGutter is the one-column space between transcript and inspector.
func inspectorGutter() string { return " " }

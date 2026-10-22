package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ramsterr/Wombat/internal/trace"
)

type sourceItem struct {
	kind  string // site or file
	value string
	calls int
}

func (c *chat) tracePath() string {
	if c.sess == nil {
		return ""
	}
	return strings.TrimSuffix(c.sess.Path(), ".jsonl") + ".events.jsonl"
}

// loadSources reads the trace log once and returns sites first, then files.
func (c *chat) loadSources() []sourceItem {
	c.sourceSites, c.sourceFiles = 0, 0
	path := c.tracePath()
	if path == "" {
		return nil
	}
	urls, files, err := trace.Sources(path)
	if err != nil {
		return nil
	}
	var out []sourceItem
	for _, u := range urls {
		out = append(out, sourceItem{kind: "site", value: u.Value, calls: u.Calls})
	}
	for _, f := range files {
		out = append(out, sourceItem{kind: "file", value: f.Value, calls: f.Calls})
	}
	c.sourceSites, c.sourceFiles = len(urls), len(files)
	return out
}

// refreshSourceCounts updates the inspector without opening the browser.
func (c *chat) refreshSourceCounts() {
	_ = c.loadSources()
}

// openSources shows the interactive sites and files browser.
func (c *chat) openSources() tea.Cmd {
	if c.sess == nil {
		c.note("no sources recorded yet — run a tool that reads or fetches something", false)
		return nil
	}
	items := c.loadSources()
	if len(items) == 0 {
		c.note("no sources recorded yet — run a tool that reads or fetches something", false)
		return nil
	}
	c.sourceItems = items
	c.sourceSel = 0
	c.sourceTab = 0
	c.modal = modalSources
	c.clearPalette()
	c.closeFilePalette()
	c.layout()
	c.refresh()
	return nil
}

func (c *chat) visibleSources() []sourceItem {
	if c.sourceTab == 0 {
		return c.sourceItems
	}
	kind := "site"
	if c.sourceTab == 2 {
		kind = "file"
	}
	var out []sourceItem
	for _, it := range c.sourceItems {
		if it.kind == kind {
			out = append(out, it)
		}
	}
	return out
}

func (c *chat) syncSourceSelection() {
	n := len(c.visibleSources())
	if n == 0 {
		c.sourceSel = 0
		return
	}
	if c.sourceSel >= n {
		c.sourceSel = n - 1
	}
	if c.sourceSel < 0 {
		c.sourceSel = 0
	}
}

// sourceKey handles the sites and files browser.
func (c *chat) sourceKey(msg tea.KeyMsg) tea.Cmd {
	items := c.visibleSources()
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		c.modal = modalNone
		c.layout()
		c.refresh()
		return nil
	case "tab", "right", "l":
		c.sourceTab = (c.sourceTab + 1) % 3
		c.sourceSel = 0
	case "shift+tab", "left", "h":
		c.sourceTab = (c.sourceTab + 2) % 3
		c.sourceSel = 0
	case "up", "k", "ctrl+p":
		if c.sourceSel > 0 {
			c.sourceSel--
		}
	case "down", "j", "ctrl+n":
		if c.sourceSel < len(items)-1 {
			c.sourceSel++
		}
	case "enter":
		if len(items) == 0 {
			return nil
		}
		it := items[c.sourceSel]
		if it.kind == "site" {
			if err := openLocalURL(it.value); err != nil {
				c.note("could not open "+it.value+": "+err.Error(), true)
			} else {
				c.note("opened "+it.value, false)
			}
			return nil
		}
		if err := c.addAttachment(it.value); err != nil {
			c.note("error: "+err.Error(), true)
		}
		return nil
	}
	c.syncSourceSelection()
	c.layout()
	c.refresh()
	return nil
}

// sourcesCard renders the browser overlay.
func (c *chat) sourcesCard() string {
	mainW := c.mainWidth()
	cardW := min(max(44, mainW-8), 90)
	innerW := max(30, cardW-6)
	cardH := min(max(12, c.vp.Height-2), 24)
	innerH := max(6, cardH-5)

	tabs := []string{"all", "sites", "files"}
	var tabLine []string
	for i, t := range tabs {
		if i == c.sourceTab {
			tabLine = append(tabLine, styleButton.Render(" "+t+" "))
		} else {
			tabLine = append(tabLine, styleFaint.Render(" "+t+" "))
		}
	}
	lines := []string{
		styleAccent2.Bold(true).Render("◈  SOURCES") + "  " + styleFaint.Render("sites & files used in this session"),
		strings.Join(tabLine, " "),
		"",
	}
	items := c.visibleSources()
	c.syncSourceSelection()
	start := 0
	if c.sourceSel >= innerH-2 {
		start = c.sourceSel - (innerH - 3)
	}
	end := min(len(items), start+innerH-2)
	for i := start; i < end; i++ {
		it := items[i]
		glyph, gs := "⌁", styleLink
		if it.kind == "file" {
			glyph, gs = "◫", styleAccent2
		}
		text := glyph + " " + clip(sanitizeTerminalText(it.value), innerW-14) + "  " + fmt.Sprintf("%d×", it.calls)
		if i == c.sourceSel {
			lines = append(lines, styleSelected.Render(" > "+pad(text, innerW-3)))
		} else {
			lines = append(lines, "   "+gs.Render(glyph)+" "+styleDim.Render(clip(sanitizeTerminalText(it.value), innerW-14))+"  "+styleFaint.Render(fmt.Sprintf("%d×", it.calls)))
		}
	}
	if len(items) == 0 {
		lines = append(lines, styleFaint.Render("nothing recorded in this tab"))
	}
	if end < len(items) {
		lines = append(lines, styleFaint.Render(fmt.Sprintf("  … %d more below", len(items)-end)))
	}
	lines = append(lines, "",
		" "+styleFaint.Render("tab switch · ↑↓ move · enter open/attach · esc close"))
	for i := range lines {
		lines[i] = clipANSI(lines[i], innerW)
	}
	return styleModal.Width(innerW + 4).Render(strings.Join(lines, "\n"))
}

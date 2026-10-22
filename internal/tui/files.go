package tui

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// updateFilePalette looks for an unfinished @mention at the end of the input
// and refreshes the matching project files. It is intentionally conservative:
// only a token that starts with @ after whitespace opens the picker.
func (c *chat) updateFilePalette() {
	if c.paletteOpen() || c.modal != modalNone {
		c.closeFilePalette()
		return
	}
	text := c.ta.Value()
	start := -1
	for i := len(text) - 1; i >= 0; i-- {
		r := text[i]
		if r == '\n' || r == '\t' || r == ' ' {
			break
		}
		if r == '@' {
			start = i
			break
		}
	}
	if start < 0 || (start > 0 && !isSpaceByte(text[start-1])) {
		c.closeFilePalette()
		return
	}
	query := text[start+1:]
	if strings.ContainsAny(query, "\n\t\"'") {
		c.closeFilePalette()
		return
	}
	c.fileActive = true
	c.fileQuery = query
	c.fileStart = start
	c.fileEnd = len(text)

	all := c.projectFiles()
	lq := strings.ToLower(query)
	var out []fileEntry
	for _, e := range all {
		p := strings.TrimSuffix(e.path, "/")
		lp := strings.ToLower(p)
		base := strings.ToLower(filepath.Base(p))
		if lq == "" || strings.HasPrefix(lp, lq) || strings.HasPrefix(base, filepath.Base(lq)) {
			out = append(out, e)
			if len(out) >= 60 {
				break
			}
		}
	}
	c.fileList = out
	c.fileSel = 0
	c.fileTop = 0
}

func isSpaceByte(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

func (c *chat) closeFilePalette() {
	c.fileActive = false
	c.fileList = nil
	c.fileSel = 0
	c.fileTop = 0
	c.fileQuery = ""
}

// projectFiles walks the working directory once and caches the result for a
// short time. It skips the directories that make completion noisy and slow.
func (c *chat) projectFiles() []fileEntry {
	if len(c.fileCache) > 0 && time.Since(c.fileCacheT) < 10*time.Second {
		return c.fileCache
	}
	root := c.opt.Cwd
	var out []fileEntry
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if skipFileDir(d.Name()) {
				return fs.SkipDir
			}
			out = append(out, fileEntry{path: rel + "/", isDir: true})
		} else {
			if strings.HasPrefix(d.Name(), ".DS_Store") {
				return nil
			}
			out = append(out, fileEntry{path: rel, isDir: false})
		}
		if len(out) >= 4000 {
			return fs.SkipAll
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	c.fileCache = out
	c.fileCacheT = time.Now()
	return out
}

func skipFileDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "vendor", "dist", "build",
		"target", ".next", ".nuxt", ".venv", "venv", "__pycache__", ".cache",
		".wombat", ".idea", ".vscode", "coverage", ".turbo":
		return true
	}
	return false
}

// filePaletteKey handles navigation in the @-file picker. The bool reports
// whether the key was consumed.
func (c *chat) filePaletteKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if !c.fileActive {
		return nil, false
	}
	if msg.String() == "esc" {
		c.closeFilePalette()
		c.layout()
		c.refresh()
		return nil, true
	}
	if len(c.fileList) == 0 {
		return nil, false
	}
	switch msg.String() {
	case "up", "ctrl+p":
		if c.fileSel > 0 {
			c.fileSel--
		}
		c.syncFileWindow()
		c.layout()
		c.refresh()
		return nil, true
	case "down", "ctrl+n":
		if c.fileSel < len(c.fileList)-1 {
			c.fileSel++
		}
		c.syncFileWindow()
		c.layout()
		c.refresh()
		return nil, true
	case "tab":
		c.chooseFile(c.fileList[c.fileSel])
		return nil, true
	case "enter":
		c.chooseFile(c.fileList[c.fileSel])
		return nil, true
	}
	return nil, false
}

func (c *chat) chooseFile(e fileEntry) {
	text := c.ta.Value()
	if c.fileStart < 0 || c.fileEnd > len(text) {
		c.closeFilePalette()
		return
	}
	mention := "@" + e.path
	if strings.ContainsAny(e.path, " \t") {
		mention = "@\"" + e.path + "\""
	}
	value := text[:c.fileStart] + mention + text[c.fileEnd:]
	c.ta.SetValue(value)
	c.ta.CursorEnd()
	c.closeFilePalette()
	if e.isDir {
		c.updateFilePalette()
	}
	c.layout()
	c.refresh()
}

func (c *chat) fileLimit() int {
	limit := 7
	if c.h > 0 {
		if room := (c.h - 14); room < limit {
			limit = room
		}
	}
	if limit < 3 {
		limit = 3
	}
	return limit
}

func (c *chat) syncFileWindow() {
	limit := c.fileLimit()
	if len(c.fileList) == 0 {
		c.fileTop = 0
		return
	}
	if limit > len(c.fileList) {
		limit = len(c.fileList)
	}
	if c.fileSel < c.fileTop {
		c.fileTop = c.fileSel
	}
	if c.fileSel >= c.fileTop+limit {
		c.fileTop = c.fileSel - limit + 1
	}
	if c.fileTop < 0 {
		c.fileTop = 0
	}
	if c.fileTop+limit > len(c.fileList) {
		c.fileTop = len(c.fileList) - limit
	}
}

// filePaletteView renders the @-file picker above the input.
func (c *chat) filePaletteView() string {
	if !c.fileActive {
		return ""
	}
	var b strings.Builder
	label := "@"
	if c.fileQuery != "" {
		label += c.fileQuery
	}
	b.WriteString(" " + styleAccent2.Render("files") + " " + styleDim.Render(label))
	if len(c.fileList) == 0 {
		b.WriteString("  " + styleFaint.Render("no matches · esc to close"))
		return b.String()
	}
	b.WriteString("  " + styleFaint.Render(itoa(len(c.fileList))+" matches") + "\n")
	limit := min(c.fileLimit(), len(c.fileList))
	c.syncFileWindow()
	end := min(len(c.fileList), c.fileTop+limit)
	for i := c.fileTop; i < end; i++ {
		e := c.fileList[i]
		glyph := "·"
		if e.isDir {
			glyph = "▸"
		}
		row := " " + glyph + " " + e.path
		if i == c.fileSel {
			b.WriteString(styleSelected.Render(pad(row, max(1, c.mainWidth()-2))) + "\n")
		} else {
			b.WriteString(styleDim.Render(row) + "\n")
		}
	}
	b.WriteString(styleFaint.Render("  ↑↓ choose · tab/enter insert · esc close"))
	return b.String()
}

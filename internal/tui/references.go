package tui

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ramsterr/Wombat/internal/config"
)

// reference is one project note the user keeps beside the chat: a file, a
// directory, or a URL. References are project-scoped and survive sessions.
type reference struct {
	Value string    `json:"value"`
	Kind  string    `json:"kind"`
	Added time.Time `json:"added"`
}

// referencesPath stores references outside the project so the repository is
// never modified just by keeping a reading list.
func referencesPath(cwd string) string {
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	sum := sha1.Sum([]byte(cwd))
	name := hex.EncodeToString(sum[:8]) + ".json"
	return filepath.Join(config.Home(), "references", name)
}

func loadReferences(cwd string) []reference {
	raw, err := os.ReadFile(referencesPath(cwd))
	if err != nil {
		return nil
	}
	var refs []reference
	if err := json.Unmarshal(raw, &refs); err != nil {
		return nil
	}
	seen := map[string]bool{}
	out := refs[:0]
	for _, r := range refs {
		if r.Value == "" || seen[r.Value] {
			continue
		}
		seen[r.Value] = true
		out = append(out, r)
	}
	return out
}

func saveReferences(cwd string, refs []reference) error {
	path := referencesPath(cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(refs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func classifyReference(cwd, value string) string {
	if u, err := url.Parse(value); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return "site"
	}
	p := value
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	if st, err := os.Stat(p); err == nil {
		if st.IsDir() {
			return "dir"
		}
		return "file"
	}
	return "ref"
}

func refGlyph(kind string) (string, lipgloss.Style) {
	switch kind {
	case "site":
		return "⌁", styleLink
	case "file":
		return "◫", styleAccent2
	case "dir":
		return "▸", styleAccent
	default:
		return "•", styleDim
	}
}

// initReferenceInput prepares the add-reference text field.
func (c *chat) initReferenceInput() {
	if c.refInput.Placeholder != "" {
		return
	}
	in := textinput.New()
	in.Placeholder = "path or URL"
	in.Prompt = "› "
	in.CharLimit = 500
	c.refInput = in
	c.applyThemeStyles()
}

// openReferencePrompt shows the reference list and a one-line add field.
func (c *chat) openReferencePrompt() tea.Cmd {
	c.initReferenceInput()
	c.refInput.SetValue("")
	focus := c.refInput.Focus()
	c.modal = modalReference
	c.clearPalette()
	c.closeFilePalette()
	c.layout()
	c.refresh()
	return focus
}

// addReference stores a new reference for this project.
func (c *chat) addReference(value string) error {
	value = strings.TrimSpace(strings.Trim(sanitizeTerminalText(value), `"'`))
	if value == "" {
		return fmt.Errorf("a path or URL is required")
	}
	for _, r := range c.refs {
		if r.Value == value {
			return fmt.Errorf("already referenced")
		}
	}
	c.refs = append(c.refs, reference{
		Value: value,
		Kind:  classifyReference(c.opt.Cwd, value),
		Added: time.Now(),
	})
	if err := saveReferences(c.opt.Cwd, c.refs); err != nil {
		return err
	}
	c.layout()
	c.refresh()
	return nil
}

// removeReference deletes by 1-based index or by value.
func (c *chat) removeReference(arg string) error {
	if len(c.refs) == 0 {
		return fmt.Errorf("the reference list is empty")
	}
	idx := -1
	if n, err := strconv.Atoi(arg); err == nil {
		if n >= 1 && n <= len(c.refs) {
			idx = n - 1
		}
	} else {
		for i, r := range c.refs {
			if r.Value == arg || filepath.Base(r.Value) == filepath.Base(arg) {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		return fmt.Errorf("no reference matches %s", arg)
	}
	removed := c.refs[idx].Value
	c.refs = append(c.refs[:idx], c.refs[idx+1:]...)
	if err := saveReferences(c.opt.Cwd, c.refs); err != nil {
		return err
	}
	c.note("removed reference "+removed, false)
	c.layout()
	c.refresh()
	return nil
}

// clearReferences empties the list for this project.
func (c *chat) clearReferences() error {
	c.refs = nil
	if err := saveReferences(c.opt.Cwd, c.refs); err != nil {
		return err
	}
	c.note("references cleared", false)
	c.layout()
	c.refresh()
	return nil
}

// referenceCommand handles /reference and /ref.
func (c *chat) referenceCommand(arg string) tea.Cmd {
	arg = strings.TrimSpace(arg)
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		return c.openReferencePrompt()
	}
	switch fields[0] {
	case "list":
		return c.openReferencePrompt()
	case "add":
		value := strings.TrimSpace(strings.TrimPrefix(arg, "add"))
		if value == "" {
			return c.openReferencePrompt()
		}
		if err := c.addReference(value); err != nil {
			c.note("error: "+err.Error(), true)
		} else {
			c.note("reference added: "+value, false)
		}
	case "rm", "remove":
		value := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(arg, "remove"), "rm"))
		if value == "" {
			c.note("usage: /reference rm <number|value>", true)
			break
		}
		if err := c.removeReference(value); err != nil {
			c.note("error: "+err.Error(), true)
		}
	case "clear":
		if err := c.clearReferences(); err != nil {
			c.note("error: "+err.Error(), true)
		}
	default:
		if err := c.addReference(arg); err != nil {
			c.note("error: "+err.Error(), true)
		} else {
			c.note("reference added: "+arg, false)
		}
	}
	return nil
}

// referenceCard is the add/list overlay.
func (c *chat) referenceCard() string {
	mainW := c.mainWidth()
	cardW := min(max(40, mainW-10), 72)
	innerW := max(28, cardW-6)
	left, right := themeGradient()
	lines := []string{
		gradientText("◈  REFERENCES", left, right) + "  " + styleFaint.Render("this project"),
		styleDim.Render("keep paths and links beside the chat; enter adds the value below"),
		"",
		c.refInput.View(),
		"",
	}
	if len(c.refs) == 0 {
		lines = append(lines, styleFaint.Render("no references yet — try a file, a folder, or a URL"))
	} else {
		for i, r := range c.refs {
			if i >= 8 {
				lines = append(lines, styleFaint.Render(fmt.Sprintf("+%d more", len(c.refs)-i)))
				break
			}
			glyph, gs := refGlyph(r.Kind)
			lines = append(lines, " "+styleFaint.Render(fmt.Sprintf("%d", i+1))+" "+
				gs.Render(glyph)+" "+styleDim.Render(clip(r.Value, innerW-6)))
		}
	}
	lines = append(lines,
		"",
		" "+styleFaint.Render("enter add · /reference rm <n> remove · esc close"),
	)
	for i := range lines {
		lines[i] = clipANSI(lines[i], innerW)
	}
	return styleModal.Width(innerW + 4).Render(strings.Join(lines, "\n"))
}

// referencesSection renders the inspector block.
func (c *chat) referencesSection(contentW int) []string {
	out := []string{sectionLabel("references")}
	if len(c.refs) == 0 {
		out = append(out, styleFaint.Render("none · ctrl+r to add"))
		return out
	}
	for i, r := range c.refs {
		if i >= 6 {
			out = append(out, styleFaint.Render(fmt.Sprintf("+%d more · ctrl+r", len(c.refs)-i)))
			break
		}
		glyph, gs := refGlyph(r.Kind)
		out = append(out, styleFaint.Render(fmt.Sprintf("%d ", i+1))+
			gs.Render(glyph)+" "+styleDim.Render(clip(r.Value, contentW-5)))
	}
	return out
}

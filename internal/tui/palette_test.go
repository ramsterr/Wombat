package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func newPaletteChat() *chat {
	c := &chat{}
	c.ta = textarea.New()
	return c
}

func TestPaletteMatches(t *testing.T) {
	c := newPaletteChat()
	c.ta.SetValue("/")
	c.updatePalette()
	if len(c.cmdList) == 0 {
		t.Fatal("expected the palette to list the commands")
	}
	if c.cmdList[0].insert != "/mode" {
		t.Errorf("first command = %q", c.cmdList[0].insert)
	}
}

func TestPaletteFilters(t *testing.T) {
	c := newPaletteChat()
	c.ta.SetValue("/thi")
	c.updatePalette()
	if len(c.cmdList) != 1 || c.cmdList[0].insert != "/thinking" {
		t.Fatalf("filtered = %+v", c.cmdList)
	}
}

func TestPaletteCloses(t *testing.T) {
	c := newPaletteChat()
	c.ta.SetValue("hello")
	c.updatePalette()
	if len(c.cmdList) != 0 {
		t.Error("the palette should be closed for ordinary text")
	}
	c.ta.SetValue("/mode")
	c.updatePalette()
	if len(c.cmdList) == 0 {
		t.Error("the palette should be open")
	}
}

func TestCompleteCommand(t *testing.T) {
	c := newPaletteChat()
	c.ta.SetValue("/thi")
	c.updatePalette()
	c.cmdSel = 0
	c.completeCommand()
	if got := c.ta.Value(); got != "/thinking " {
		t.Errorf("value = %q", got)
	}
	if len(c.cmdList) != 0 {
		t.Error("the palette should close after completing")
	}
}

// TestPaletteAppearsWhileTyping drives the real key handler: the palette must
// appear as the user types, not only when updatePalette is called by hand.
func TestPaletteAppearsWhileTyping(t *testing.T) {
	c := newPaletteChat()
	c.ta.Focus()
	c.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if len(c.cmdList) == 0 {
		t.Fatal("the palette did not open when / was typed")
	}
	if c.ta.Value() != "/" {
		t.Errorf("input = %q", c.ta.Value())
	}
	if !c.paletteOpen() {
		t.Error("paletteOpen should be true")
	}
	// typing more filters the list
	c.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if len(c.cmdList) == 0 {
		t.Fatal("the palette closed while typing")
	}
	if c.cmdList[0].insert != "/sessions" && c.cmdList[0].insert != "/status" && c.cmdList[0].insert != "/setup" && c.cmdList[0].insert != "/sources" {
		t.Errorf("filtered list = %+v", c.cmdList)
	}
}

// TestPaletteClosesOnSubmit checks the palette does not linger after a
// message is sent.
func TestPaletteClosesOnSubmit(t *testing.T) {
	c := newPaletteChat()
	c.ta.Focus()
	c.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !c.paletteOpen() {
		t.Fatal("the palette did not open")
	}
	c.cmdList = nil
	c.updatePalette()
	if !c.paletteOpen() {
		t.Error("the palette should still reflect the input")
	}
	// submitting clears the input and the palette
	c.submit("/help")
	if c.paletteOpen() {
		t.Error("the palette should close after a message is sent")
	}
}

// TestPaletteArrowsMoveSelection drives the real key handler: the arrow keys
// must move the highlight while the palette is open.
func TestPaletteArrowsMoveSelection(t *testing.T) {
	c := newPaletteChat()
	c.ta.Focus()
	c.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !c.paletteOpen() {
		t.Fatal("the palette did not open")
	}
	first := c.cmdList[0].insert

	c.key(tea.KeyMsg{Type: tea.KeyDown})
	if c.cmdSel != 1 {
		t.Fatalf("cmdSel = %d, want 1", c.cmdSel)
	}
	if c.cmdList[c.cmdSel].insert == first {
		t.Error("the selection did not move")
	}

	c.key(tea.KeyMsg{Type: tea.KeyUp})
	if c.cmdSel != 0 {
		t.Fatalf("cmdSel = %d, want 0", c.cmdSel)
	}
	// it must not go past the ends
	c.key(tea.KeyMsg{Type: tea.KeyUp})
	if c.cmdSel != 0 {
		t.Fatalf("cmdSel = %d, want 0", c.cmdSel)
	}
	for i := 0; i < len(c.cmdList)+3; i++ {
		c.key(tea.KeyMsg{Type: tea.KeyDown})
	}
	if c.cmdSel != len(c.cmdList)-1 {
		t.Fatalf("cmdSel = %d, want %d", c.cmdSel, len(c.cmdList)-1)
	}
}

// TestPaletteTabCompletesAndEscCloses covers the rest of the palette keys.
func TestPaletteTabCompletesAndEscCloses(t *testing.T) {
	c := newPaletteChat()
	c.ta.Focus()
	c.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	c.key(tea.KeyMsg{Type: tea.KeyTab})
	if !strings.HasPrefix(c.ta.Value(), "/") {
		t.Errorf("input = %q", c.ta.Value())
	}
	c.key(tea.KeyMsg{Type: tea.KeyEsc})
	if c.paletteOpen() {
		t.Error("esc should close the palette")
	}
}

// TestPaletteEnterRunsCommand checks that Enter runs the highlighted command.
func TestPaletteEnterRunsCommand(t *testing.T) {
	c := newPaletteChat()
	c.ta.Focus()
	c.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/yolo")})
	if !c.paletteOpen() {
		t.Fatal("the palette did not open")
	}
	before := c.auto
	c.key(tea.KeyMsg{Type: tea.KeyEnter})
	if c.auto == before {
		t.Error("enter did not run the highlighted command")
	}
	if c.paletteOpen() {
		t.Error("the palette should close after running a command")
	}
}

// TestPaletteEnterOffersOptions checks that a command with options opens
// them instead of running straight away.
func TestPaletteEnterOffersOptions(t *testing.T) {
	c := newPaletteChat()
	c.ta.Focus()
	c.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/thinking")})
	if !c.paletteOpen() {
		t.Fatal("the palette did not open")
	}
	c.key(tea.KeyMsg{Type: tea.KeyEnter})
	if c.ta.Value() != "/thinking " {
		t.Errorf("input = %q, want %q", c.ta.Value(), "/thinking ")
	}
	if !c.paletteOpen() {
		t.Fatal("the options did not open")
	}
	labels := map[string]bool{}
	for _, e := range c.cmdList {
		labels[e.label] = true
	}
	if !labels["on"] || !labels["off"] {
		t.Errorf("options = %v", labels)
	}
	// the option list filters as the argument is typed
	c.ta.SetValue("/thinking o")
	c.updatePalette()
	if len(c.cmdList) != 2 {
		t.Errorf("filtered options = %+v", c.cmdList)
	}
}

// TestPaletteWindowFollowsSelection checks the list scrolls so the selected
// command is always visible, with counters for what is hidden.
func TestPaletteWindowFollowsSelection(t *testing.T) {
	c := newPaletteChat()
	c.h = 30
	c.ta.Focus()
	c.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	view := c.paletteView()
	if !strings.Contains(view, "more below") {
		t.Errorf("expected a counter below:\n%s", view)
	}
	if strings.Contains(view, "more above") {
		t.Errorf("nothing should be hidden above at the start:\n%s", view)
	}

	for i := 0; i < 10; i++ {
		c.key(tea.KeyMsg{Type: tea.KeyDown})
	}
	if c.cmdTop == 0 {
		t.Fatal("the window did not scroll")
	}
	view = c.paletteView()
	if !strings.Contains(view, "more above") {
		t.Errorf("expected a counter above:\n%s", view)
	}
	// the selected command must be in the visible window
	if !strings.Contains(view, c.cmdList[c.cmdSel].insert) {
		t.Errorf("the selection %q is not visible:\n%s", c.cmdList[c.cmdSel].insert, view)
	}
	// walking back up scrolls the window back
	for i := 0; i < 10; i++ {
		c.key(tea.KeyMsg{Type: tea.KeyUp})
	}
	if c.cmdTop != 0 {
		t.Errorf("the window did not scroll back: cmdTop = %d", c.cmdTop)
	}
}

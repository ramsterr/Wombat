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
	if c.cmdList[0].Name != "/mode" {
		t.Errorf("first command = %q", c.cmdList[0].Name)
	}
}

func TestPaletteFilters(t *testing.T) {
	c := newPaletteChat()
	c.ta.SetValue("/sand")
	c.updatePalette()
	if len(c.cmdList) != 1 || c.cmdList[0].Name != "/sandbox" {
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
	c.ta.SetValue("/sand")
	c.updatePalette()
	c.cmdSel = 0
	c.completeCommand()
	if got := c.ta.Value(); got != "/sandbox " {
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
	if c.cmdList[0].Name != "/sessions" && c.cmdList[0].Name != "/sandbox" && c.cmdList[0].Name != "/status" && c.cmdList[0].Name != "/setup" && c.cmdList[0].Name != "/sources" {
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
	first := c.cmdList[0].Name

	c.key(tea.KeyMsg{Type: tea.KeyDown})
	if c.cmdSel != 1 {
		t.Fatalf("cmdSel = %d, want 1", c.cmdSel)
	}
	if c.cmdList[c.cmdSel].Name == first {
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
	if c.paletteOpen() {
		t.Error("tab should complete and close the palette")
	}
	if !strings.HasPrefix(c.ta.Value(), "/") {
		t.Errorf("input = %q", c.ta.Value())
	}

	c.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !c.paletteOpen() {
		t.Fatal("the palette did not reopen")
	}
	c.key(tea.KeyMsg{Type: tea.KeyEsc})
	if c.paletteOpen() {
		t.Error("esc should close the palette")
	}
}

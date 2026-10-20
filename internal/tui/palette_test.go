package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
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

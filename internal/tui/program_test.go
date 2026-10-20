package tui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/ramsterr/Wombat/internal/config"
)

// TestPaletteInRealProgram runs the actual Bubble Tea program with a typed
// "/" and checks that the rendered output contains the command list. This is
// the test that catches the palette not being wired into the key handler.
func TestPaletteInRealProgram(t *testing.T) {
	ch := &chat{
		cfg:       &config.Config{Provider: "openai", Model: "gpt-5", Mode: "create"},
		provName:  "openai",
		modelName: "gpt-5",
		mode:      "create",
		ta:        textarea.New(),
		vp:        viewport.New(100, 30),
	}
	ch.ta.SetWidth(96)
	m := &rootModel{chat: ch, screen: scrChat}

	var out bytes.Buffer
	in := strings.NewReader("/")
	p := tea.NewProgram(m,
		tea.WithInput(in),
		tea.WithOutput(&out),
	)
	go func() {
		time.Sleep(150 * time.Millisecond)
		p.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
	}()

	done := make(chan struct{})
	go func() {
		p.Run()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		p.Quit()
		<-done
	}

	rendered := out.String()
	if !strings.Contains(rendered, "list the working modes") {
		t.Fatalf("the palette did not appear in the rendered output:\n%s", stripANSI(rendered))
	}
	if !strings.Contains(rendered, "switch the working mode") {
		t.Fatalf("the palette is missing entries:\n%s", stripANSI(rendered))
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc && (r == 'm' || r == 'H' || r == 'J' || r == 'K' || r == 'A' || r == 'B' || r == 'C' || r == 'D' || r == 'h' || r == 'l'):
			inEsc = false
		case inEsc:
			// skip
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

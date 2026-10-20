package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ramsterr/Wombat/internal/tools"
)

func newQuestionChat() (*chat, chan string) {
	c := &chat{}
	reply := make(chan string, 1)
	c.askReply = reply
	c.initQuestionInput()
	c.question = &tools.Question{
		Question: "Which direction?",
		Options: []tools.QuestionOption{
			{Label: "Editorial", Description: "bold"},
			{Label: "Minimal", Description: "quiet"},
		},
	}
	return c, reply
}

func TestQuestionPicksAnOption(t *testing.T) {
	c, reply := newQuestionChat()
	c.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	select {
	case answer := <-reply:
		if answer != "Minimal" {
			t.Errorf("answer = %q", answer)
		}
	default:
		t.Fatal("no answer was delivered")
	}
	if c.question != nil {
		t.Error("the question should be closed")
	}
}

func TestQuestionAcceptsTypedAnswer(t *testing.T) {
	c, reply := newQuestionChat()
	c.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if !c.questionTyping {
		t.Fatal("the free-text mode did not open")
	}
	c.questionText.SetValue("something else entirely")
	c.key(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case answer := <-reply:
		if answer != "something else entirely" {
			t.Errorf("answer = %q", answer)
		}
	default:
		t.Fatal("no answer was delivered")
	}
}

func TestQuestionEscapeSkips(t *testing.T) {
	c, reply := newQuestionChat()
	c.key(tea.KeyMsg{Type: tea.KeyEsc})
	select {
	case answer := <-reply:
		if answer != "" {
			t.Errorf("answer = %q, want empty", answer)
		}
	default:
		t.Fatal("no answer was delivered")
	}
}

func TestQuestionViewListsOptions(t *testing.T) {
	c, _ := newQuestionChat()
	view := c.questionView()
	for _, want := range []string{"Which direction?", "1) Editorial", "2) Minimal", "type your own"} {
		if !contains(view, want) {
			t.Errorf("the question view is missing %q:\n%s", want, view)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

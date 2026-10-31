package tui

import (
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/todo"
	"github.com/ramsterr/Wombat/internal/tools"
)

func TestTodoBarShowsPlan(t *testing.T) {
	c := testChat(t)
	c.resize(120, 34)
	c.ag = &agent.Agent{Env: &tools.Env{}}
	c.ag.Env.SetTodos([]todo.Item{
		{Content: "inspect the page", Status: todo.Completed},
		{Content: "fix the mobile layout", Status: todo.InProgress, Priority: todo.High},
		{Content: "run the checks", Status: todo.Pending},
	})
	got := stripANSI(c.todoBar())
	for _, want := range []string{"todos 1/3", "fix the mobile layout"} {
		if !strings.Contains(got, want) {
			t.Errorf("todo bar missing %q:\n%s", want, got)
		}
	}
}

func TestShowTodosCommand(t *testing.T) {
	c := testChat(t)
	c.ag = &agent.Agent{Env: &tools.Env{}}
	c.ag.Env.SetTodos([]todo.Item{{Content: "run the checks", Status: todo.Pending}})
	c.showTodos()
	if len(c.entries) == 0 || !strings.Contains(c.entries[len(c.entries)-1].text, "run the checks") {
		t.Fatalf("todo command output = %+v", c.entries)
	}
}

func TestTodoBarFitsNarrowLayout(t *testing.T) {
	c := testChat(t)
	c.resize(80, 24)
	c.ag = &agent.Agent{Env: &tools.Env{}}
	c.ag.Env.SetTodos([]todo.Item{
		{Content: "fix the mobile layout", Status: todo.InProgress},
		{Content: "run the checks", Status: todo.Pending},
	})
	c.layout()
	c.refresh()
	view := c.view()
	if !strings.Contains(view, "todos 0/2") {
		t.Fatalf("narrow view is missing the todo bar:\n%s", view)
	}
	if lines := strings.Split(view, "\n"); len(lines) > 24 {
		t.Fatalf("view is %d rows, terminal is 24", len(lines))
	}
}

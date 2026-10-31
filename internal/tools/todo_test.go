package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/todo"
)

func TestTodoWriteUpdatesPlan(t *testing.T) {
	env := &Env{}
	tool := &TodoWrite{}
	raw := json.RawMessage(`{"todos":[
		{"content":"inspect the page","status":"in_progress","priority":"high"},
		{"content":"fix the layout","status":"pending"}
	]}`)
	out, err := tool.Run(context.Background(), raw, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Todos 0/2 complete") || !strings.Contains(out, "[>] 1. inspect the page") {
		t.Fatalf("todo output = %q", out)
	}
	if !env.HasIncompleteTodos() {
		t.Fatal("the plan should have incomplete work")
	}
	items := env.Todos()
	if len(items) != 2 || items[0].Status != todo.InProgress || items[0].Priority != todo.High {
		t.Fatalf("env todos = %+v", items)
	}

	out, err = tool.Run(context.Background(), json.RawMessage(`{"todos":[
		{"content":"inspect the page","status":"completed"},
		{"content":"fix the layout","status":"completed"}
	]}`), env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Todos 2/2 complete") {
		t.Fatalf("completed output = %q", out)
	}
	if env.HasIncompleteTodos() {
		t.Fatal("the plan should be complete")
	}
}

func TestTodoWriteNotAvailableToWorkers(t *testing.T) {
	if WorkerEligible(&TodoWrite{}) {
		t.Fatal("todowrite is lead-only")
	}
}

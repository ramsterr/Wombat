package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/ramsterr/Wombat/internal/todo"
)

type todoState struct {
	mu    sync.Mutex
	items []todo.Item
}

var todoStateInit sync.Mutex

func (e *Env) todosState() *todoState {
	if e == nil {
		return nil
	}
	todoStateInit.Lock()
	defer todoStateInit.Unlock()
	if e.todoList == nil {
		e.todoList = &todoState{}
	}
	return e.todoList
}

// SetTodos replaces the current plan and notifies the session sink.
func (e *Env) SetTodos(items []todo.Item) {
	state := e.todosState()
	if state == nil {
		return
	}
	copied := append([]todo.Item(nil), items...)
	state.mu.Lock()
	state.items = copied
	state.mu.Unlock()
	if e.OnTodosChanged != nil {
		e.OnTodosChanged(append([]todo.Item(nil), copied...))
	}
}

// Todos returns a copy of the current plan.
func (e *Env) Todos() []todo.Item {
	state := e.todosState()
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return append([]todo.Item(nil), state.items...)
}

// HasIncompleteTodos reports whether any todo still needs work.
func (e *Env) HasIncompleteTodos() bool {
	return todo.Incomplete(e.Todos())
}

// TodoNote is the current plan for the system prompt.
func (e *Env) TodoNote() string {
	items := e.Todos()
	if len(items) == 0 {
		return ""
	}
	return "Current todo list:\n" + todo.Format(items) +
		"\nFinish every pending or in-progress item and mark it completed with the plan tool before asking for a review or handing back."
}

// TodoWrite replaces the task's todo list. It is lead-only: workers get a
// self-contained brief and should not maintain a second plan.
type TodoWrite struct{}

func (t *TodoWrite) Name() string  { return "todowrite" }
func (t *TodoWrite) Class() string { return "read" }
func (t *TodoWrite) Description() string {
	return "Write the task's todo list. Use it for multi-step work before the first edit. " +
		"Replace the whole list each time. Keep at most one item in_progress. " +
		"Mark each item completed as you finish; a preview review waits until no pending or in-progress items remain."
}
func (t *TodoWrite) Schema() map[string]any {
	return obj(map[string]any{
		"todos": map[string]any{
			"type":        "array",
			"description": "the complete task plan, in order",
			"minItems":    1,
			"maxItems":    50,
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content":  map[string]any{"type": "string", "description": "short imperative task"},
					"status":   map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "cancelled"}, "description": "current state of this task"},
					"priority": map[string]any{"type": "string", "enum": []string{"high", "medium", "low"}, "description": "optional ordering hint"},
				},
				"required": []string{"content", "status"},
			},
		},
	}, "todos")
}
func (t *TodoWrite) Run(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
	if env == nil {
		return "", fmt.Errorf("no environment")
	}
	var a struct {
		Todos []todo.Item `json:"todos"`
	}
	if err := decodeInto(raw, &a); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	items, err := todo.Normalize(a.Todos)
	if err != nil {
		return "", err
	}
	env.SetTodos(items)
	return todo.Format(items), nil
}
func (t *TodoWrite) AllowedInWorkers() bool { return false }

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ramsterr/Wombat/internal/todo"
)

// TodoWrite replaces the session's visible task list. It is the anchor the
// user watches while parallel work runs.
type TodoWrite struct{}

func (t *TodoWrite) Name() string  { return "todo_write" }
func (t *TodoWrite) Class() string { return "plan" }
func (t *TodoWrite) Description() string {
	return "Replace the session todo list with the current plan. Use it for any task with three or more steps, before delegating, and whenever a step starts or finishes. " +
		"Keep exactly one item in_progress unless independent work is running in parallel. The user sees this list as the live anchor, so keep it short and concrete."
}

func (t *TodoWrite) Schema() map[string]any {
	item := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":      map[string]any{"type": "string", "description": "short stable id; reused when updating"},
			"content": map[string]any{"type": "string", "description": "one concrete task, imperative form"},
			"status": map[string]any{"type": "string",
				"enum":        []string{"pending", "in_progress", "completed", "cancelled", "blocked"},
				"description": "current state"},
		},
		"required": []string{"content", "status"},
	}
	return obj(map[string]any{
		"todos": map[string]any{
			"type":        "array",
			"description": "the complete list, in display order (max 32); an empty list clears it",
			"maxItems":    todo.MaxItems,
			"items":       item,
		},
	}, "todos")
}

func (t *TodoWrite) Run(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
	var args struct {
		Todos []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	items := make([]todo.Item, 0, len(args.Todos))
	for _, item := range args.Todos {
		items = append(items, todo.Item{ID: item.ID, Content: item.Content, Status: todo.Status(item.Status)})
	}
	store := env.TodoStore()
	if err := store.Set(items); err != nil {
		return "", fmt.Errorf("save todo list: %w", err)
	}
	brief := store.Brief()
	if brief == "" {
		return "todo list cleared", nil
	}
	return "todo list updated\n" + brief, nil
}

// TodoRead returns the current list. It is a cheap read for resuming a
// session or before updating the list.
type TodoRead struct{}

func (t *TodoRead) Name() string  { return "todo_read" }
func (t *TodoRead) Class() string { return "read" }
func (t *TodoRead) Description() string {
	return "Read the current session todo list. Use it when resuming or before rewriting the list with todo_write."
}

func (t *TodoRead) Schema() map[string]any { return obj(map[string]any{}) }

func (t *TodoRead) Run(_ context.Context, _ json.RawMessage, env *Env) (string, error) {
	brief := env.TodoStore().Brief()
	if strings.TrimSpace(brief) == "" {
		return "todo list is empty", nil
	}
	return brief, nil
}

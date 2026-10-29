package orchestrator

import (
	"github.com/ramsterr/Wombat/internal/todo"
)

// syncTodos mirrors the ledger into the session todo list. The todo store is
// the user-visible anchor; the ledger stays the execution record.
func (o *Orchestrator) syncTodos() {
	o.mu.Lock()
	o.syncTodosLocked()
	o.mu.Unlock()
}

// syncTodosLocked is syncTodos for callers already holding o.mu.
func (o *Orchestrator) syncTodosLocked() {
	if o.lead == nil {
		return
	}
	store := o.lead.Env.Todos
	if store == nil || o.ledger == nil || len(o.ledger.Steps) == 0 {
		return
	}
	items := make([]todo.Item, 0, len(o.ledger.Steps))
	for _, step := range o.ledger.Steps {
		content := step.Title
		if content == "" {
			content = step.Objective
		}
		if content == "" {
			continue
		}
		items = append(items, todo.Item{
			ID:      step.ID,
			Content: content,
			Status:  todoStatus(step.Status),
		})
	}
	if len(items) > 0 {
		_ = store.Set(items)
	}
}

func todoStatus(status StepStatus) todo.Status {
	switch status {
	case StepCompleted:
		return todo.Completed
	case StepInProgress:
		return todo.InProgress
	case StepFailed, StepBlocked:
		return todo.Blocked
	default:
		return todo.Pending
	}
}

package todo

import (
	"fmt"
	"strings"
)

// Status is the state of one todo item.
type Status string

const (
	Pending    Status = "pending"
	InProgress Status = "in_progress"
	Completed  Status = "completed"
	Cancelled  Status = "cancelled"
)

// Priority is an optional ordering hint.
type Priority string

const (
	High   Priority = "high"
	Medium Priority = "medium"
	Low    Priority = "low"
)

// Item is one step in a task plan.
type Item struct {
	Content  string   `json:"content"`
	Status   Status   `json:"status"`
	Priority Priority `json:"priority,omitempty"`
}

// Done reports whether the item no longer needs work.
func (i Item) Done() bool {
	return i.Status == Completed || i.Status == Cancelled
}

// Normalize trims and validates a todo list. It defaults a missing status to
// pending and a missing priority to medium.
func Normalize(items []Item) ([]Item, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("at least one todo is required")
	}
	if len(items) > 50 {
		return nil, fmt.Errorf("at most 50 todos are allowed")
	}
	out := make([]Item, 0, len(items))
	inProgress := 0
	for i, item := range items {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			return nil, fmt.Errorf("todo %d: content is required", i+1)
		}
		status := Status(strings.ToLower(strings.TrimSpace(string(item.Status))))
		if status == "" {
			status = Pending
		}
		switch status {
		case Pending, InProgress, Completed, Cancelled:
		default:
			return nil, fmt.Errorf("todo %d: unknown status %q", i+1, item.Status)
		}
		if status == InProgress {
			inProgress++
		}
		priority := Priority(strings.ToLower(strings.TrimSpace(string(item.Priority))))
		if priority == "" {
			priority = Medium
		}
		switch priority {
		case High, Medium, Low:
		default:
			return nil, fmt.Errorf("todo %d: unknown priority %q", i+1, item.Priority)
		}
		out = append(out, Item{Content: content, Status: status, Priority: priority})
	}
	if inProgress > 1 {
		return nil, fmt.Errorf("only one todo may be in_progress")
	}
	return out, nil
}

// Incomplete reports whether any item is still pending or in progress.
func Incomplete(items []Item) bool {
	for _, item := range items {
		if !item.Done() {
			return true
		}
	}
	return false
}

// Counts returns completed and total item counts.
func Counts(items []Item) (done, total int) {
	for _, item := range items {
		if item.Status == Completed {
			done++
		}
	}
	return done, len(items)
}

// Format renders a compact list the model and the TUI can read.
func Format(items []Item) string {
	done, total := Counts(items)
	var b strings.Builder
	fmt.Fprintf(&b, "Todos %d/%d complete", done, total)
	for i, item := range items {
		mark := "[ ]"
		switch item.Status {
		case Completed:
			mark = "[x]"
		case Cancelled:
			mark = "[-]"
		case InProgress:
			mark = "[>]"
		}
		fmt.Fprintf(&b, "\n%s %d. %s", mark, i+1, item.Content)
		if item.Priority == High {
			b.WriteString(" (high)")
		}
	}
	return b.String()
}

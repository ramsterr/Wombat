// Package todo keeps the session's visible task list. The agent updates it
// with the todo_write tool, the orchestrator mirrors its ledger into it, and
// the TUI renders it as the session anchor instead of a stream of tool calls.
package todo

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// MaxItems caps one list.
const MaxItems = 32

// Status is one item's state.
type Status string

const (
	Pending    Status = "pending"
	InProgress Status = "in_progress"
	Completed  Status = "completed"
	Cancelled  Status = "cancelled"
	Blocked    Status = "blocked"
)

// Item is one task.
type Item struct {
	ID      string `json:"id,omitempty"`
	Content string `json:"content"`
	Status  Status `json:"status"`
}

// PathForSession is where a session's todo list persists.
func PathForSession(sessionPath string) string {
	if strings.TrimSpace(sessionPath) == "" {
		return ""
	}
	return strings.TrimSuffix(sessionPath, filepath.Ext(sessionPath)) + ".todos.json"
}

// Store is an in-memory list with optional JSON persistence. It is safe for
// the tool goroutine and the TUI to use concurrently.
type Store struct {
	mu   sync.Mutex
	path string
	list []Item
}

// New loads a store from path. An empty path keeps it in memory.
func New(path string) *Store {
	s := &Store{path: path}
	if path != "" {
		_ = s.load()
	}
	return s
}

// Path is where the store persists, or empty for an in-memory store.
func (s *Store) Path() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.path
}

// SetPath changes where the store persists and loads that file.
func (s *Store) SetPath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == path {
		return
	}
	s.path = path
	if path != "" {
		_ = s.loadLocked()
	}
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	var list []Item
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	s.list = sanitize(list)
	return nil
}

// Reload re-reads the persisted list. It lets a second process or a
// background scheduler publish progress without losing the shared store.
func (s *Store) Reload() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

// Items returns a copy of the list.
func (s *Store) Items() []Item {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Item(nil), s.list...)
}

// Set replaces the list, normalizes it, and persists it.
func (s *Store) Set(list []Item) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = sanitize(list)
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

// Clear empties the list.
func (s *Store) Clear() error { return s.Set(nil) }

// Brief is a compact text view for prompts and tool results.
func (s *Store) Brief() string {
	if s == nil {
		return ""
	}
	items := s.Items()
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	for _, item := range items {
		fmt.Fprintf(&b, "%s %s\n", glyph(item.Status), item.Content)
	}
	done, total := s.Progress()
	fmt.Fprintf(&b, "%d/%d complete", done, total)
	return strings.TrimSpace(b.String())
}

// Progress counts completed items.
func (s *Store) Progress() (done, total int) {
	if s == nil {
		return 0, 0
	}
	for _, item := range s.Items() {
		total++
		if item.Status == Completed || item.Status == Cancelled {
			done++
		}
	}
	return done, total
}

// FromStrings builds a list from bare content, marking the first unfinished
// item in progress. It is a convenience for callers that do not track ids.
func FromStrings(lines []string) []Item {
	out := make([]Item, 0, len(lines))
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		status := Pending
		if i == 0 {
			status = InProgress
		}
		out = append(out, Item{Content: line, Status: status})
	}
	return out
}

func sanitize(list []Item) []Item {
	out := make([]Item, 0, len(list))
	seen := map[string]bool{}
	for _, item := range list {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = fmt.Sprintf("t%d", len(out)+1)
		}
		if seen[id] {
			continue
		}
		if len(content) > 500 {
			content = content[:500] + "…"
		}
		status := Status(strings.ToLower(strings.TrimSpace(string(item.Status))))
		switch status {
		case Pending, InProgress, Completed, Cancelled, Blocked:
		default:
			status = Pending
		}
		seen[id] = true
		out = append(out, Item{ID: id, Content: content, Status: status})
		if len(out) >= MaxItems {
			break
		}
	}
	return out
}

func glyph(status Status) string {
	switch status {
	case Completed:
		return "[x]"
	case Cancelled:
		return "[-]"
	case InProgress:
		return "[>]"
	case Blocked:
		return "[!]"
	default:
		return "[ ]"
	}
}

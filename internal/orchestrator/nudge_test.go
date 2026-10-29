package orchestrator

import (
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/todo"
	"github.com/ramsterr/Wombat/internal/tools"
)

func TestLooksMultiStep(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"build a landing page, write the copy, and render a video", true},
		{"fix the failing test and add coverage", true},
		{"update the README and CHANGELOG", true},
		{"research the options then write a recommendation", true},
		{"make a video", false},
		{"fix the bug", false},
		{"read the file and explain it", false},
		{"what does this function do?", false},
		{"how do I restart the server?", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := looksMultiStep(tc.input); got != tc.want {
			t.Errorf("looksMultiStep(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestInputNotePushesPlanFirst(t *testing.T) {
	o := &Orchestrator{ledger: &Ledger{}, cfg: Config{MaxParallelScouts: 3}}
	note := o.inputNote("build a site, write the copy, and render a video")
	for _, want := range []string{"Planner directive", "update_plan", "run_plan", "parallel wave", "todo_write"} {
		if !strings.Contains(note, want) {
			t.Errorf("note missing %q:\n%s", want, note)
		}
	}
	if got := o.inputNote("read the file and explain it"); got != "" {
		t.Errorf("trivial request got a nudge: %q", got)
	}
}

func TestInputNoteRespectsActiveLedger(t *testing.T) {
	o := &Orchestrator{
		ledger: &Ledger{Steps: []Step{{ID: "a", Title: "A", Status: StepInProgress}}},
		cfg:    Config{MaxParallelScouts: 3},
	}
	note := o.inputNote("write the copy and render a video")
	if !strings.Contains(note, "active ledger") {
		t.Errorf("note = %q", note)
	}
	if strings.Contains(note, "first tool call must be update_plan") {
		t.Errorf("active ledger should not restart planning: %q", note)
	}
}

func TestSyncTodosMirrorsLedgerStatuses(t *testing.T) {
	store := todo.New("")
	o := &Orchestrator{
		ledger: &Ledger{Steps: []Step{
			{ID: "a", Title: "Research", Status: StepPending},
			{ID: "b", Title: "Build", Status: StepInProgress},
			{ID: "c", Title: "Check", Status: StepCompleted},
			{ID: "d", Title: "Blocked", Status: StepFailed},
		}},
		lead: &agent.Agent{Env: &tools.Env{Todos: store}},
	}
	o.syncTodos()
	items := store.Items()
	if len(items) != 4 {
		t.Fatalf("items = %+v", items)
	}
	want := []todo.Status{todo.Pending, todo.InProgress, todo.Completed, todo.Blocked}
	for i, status := range want {
		if items[i].Status != status {
			t.Errorf("item %d = %s, want %s", i, items[i].Status, status)
		}
	}
}

func TestSyncTodosIsNilSafe(t *testing.T) {
	o := &Orchestrator{ledger: &Ledger{Steps: []Step{{ID: "a", Title: "A", Status: StepPending}}}}
	o.syncTodos()
}

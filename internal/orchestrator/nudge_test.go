package orchestrator

import (
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/llm"
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

func TestStepNoteSteersSerialWork(t *testing.T) {
	o := &Orchestrator{ledger: &Ledger{}, cfg: Config{MaxParallelScouts: 3}}
	o.inputNote("build a site, write the copy, and render a video")
	calls := []llm.ToolCall{{Name: "bash"}, {Name: "read"}, {Name: "bash"}}
	note := o.stepNote(4, calls)
	if !strings.Contains(note, "update_plan") || !strings.Contains(note, "run_plan") {
		t.Fatalf("plan steer = %q", note)
	}
	if again := o.stepNote(5, calls); again != "" {
		t.Fatalf("steer should fire once, got %q", again)
	}

	planned := &Orchestrator{ledger: &Ledger{}, cfg: Config{MaxParallelScouts: 3}}
	planned.inputNote("build a site, write the copy, and render a video")
	if got := planned.stepNote(4, []llm.ToolCall{{Name: "update_plan"}}); got != "" {
		t.Fatalf("planning should satisfy the steer: %q", got)
	}
}

func TestStepNoteStopsEndlessExploration(t *testing.T) {
	o := &Orchestrator{ledger: &Ledger{}}
	o.inputNote("make a video")
	calls := make([]llm.ToolCall, 10)
	for i := range calls {
		calls[i] = llm.ToolCall{Name: "read"}
	}
	note := o.stepNote(11, calls)
	if !strings.Contains(note, "Stop exploring") {
		t.Fatalf("exploration steer = %q", note)
	}
	if got := o.stepNote(12, calls); got != "" {
		t.Fatalf("steer should fire once, got %q", got)
	}
}

func TestStepNotePushesBatchedScreenshotFixes(t *testing.T) {
	o := &Orchestrator{ledger: &Ledger{}}
	o.inputNote("build a page")
	shot := llm.ToolCall{Name: "browser", Arguments: `{"action":"screenshot"}`}
	edit := llm.ToolCall{Name: "edit", Arguments: `{"path":"index.html"}`}
	calls := []llm.ToolCall{shot, edit, shot}
	note := o.stepNote(4, calls)
	if !strings.Contains(note, "Screenshot batching") || !strings.Contains(note, "list every visible defect") {
		t.Fatalf("screenshot nudge = %q", note)
	}
	if again := o.stepNote(5, calls); again != "" {
		t.Fatalf("screenshot nudge should fire once, got %q", again)
	}
}

func TestStepNoteAllowsBatchBetweenScreenshots(t *testing.T) {
	o := &Orchestrator{ledger: &Ledger{}}
	o.inputNote("build a page")
	shot := llm.ToolCall{Name: "browser", Arguments: `{"action":"screenshot"}`}
	patch := llm.ToolCall{Name: "apply_patch", Arguments: `{"patch":"..."}`}
	if note := o.stepNote(4, []llm.ToolCall{shot, patch, shot}); note != "" {
		t.Fatalf("a batch edit should not trigger the nudge: %q", note)
	}
}

func TestStepNoteRoutesBashReadsToDedicatedTools(t *testing.T) {
	o := &Orchestrator{ledger: &Ledger{}}
	o.inputNote("make a page")
	calls := []llm.ToolCall{
		{Name: "bash", Arguments: `{"command":"cat README.md"}`},
		{Name: "bash", Arguments: `{"command":"grep -n wombat README.md"}`},
		{Name: "bash", Arguments: `{"command":"ls internal"}`},
	}
	note := o.stepNote(4, calls)
	if !strings.Contains(note, "Tool routing") || !strings.Contains(note, "read, grep, glob") {
		t.Fatalf("routing nudge = %q", note)
	}
	if again := o.stepNote(5, calls); again != "" {
		t.Fatalf("routing nudge should fire once, got %q", again)
	}
}

package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ramsterr/Wombat/internal/llm"
)

type fleetProvider struct {
	mu        sync.Mutex
	leadCalls int
	ready     int
	both      chan struct{}
}

func (p *fleetProvider) Name() string { return "fleet" }
func (p *fleetProvider) ListModels(context.Context) ([]string, error) {
	return []string{"fleet-model"}, nil
}
func (p *fleetProvider) Chat(ctx context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	if strings.Contains(req.System, "## Delegated worker") {
		p.mu.Lock()
		p.ready++
		if p.ready == 2 {
			close(p.both)
		}
		p.mu.Unlock()
		select {
		case <-p.both:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &llm.Result{
			Content: `{"status":"completed","summary":"scout finished","findings":[]}`,
			Usage:   llm.Usage{In: 5, Out: 2},
		}, nil
	}
	p.leadCalls++
	switch p.leadCalls {
	case 1:
		tasks, _ := json.Marshal(delegateManyArgs{Tasks: []delegateArgs{
			{StepID: "s1", Title: "One", Objective: "Investigate one"},
			{StepID: "s2", Title: "Two", Objective: "Investigate two"},
		}})
		return &llm.Result{
			ToolCalls: []llm.ToolCall{{ID: "c1", Name: "delegate_many", Arguments: string(tasks)}},
			Usage:     llm.Usage{In: 10, Out: 4},
		}, nil
	case 2:
		return &llm.Result{Content: "done", Usage: llm.Usage{In: 8, Out: 1}}, nil
	default:
		return nil, fmt.Errorf("unexpected lead call %d", p.leadCalls)
	}
}

func TestDelegateManyRunsScoutsInParallel(t *testing.T) {
	p := &fleetProvider{both: make(chan struct{})}
	o, ag, home := newOrch(t, p, Config{
		MaxDelegations:    2,
		MaxParallelScouts: 2,
		WorkerMaxSteps:    4,
		WorkerTimeout:     2 * time.Second,
	})
	if _, err := o.updatePlan("survey", nil, []Step{
		{ID: "s1", Title: "One", Status: StepInProgress},
		{ID: "s2", Title: "Two", Status: StepInProgress},
	}); err != nil {
		t.Fatal(err)
	}
	if err := ag.Run(context.Background(), "investigate both"); err != nil {
		t.Fatal(err)
	}
	if o.Delegations() != 2 {
		t.Fatalf("delegations = %d", o.Delegations())
	}
	ledger, _, err := LoadLedger(LedgerPath(ag.Sess.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Steps) != 2 || ledger.Steps[0].Status != StepCompleted || ledger.Steps[1].Status != StepCompleted {
		t.Fatalf("ledger = %+v", ledger.Steps)
	}
	stats := ag.Usage()
	if stats.Calls != 4 || stats.In != 28 || stats.Out != 9 {
		t.Fatalf("usage = %+v", stats)
	}
	sessions, _ := filepath.Glob(filepath.Join(home, "workers", "sessions", "*", "*.jsonl"))
	if len(sessions) != 2 {
		t.Fatalf("worker sessions = %v", sessions)
	}
	found := false
	for _, m := range ag.Sess.Messages {
		if m.Role == "tool" && m.Name == "delegate_many" && strings.Contains(m.Content, `"tasks"`) {
			found = true
		}
	}
	if !found {
		t.Fatal("fleet tool result missing")
	}
}

func TestDelegateManyValidation(t *testing.T) {
	o, _, _ := newOrch(t, &scriptProvider{fn: func(int, llm.Request) (*llm.Result, error) {
		return &llm.Result{Content: "ok"}, nil
	}}, Config{MaxDelegations: 6})
	if _, err := o.updatePlan("survey", nil, []Step{
		{ID: "a", Title: "A", Status: StepPending},
		{ID: "b", Title: "B", Status: StepPending, DependsOn: []string{"a"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.delegateMany(context.Background(), []delegateArgs{{Title: "only", Objective: "x"}}); err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Fatalf("single-task fleet error = %v", err)
	}
	cases := []struct {
		name string
		args []delegateArgs
		want string
	}{
		{"duplicate", []delegateArgs{{StepID: "a", Title: "A", Objective: "x"}, {StepID: "a", Title: "B", Objective: "y"}}, "twice"},
		{"dependent", []delegateArgs{{StepID: "a", Title: "A", Objective: "x"}, {StepID: "b", Title: "B", Objective: "y"}}, "depend"},
		{"too many", make([]delegateArgs, maxFleetTasks+1), "too many"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := o.prepareDelegations(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	if o.delegations != 0 || o.Delegations() != 0 {
		t.Fatalf("failed validation consumed budget: %d/%d", o.delegations, o.Delegations())
	}
}

func TestDelegateManyIsolatesFailures(t *testing.T) {
	p := &scriptProvider{}
	p.fn = func(call int, req llm.Request) (*llm.Result, error) {
		if strings.Contains(req.System, "## Delegated worker") {
			for _, m := range req.Messages {
				if strings.Contains(m.Content, "fail this") {
					return &llm.Result{
						Content: `{"status":"failed","summary":"cannot read the target"}`,
						Usage:   llm.Usage{In: 4, Out: 2},
					}, nil
				}
			}
			return &llm.Result{
				Content: `{"status":"completed","summary":"scout ok","findings":[]}`,
				Usage:   llm.Usage{In: 4, Out: 2},
			}, nil
		}
		return nil, fmt.Errorf("unexpected lead call %d", call)
	}
	o, ag, _ := newOrch(t, p, Config{MaxDelegations: 2, MaxParallelScouts: 2})
	if _, err := o.updatePlan("survey", nil, []Step{
		{ID: "s1", Title: "Fail", Status: StepPending},
		{ID: "s2", Title: "Pass", Status: StepPending},
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := o.delegateMany(context.Background(), []delegateArgs{
		{StepID: "s1", Title: "Fail", Objective: "fail this"},
		{StepID: "s2", Title: "Pass", Objective: "pass this"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var fleet fleetResult
	if err := json.Unmarshal([]byte(raw), &fleet); err != nil {
		t.Fatal(err)
	}
	if len(fleet.Tasks) != 2 || fleet.Tasks[0].Report.Status != "failed" || fleet.Tasks[1].Report.Status != "completed" {
		t.Fatalf("fleet = %+v", fleet.Tasks)
	}
	if fleet.Usage.Calls != 2 || fleet.Usage.In != 8 || fleet.Usage.Out != 4 {
		t.Fatalf("usage = %+v", fleet.Usage)
	}
	ledger, _, err := LoadLedger(LedgerPath(ag.Sess.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Steps[0].Status != StepFailed || ledger.Steps[1].Status != StepCompleted {
		t.Fatalf("ledger = %+v", ledger.Steps)
	}
}

func TestWorkerTokenBudget(t *testing.T) {
	p := &scriptProvider{}
	p.fn = func(call int, req llm.Request) (*llm.Result, error) {
		if strings.Contains(req.System, "## Delegated worker") {
			return &llm.Result{
				Content: `{"status":"completed","summary":"expensive scout"}`,
				Usage:   llm.Usage{In: 2000, Out: 1000},
			}, nil
		}
		return nil, fmt.Errorf("unexpected lead call %d", call)
	}
	o, _, _ := newOrch(t, p, Config{
		MaxDelegations:        2,
		WorkerTokenBudget:     1500,
		TurnWorkerTokenBudget: 1500,
	})
	raw, err := o.delegate(context.Background(), delegateArgs{Title: "Survey", Objective: "Spend tokens"})
	if err != nil {
		t.Fatal(err)
	}
	var result delegateResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Report.Status != "failed" || !strings.Contains(result.Warning, "token budget") {
		t.Fatalf("result = %+v warning=%q", result.Report, result.Warning)
	}
	o.mu.Lock()
	used := o.workerTokens
	o.mu.Unlock()
	if used != 3000 {
		t.Fatalf("worker tokens = %d, want 3000", used)
	}
	_, err = o.delegate(context.Background(), delegateArgs{Title: "Again", Objective: "Spend more"})
	if err == nil || !strings.Contains(err.Error(), "token budget exhausted") {
		t.Fatalf("second delegation error = %v", err)
	}
}

func TestFleetBudgetShare(t *testing.T) {
	o, _, _ := newOrch(t, &scriptProvider{fn: func(int, llm.Request) (*llm.Result, error) {
		return &llm.Result{Content: "ok"}, nil
	}}, Config{
		MaxDelegations:        6,
		WorkerTokenBudget:     1000,
		TurnWorkerTokenBudget: 1500,
	})
	_, err := o.prepareDelegations([]delegateArgs{
		{Title: "One", Objective: "x"},
		{Title: "Two", Objective: "y"},
	})
	if err == nil || !strings.Contains(err.Error(), "not enough worker token budget") {
		t.Fatalf("error = %v", err)
	}
}

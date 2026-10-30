package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

type fleetProvider struct {
	mu     sync.Mutex
	n      int
	models []string
}

func (p *fleetProvider) Name() string                                 { return "mock" }
func (p *fleetProvider) ListModels(context.Context) ([]string, error) { return []string{"mock"}, nil }
func (p *fleetProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	p.mu.Lock()
	p.n++
	n := p.n
	p.models = append(p.models, req.Model)
	p.mu.Unlock()
	report := map[string]any{"status": "completed", "summary": fmt.Sprintf("scout %d", n),
		"findings": []map[string]any{{"claim": fmt.Sprintf("angle %d", n), "evidence": "README.md:1"}},
		"changes":  []any{}, "tests": []any{}, "risks": []any{}, "next_steps": []any{}}
	body, _ := json.Marshal(report)
	return &llm.Result{Content: string(body), Usage: llm.Usage{In: 50, Out: 20}}, nil
}

// TestFleetRunsInParallel checks that a fleet of five scouts runs at once and
// returns one report each.
func TestFleetRunsInParallel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	sess, err := session.CreateAt(home, t.TempDir(), "mock", "mock", "t")
	if err != nil {
		t.Fatal(err)
	}
	prov := &fleetProvider{}
	ag := &agent.Agent{Provider: prov, Model: "mock", Tools: tools.Registry(),
		Env:  &tools.Env{Dir: sess.Meta.Cwd, Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} }},
		Sess: sess, MaxSteps: 8}
	o, err := Attach(ag, Config{MaxDelegations: 6, MaxParallelScouts: 5, WorkerMaxSteps: 16,
		WorkerTokenBudget: 50000, TurnWorkerTokenBudget: 260000, WorkerTimeout: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	steps := []Step{}
	for i := 0; i < 5; i++ {
		steps = append(steps, Step{ID: fmt.Sprintf("s%d", i+1), Title: "S", Status: StepInProgress})
	}
	if _, err := o.updatePlan("research", nil, steps); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	tasks := []delegateArgs{}
	for i := 1; i <= 5; i++ {
		tasks = append(tasks, delegateArgs{StepID: fmt.Sprintf("s%d", i), Title: "t",
			Objective: fmt.Sprintf("angle %d", i), Deliverable: "findings"})
	}
	out, err := o.delegateMany(context.Background(), tasks)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	fmt.Printf("5 scouts finished in %s\n", elapsed.Round(time.Millisecond))
	if elapsed > 30*time.Second {
		t.Errorf("the fleet took %s, which does not look parallel", elapsed)
	}
	var parsed struct {
		Tasks []struct {
			Report struct {
				Summary string `json:"summary"`
			} `json:"report"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("reports: %d\n", len(parsed.Tasks))
	if len(parsed.Tasks) != 5 {
		t.Fatalf("expected 5 reports, got %d", len(parsed.Tasks))
	}
	for _, task := range parsed.Tasks {
		if task.Report.Summary == "" {
			t.Error("a scout returned no summary")
		}
	}
}

// TestScoutsUseTheirOwnModel checks the model routing: the read-only
// workers can run on a cheaper model than the lead.
func TestScoutsUseTheirOwnModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	sess, err := session.CreateAt(home, t.TempDir(), "mock", "lead-model", "t")
	if err != nil {
		t.Fatal(err)
	}
	prov := &fleetProvider{}
	ag := &agent.Agent{Provider: prov, Model: "lead-model", Tools: tools.Registry(),
		Env:  &tools.Env{Dir: sess.Meta.Cwd, Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} }},
		Sess: sess, MaxSteps: 8}
	o, err := Attach(ag, Config{MaxDelegations: 6, MaxParallelScouts: 5, WorkerMaxSteps: 4,
		WorkerTokenBudget: 50000, TurnWorkerTokenBudget: 260000, ScoutModel: "cheap-model"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.updatePlan("research", nil, []Step{
		{ID: "s1", Title: "S", Status: StepInProgress},
		{ID: "s2", Title: "S", Status: StepInProgress},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := o.delegateMany(context.Background(), []delegateArgs{
		{StepID: "s1", Title: "t", Objective: "one", Deliverable: "d"},
		{StepID: "s2", Title: "t", Objective: "two", Deliverable: "d"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("out: %s", out)
	prov.mu.Lock()
	models := append([]string(nil), prov.models...)
	prov.mu.Unlock()
	if len(models) == 0 {
		t.Fatal("the workers never called the model")
	}
	for _, m := range models {
		if m != "cheap-model" {
			t.Errorf("a worker used %q, want cheap-model", m)
		}
	}
}

type dagProvider struct {
	mu     sync.Mutex
	events []string
	bad    []string
}

func (p *dagProvider) Name() string                                 { return "dag-mock" }
func (p *dagProvider) ListModels(context.Context) ([]string, error) { return []string{"dag-mock"}, nil }
func (p *dagProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	brief := ""
	system := req.System
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			brief = msg.Content
		}
	}
	id := ""
	if i := strings.Index(system, "Ledger step: ["); i >= 0 {
		id = system[i+len("Ledger step: ["):]
		if j := strings.Index(id, "]"); j >= 0 {
			id = id[:j]
		}
	}
	p.mu.Lock()
	p.events = append(p.events, "start:"+id)
	if strings.Contains(brief, "do beta") {
		if strings.Contains(brief, "## Upstream results") && strings.Contains(brief, "alpha result") {
			p.events = append(p.events, "beta-saw-alpha")
		} else {
			p.bad = append(p.bad, brief)
		}
	}
	p.mu.Unlock()
	summary := "result-" + id
	if strings.Contains(brief, "do alpha") {
		summary = "alpha result"
	}
	if strings.Contains(brief, "do beta") {
		summary = "beta result"
	}
	if strings.Contains(brief, "do gamma") {
		summary = "gamma result"
	}
	p.mu.Lock()
	p.events = append(p.events, "finish:"+id)
	p.mu.Unlock()
	return &llm.Result{Content: `{"status":"completed","summary":"` + summary + `"}`}, nil
}

func TestDelegateManyDAGWaitsAndPassesUpstreamReports(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	sess, err := session.CreateAt(home, t.TempDir(), "dag-mock", "dag-mock", "t")
	if err != nil {
		t.Fatal(err)
	}
	prov := &dagProvider{}
	ag := &agent.Agent{Provider: prov, Model: "dag-mock", Tools: tools.Registry(),
		Env:  &tools.Env{Dir: sess.Meta.Cwd, Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} }},
		Sess: sess, MaxSteps: 8}
	o, err := Attach(ag, Config{MaxParallelScouts: 5, WorkerTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.updatePlan("goal", nil, []Step{
		{ID: "a", Title: "A", Status: StepInProgress},
		{ID: "b", Title: "B", Status: StepInProgress, DependsOn: []string{"a"}},
		{ID: "c", Title: "C", Status: StepInProgress},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := o.delegateMany(context.Background(), []delegateArgs{
		{ID: "a", StepID: "a", Title: "A", Objective: "do alpha"},
		{ID: "b", StepID: "b", Title: "B", Objective: "do beta", DependsOn: []string{"a"}},
		{ID: "c", StepID: "c", Title: "C", Objective: "do gamma"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prov.bad) > 0 {
		t.Fatalf("dependent worker missing upstream context: %s", prov.bad[0])
	}
	joined := strings.Join(prov.events, ",")
	if !strings.Contains(joined, "finish:a") || !strings.Contains(joined, "start:b") {
		t.Fatalf("events = %s", joined)
	}
	if strings.Index(joined, "finish:a") > strings.Index(joined, "start:b") {
		t.Fatalf("dependent started before upstream finished: %s", joined)
	}
	if !strings.Contains(out, "beta result") || !strings.Contains(out, "gamma result") {
		t.Fatalf("out = %s", out)
	}
}

func TestDelegateManyRejectsDependencyCycle(t *testing.T) {
	o := &Orchestrator{ledger: &Ledger{}}
	_, err := o.delegateMany(context.Background(), []delegateArgs{
		{ID: "a", Title: "A", Objective: "a", DependsOn: []string{"b"}},
		{ID: "b", Title: "B", Objective: "b", DependsOn: []string{"a"}},
	})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("err = %v", err)
	}
}

func TestDelegateManyCanRunReadOnlyScouts(t *testing.T) {
	p := &scriptProvider{}
	p.fn = func(call int, req llm.Request) (*llm.Result, error) {
		if !strings.Contains(req.System, "read-only research worker") {
			t.Errorf("worker %d was not read-only: %s", call, req.System)
		}
		if strings.Contains(req.System, "full-access worker") {
			t.Errorf("worker %d was full-access", call)
		}
		return &llm.Result{
			Content: `{"status":"completed","summary":"read-only report","findings":[],"changes":[],"tests":[],"risks":[],"next_steps":[]}`,
			Usage:   llm.Usage{In: 3, Out: 2},
		}, nil
	}
	o, _, _ := newOrch(t, p, Config{FullWorkers: true, WorkerMaxSteps: 4})
	out, err := o.delegateMany(context.Background(), []delegateArgs{
		{Title: "A", Objective: "inspect A", ReadOnly: true},
		{Title: "B", Objective: "inspect B", ReadOnly: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "read-only report") {
		t.Fatalf("out = %s", out)
	}
}

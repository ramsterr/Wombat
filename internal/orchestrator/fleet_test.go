package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
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
		Sess: sess}
	o, err := Attach(ag, Config{ScoutModel: "cheap-model"})
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
		Sess: sess}
	o, err := Attach(ag, Config{ScoutModel: "cheap-model"})
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

func TestWorkerToolsIncludeWeb(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	sess, err := session.CreateAt(home, t.TempDir(), "mock", "mock", "t")
	if err != nil {
		t.Fatal(err)
	}
	ag := &agent.Agent{Provider: &fleetProvider{}, Model: "mock", Tools: tools.Registry(),
		Env:  &tools.Env{Dir: sess.Meta.Cwd},
		Sess: sess}
	o, err := Attach(ag, Config{})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range o.workerTools {
		names[tool.Name()] = true
	}
	for _, want := range []string{"bash", "read", "glob", "grep", "websearch", "webfetch"} {
		if !names[want] {
			t.Errorf("worker tools are missing %s: %v", want, names)
		}
	}
}

func TestFleetHasNoTaskOrStepCap(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	sess, err := session.CreateAt(home, t.TempDir(), "mock", "mock", "t")
	if err != nil {
		t.Fatal(err)
	}
	ag := &agent.Agent{Provider: &fleetProvider{}, Model: "mock", Tools: tools.Registry(),
		Env:  &tools.Env{Dir: sess.Meta.Cwd, Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} }},
		Sess: sess}
	o, err := Attach(ag, Config{})
	if err != nil {
		t.Fatal(err)
	}
	var steps []Step
	var tasks []delegateArgs
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("s%d", i)
		steps = append(steps, Step{ID: id, Title: "S", Status: StepInProgress})
		tasks = append(tasks, delegateArgs{StepID: id, Title: "t", Objective: "angle"})
	}
	if _, err := o.updatePlan("research", nil, steps); err != nil {
		t.Fatal(err)
	}
	batch, err := o.prepareDelegations(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.items) != 10 {
		t.Fatalf("tasks = %d, want 10", len(batch.items))
	}
	// Several scouts may share one ledger step; that must not reject the fleet.
	dupes := []delegateArgs{
		{StepID: "s0", Title: "a", Objective: "one"},
		{StepID: "s0", Title: "b", Objective: "two"},
	}
	if _, err := o.prepareDelegations(dupes); err != nil {
		t.Fatalf("duplicate step id rejected the fleet: %v", err)
	}
}

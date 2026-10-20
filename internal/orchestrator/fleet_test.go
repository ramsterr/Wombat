package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

type fleetProvider struct{ n int }

func (p *fleetProvider) Name() string                                 { return "mock" }
func (p *fleetProvider) ListModels(context.Context) ([]string, error) { return []string{"mock"}, nil }
func (p *fleetProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	p.n++
	report := map[string]any{"status": "completed", "summary": fmt.Sprintf("scout %d", p.n),
		"findings": []map[string]any{{"claim": fmt.Sprintf("angle %d", p.n), "evidence": "README.md:1"}},
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

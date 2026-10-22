package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ramsterr/Wombat/internal/llm"
)

// overlapPlanProvider holds the slow scout while letting the independent
// fast scout and the step that depends on it finish.
type overlapPlanProvider struct {
	slowStarted chan struct{}
	releaseSlow chan struct{}
	afterFast   chan struct{}
	slowOnce    sync.Once
	afterOnce   sync.Once
	writerCalls int
}

func (p *overlapPlanProvider) Name() string { return "overlap-plan" }
func (p *overlapPlanProvider) ListModels(context.Context) ([]string, error) {
	return []string{"overlap-plan"}, nil
}
func (p *overlapPlanProvider) Chat(ctx context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	switch {
	case strings.Contains(req.System, "[slow]"):
		p.slowOnce.Do(func() { close(p.slowStarted) })
		select {
		case <-p.releaseSlow:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case strings.Contains(req.System, "[after]"):
		p.afterOnce.Do(func() { close(p.afterFast) })
	case strings.Contains(req.System, "## Writer worker"):
		p.writerCalls++
		if p.writerCalls == 1 {
			return toolCall("w", "write", map[string]any{"path": "from-writer.txt", "content": "done"}), nil
		}
		return toolCall("r", "submit_report", map[string]any{"status": "completed", "summary": "wrote"}), nil
	}
	return &llm.Result{Content: `{"status":"completed","summary":"ok"}`, Usage: llm.Usage{In: 3, Out: 1}}, nil
}

func TestRunPlanMergesWriterWhileScoutIsSlow(t *testing.T) {
	p := &overlapPlanProvider{
		slowStarted: make(chan struct{}),
		releaseSlow: make(chan struct{}),
		afterFast:   make(chan struct{}),
	}
	o, repo := reviewRepo(t, p, Config{ReviewWriters: boolPtr(false)})
	if _, err := o.updatePlan("goal", nil, []Step{
		{ID: "slow", Title: "Slow scout", Kind: StepScout, Status: StepPending},
		{ID: "write", Title: "Fast writer", Kind: StepWrite, Files: []string{"from-writer.txt"}, Status: StepPending},
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := o.runPlan(context.Background(), runPlanArgs{MaxParallel: 2})
		done <- err
	}()
	select {
	case <-p.slowStarted:
	case <-time.After(3 * time.Second):
		close(p.releaseSlow)
		t.Fatal("the slow scout never started")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(repo, "from-writer.txt")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			close(p.releaseSlow)
			t.Fatal("writer merge waited for the slow scout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(p.releaseSlow)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("plan did not finish")
	}
}

func TestRunPlanDoesNotWaitForSlowStepToStartReadyWork(t *testing.T) {
	p := &overlapPlanProvider{
		slowStarted: make(chan struct{}),
		releaseSlow: make(chan struct{}),
		afterFast:   make(chan struct{}),
	}
	o, _ := reviewRepo(t, p, Config{})
	if _, err := o.updatePlan("goal", nil, []Step{
		{ID: "fast", Title: "Fast scout", Kind: StepScout, Status: StepPending},
		{ID: "slow", Title: "Slow scout", Kind: StepScout, Status: StepPending},
		{ID: "after", Title: "After fast", Kind: StepScout, Status: StepPending, DependsOn: []string{"fast"}},
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := o.runPlan(context.Background(), runPlanArgs{MaxParallel: 2})
		done <- err
	}()

	select {
	case <-p.slowStarted:
	case <-time.After(3 * time.Second):
		close(p.releaseSlow)
		t.Fatal("the slow scout never started")
	}
	select {
	case <-p.afterFast:
		// The dependent step ran while the slow independent step was still
		// blocked. Release the slow step and let the plan finish.
		close(p.releaseSlow)
	case <-time.After(3 * time.Second):
		close(p.releaseSlow)
		t.Fatal("ready work waited behind the slow step")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("plan did not finish")
	}
	for _, id := range []string{"fast", "slow", "after"} {
		if step := o.ledger.Find(id); step == nil || step.Status != StepCompleted {
			t.Fatalf("%s = %+v", id, step)
		}
	}
}

type flakyPlanProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *flakyPlanProvider) Name() string { return "flaky-plan" }
func (p *flakyPlanProvider) ListModels(context.Context) ([]string, error) {
	return []string{"flaky-plan"}, nil
}
func (p *flakyPlanProvider) Chat(ctx context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	if strings.Contains(req.System, "[flaky]") {
		p.mu.Lock()
		p.calls++
		calls := p.calls
		p.mu.Unlock()
		if calls == 1 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
	}
	return &llm.Result{Content: `{"status":"completed","summary":"recovered"}`, Usage: llm.Usage{In: 3, Out: 1}}, nil
}

func TestRunPlanRetriesTimedOutStep(t *testing.T) {
	p := &flakyPlanProvider{}
	o, _ := reviewRepo(t, p, Config{WorkerTimeout: 50 * time.Millisecond})
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "flaky", Title: "Flaky scout", Kind: StepScout, Status: StepPending,
	}}); err != nil {
		t.Fatal(err)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 {
		t.Fatalf("worker calls = %d, want 2", p.calls)
	}
	if !strings.Contains(out, "retried after") {
		t.Fatalf("missing retry warning:\n%s", out)
	}
	if step := o.ledger.Find("flaky"); step == nil || step.Status != StepCompleted {
		t.Fatalf("step = %+v\n%s", step, out)
	}
}

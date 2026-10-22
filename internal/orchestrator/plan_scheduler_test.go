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

type orderScoutProvider struct {
	mu    sync.Mutex
	order []string
}

func (p *orderScoutProvider) Name() string { return "order-scout" }
func (p *orderScoutProvider) ListModels(context.Context) ([]string, error) {
	return []string{"order-scout"}, nil
}
func (p *orderScoutProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	id := ""
	for _, line := range strings.Split(req.System, "\n") {
		if strings.HasPrefix(line, "Ledger step: [") {
			id = strings.TrimPrefix(line, "Ledger step: [")
			if end := strings.Index(id, "]"); end >= 0 {
				id = id[:end]
			}
			break
		}
	}
	p.mu.Lock()
	p.order = append(p.order, id)
	p.mu.Unlock()
	return &llm.Result{Content: `{"status":"completed","summary":"ok"}`}, nil
}
func (p *orderScoutProvider) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.order...)
}

func TestRunPlanPrioritizesCriticalPath(t *testing.T) {
	prov := &orderScoutProvider{}
	o, _, _ := newOrch(t, prov, Config{})
	if _, err := o.updatePlan("goal", nil, []Step{
		{ID: "a", Title: "Chain root", Status: StepPending},
		{ID: "b", Title: "Independent", Status: StepPending},
		{ID: "c", Title: "Chain middle", Status: StepPending, DependsOn: []string{"a"}},
		{ID: "d", Title: "Chain leaf", Status: StepPending, DependsOn: []string{"c"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.runPlan(context.Background(), runPlanArgs{MaxParallel: 1}); err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "c", "b", "d"}
	got := prov.seen()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

type backgroundPlanProvider struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *backgroundPlanProvider) Name() string { return "background-plan" }
func (p *backgroundPlanProvider) ListModels(context.Context) ([]string, error) {
	return []string{"background-plan"}, nil
}
func (p *backgroundPlanProvider) Chat(ctx context.Context, _ llm.Request, _ func(string)) (*llm.Result, error) {
	p.once.Do(func() { close(p.started) })
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &llm.Result{Content: `{"status":"completed","summary":"ok"}`}, nil
}

func TestRunPlanBackgroundCanBeJoined(t *testing.T) {
	p := &backgroundPlanProvider{started: make(chan struct{}), release: make(chan struct{})}
	o, _, _ := newOrch(t, p, Config{})
	if _, err := o.updatePlan("goal", nil, []Step{{ID: "slow", Title: "Slow", Status: StepPending}}); err != nil {
		t.Fatal(err)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{Background: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status": "running"`) {
		t.Fatalf("background start = %s", out)
	}
	select {
	case <-p.started:
	case <-time.After(3 * time.Second):
		t.Fatal("background plan never started")
	}
	joined := make(chan string, 1)
	go func() {
		res, err := o.runPlan(context.Background(), runPlanArgs{})
		if err != nil {
			t.Error(err)
		}
		joined <- res
	}()
	close(p.release)
	select {
	case res := <-joined:
		if !strings.Contains(res, `"status": "completed"`) {
			t.Fatalf("joined result = %s", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("join did not return")
	}
	if step := o.ledger.Find("slow"); step == nil || step.Status != StepCompleted {
		t.Fatalf("step = %+v", step)
	}
}

type cancelPlanProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *cancelPlanProvider) Name() string { return "cancel-plan" }
func (p *cancelPlanProvider) ListModels(context.Context) ([]string, error) {
	return []string{"cancel-plan"}, nil
}
func (p *cancelPlanProvider) Chat(ctx context.Context, _ llm.Request, _ func(string)) (*llm.Result, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.mu.Unlock()
	if n == 1 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &llm.Result{Content: `{"status":"completed","summary":"resumed"}`}, nil
}

func TestRunPlanCancelLeavesStepsPendingForResume(t *testing.T) {
	p := &cancelPlanProvider{}
	o, _, _ := newOrch(t, p, Config{})
	if _, err := o.updatePlan("goal", nil, []Step{
		{ID: "a", Title: "First", Status: StepPending},
		{ID: "b", Title: "Second", Status: StepPending, DependsOn: []string{"a"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.runPlan(context.Background(), runPlanArgs{Background: true, MaxParallel: 1}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		p.mu.Lock()
		started := p.calls > 0
		p.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("plan never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{Cancel: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status": "cancelled"`) {
		t.Fatalf("cancel result = %s", out)
	}
	if step := o.ledger.Find("a"); step == nil || step.Status != StepPending {
		t.Fatalf("a after cancel = %+v", step)
	}
	if step := o.ledger.Find("b"); step == nil || step.Status != StepPending {
		t.Fatalf("b after cancel = %+v", step)
	}
	resumed, err := o.runPlan(context.Background(), runPlanArgs{MaxParallel: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resumed, `"status": "completed"`) {
		t.Fatalf("resumed = %s", resumed)
	}
	for _, id := range []string{"a", "b"} {
		if step := o.ledger.Find(id); step == nil || step.Status != StepCompleted {
			t.Fatalf("%s after resume = %+v", id, step)
		}
	}
}

type turnPlanProvider struct {
	mu          sync.Mutex
	leadCalls   int
	workerCalls int
	started     chan struct{}
	once        sync.Once
}

func (p *turnPlanProvider) Name() string { return "turn-plan" }
func (p *turnPlanProvider) ListModels(context.Context) ([]string, error) {
	return []string{"turn-plan"}, nil
}
func (p *turnPlanProvider) Chat(ctx context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	if strings.Contains(req.System, "## Delegated worker") {
		p.mu.Lock()
		p.workerCalls++
		n := p.workerCalls
		p.mu.Unlock()
		if n == 1 {
			p.once.Do(func() { close(p.started) })
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &llm.Result{Content: `{"status":"completed","summary":"resumed"}`}, nil
	}
	p.mu.Lock()
	p.leadCalls++
	n := p.leadCalls
	p.mu.Unlock()
	switch n {
	case 1:
		return toolCall("p1", "run_plan", map[string]any{"background": true, "max_parallel": 1}), nil
	case 2:
		return &llm.Result{Content: "started it"}, nil
	case 3:
		return toolCall("p2", "run_plan", map[string]any{"max_parallel": 1}), nil
	default:
		return &llm.Result{Content: "all done"}, nil
	}
}

func TestRunEndCancelsBackgroundPlanAndNextTurnResumes(t *testing.T) {
	p := &turnPlanProvider{started: make(chan struct{})}
	o, ag, _ := newOrch(t, p, Config{})
	if _, err := o.updatePlan("goal", nil, []Step{{ID: "a", Title: "First", Status: StepPending}}); err != nil {
		t.Fatal(err)
	}
	if err := ag.Run(context.Background(), "start the plan in the background"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.started:
	default:
		t.Fatal("background plan never started")
	}
	if step := o.ledger.Find("a"); step == nil || step.Status != StepPending {
		t.Fatalf("step after turn = %+v", step)
	}
	if err := ag.Run(context.Background(), "resume the plan"); err != nil {
		t.Fatal(err)
	}
	if step := o.ledger.Find("a"); step == nil || step.Status != StepCompleted {
		t.Fatalf("step after resume = %+v", step)
	}
}

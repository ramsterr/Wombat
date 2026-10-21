package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/tools"
)

type runPlanTool struct{ o *Orchestrator }

type runPlanArgs struct {
	Steps         []string `json:"steps"`
	MaxParallel   int      `json:"max_parallel"`
	StopOnFailure bool     `json:"stop_on_failure"`
}

func (t *runPlanTool) Name() string  { return "run_plan" }
func (t *runPlanTool) Class() string { return "delegate" }
func (t *runPlanTool) Description() string {
	return "Execute the task ledger as a dependency graph. Ready steps run in parallel: scouts research, write steps run in isolated worktrees with owned files, an independent reviewer checks each patch, and passing patches merge automatically. Dependencies are respected wave by wave; failed steps block only their dependents. Use after update_plan for multi-step work instead of dispatching steps by hand."
}
func (t *runPlanTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"steps":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "optional step ids to run (default: every pending step)"},
			"max_parallel":    map[string]any{"type": "integer", "description": "maximum steps in one wave (default 4, max 6)"},
			"stop_on_failure": map[string]any{"type": "boolean", "description": "stop scheduling new waves after a failure (independent steps already running still finish)"},
		},
	}
}

func (t *runPlanTool) Run(ctx context.Context, raw json.RawMessage, _ *tools.Env) (string, error) {
	var a runPlanArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	return t.o.runPlan(ctx, a)
}

type planTaskOutcome struct {
	StepID   string         `json:"step_id"`
	Title    string         `json:"title"`
	Kind     string         `json:"kind"`
	Status   string         `json:"status"`
	Report   *WorkerReport  `json:"report,omitempty"`
	Review   *Review        `json:"review,omitempty"`
	Usage    ReportUsage    `json:"usage"`
	Checks   []checkOutcome `json:"checks,omitempty"`
	Merged   bool           `json:"merged,omitempty"`
	Worktree string         `json:"worktree,omitempty"`
	Warning  string         `json:"warning,omitempty"`
	Error    string         `json:"error,omitempty"`
	merged   bool
}

type planWave struct {
	Index int               `json:"index"`
	Tasks []planTaskOutcome `json:"tasks"`
}

type planRunResult struct {
	Waves   []planWave  `json:"waves"`
	Usage   ReportUsage `json:"usage"`
	Ledger  LedgerView  `json:"ledger"`
	Warning string      `json:"warning,omitempty"`
}

func (o *Orchestrator) runPlan(ctx context.Context, a runPlanArgs) (string, error) {
	if o.cfg.FullWorkers && o.worktrees != nil {
		o.ensureWriterBase()
	}
	merged := false
	if o.worktrees != nil {
		defer func() {
			if merged {
				_ = o.worktrees.Unstage()
			}
		}()
	}
	o.mu.Lock()
	if o.ledger == nil {
		o.mu.Unlock()
		return "", fmt.Errorf("no ledger: call update_plan first")
	}
	cfg := o.cfg
	ledger := o.ledger
	o.mu.Unlock()

	parallel := a.MaxParallel
	if parallel <= 0 {
		parallel = 4
	}
	if parallel > maxFleetTasks {
		parallel = maxFleetTasks
	}

	targets, err := selectPlanSteps(ledger, a.Steps)
	if err != nil {
		return "", err
	}
	if len(targets) == 0 {
		return "", fmt.Errorf("no runnable steps: %s; pass step ids to retry failed or blocked steps", stepStatusSummary(ledger))
	}

	// An explicitly requested failed or blocked step is a retry.
	for _, id := range targets {
		if step := ledger.Find(id); step != nil && (step.Status == StepFailed || step.Status == StepBlocked) {
			step.Status = StepPending
			step.Report = nil
			step.Review = nil
		}
	}

	result := planRunResult{}
	executed := map[string]bool{}
	for waveIndex := 1; ; waveIndex++ {
		o.mu.Lock()
		ready := readySteps(ledger, targets, executed)
		goal := ledger.Goal
		facts := append([]string(nil), ledger.Facts...)
		o.mu.Unlock()
		if len(ready) == 0 {
			break
		}
		if len(ready) > parallel {
			ready = ready[:parallel]
		}

		var scouts, writers []*Step
		var validReady []*Step
		var invalid []planTaskOutcome
		for _, step := range ready {
			if normalizeStepKind(step.Kind) == StepWrite && len(step.Files) == 0 {
				executed[step.ID] = true
				o.mu.Lock()
				step.Status = StepFailed
				step.Report = &WorkerReport{Status: "failed", Summary: "write step has no owned files"}
				o.mu.Unlock()
				invalid = append(invalid, planTaskOutcome{StepID: step.ID, Title: step.Title, Kind: StepWrite, Status: "failed", Error: "write step has no owned files", Report: &WorkerReport{Status: "failed", Summary: "write step has no owned files"}})
				continue
			}
			if normalizeStepKind(step.Kind) == StepWrite && cfg.RequireVerification && len(step.Checks) == 0 {
				executed[step.ID] = true
				o.mu.Lock()
				step.Status = StepFailed
				step.Report = &WorkerReport{Status: "failed", Summary: "write step has no checks"}
				o.mu.Unlock()
				invalid = append(invalid, planTaskOutcome{StepID: step.ID, Title: step.Title, Kind: StepWrite, Status: "failed", Error: "write step has no checks", Report: &WorkerReport{Status: "failed", Summary: "write step has no checks"}})
				continue
			}
			validReady = append(validReady, step)
			if normalizeStepKind(step.Kind) == StepWrite {
				writers = append(writers, step)
			} else {
				scouts = append(scouts, step)
			}
		}
		if len(writers) > 0 {
			tasks := make([]writerTask, 0, len(writers))
			for _, step := range writers {
				tasks = append(tasks, writerTask{StepID: step.ID, Title: step.Title, Objective: stepObjective(step), Files: step.Files})
			}
			if err := validateWriters(tasks); err != nil {
				for _, step := range writers {
					executed[step.ID] = true
					o.mu.Lock()
					step.Status = StepFailed
					step.Report = &WorkerReport{Status: "failed", Summary: err.Error()}
					o.mu.Unlock()
					invalid = append(invalid, planTaskOutcome{StepID: step.ID, Title: step.Title, Kind: StepWrite, Status: "failed", Error: err.Error(), Report: &WorkerReport{Status: "failed", Summary: err.Error()}})
				}
				writers = nil
				kept := validReady[:0]
				for _, step := range validReady {
					if normalizeStepKind(step.Kind) != StepWrite {
						kept = append(kept, step)
					}
				}
				validReady = kept
			}
		}
		if len(validReady) == 0 {
			result.Waves = append(result.Waves, planWave{Index: waveIndex, Tasks: invalid})
			if a.StopOnFailure {
				break
			}
			continue
		}
		ready = validReady
		if len(writers) > 0 && o.worktrees == nil {
			msg := o.writerWarn
			if msg == "" {
				msg = "writers are not enabled"
			}
			for _, step := range writers {
				executed[step.ID] = true
				o.mu.Lock()
				step.Status = StepFailed
				step.Report = &WorkerReport{Status: "failed", Summary: msg}
				o.mu.Unlock()
				invalid = append(invalid, planTaskOutcome{StepID: step.ID, Title: step.Title, Kind: StepWrite, Status: "failed", Error: msg, Report: &WorkerReport{Status: "failed", Summary: msg}})
			}
			writers = nil
			kept := validReady[:0]
			for _, step := range validReady {
				if normalizeStepKind(step.Kind) != StepWrite {
					kept = append(kept, step)
				}
			}
			validReady = kept
			ready = validReady
			if len(validReady) == 0 {
				result.Waves = append(result.Waves, planWave{Index: waveIndex, Tasks: invalid})
				if a.StopOnFailure {
					break
				}
				continue
			}
		}

		scoutTokens, err := o.reserveScoutBudget(len(scouts))
		writerTokens, werr := o.reserveWriterBudget(len(writers))
		if err != nil || werr != nil {
			msg := firstError(err, werr)
			o.mu.Lock()
			for _, step := range ready {
				executed[step.ID] = true
				step.Status = StepBlocked
				step.Report = &WorkerReport{Status: "blocked", Summary: msg}
			}
			o.mu.Unlock()
			result.Warning = msg
			result.Waves = append(result.Waves, planWave{Index: waveIndex})
			break
		}

		outcomes := o.runPlanWave(ctx, ready, scouts, writers, goal, facts, scoutTokens, writerTokens, cfg)
		outcomes = append(outcomes, invalid...)
		result.Waves = append(result.Waves, planWave{Index: waveIndex, Tasks: outcomes})
		failed := false
		o.mu.Lock()
		for _, out := range outcomes {
			if out.merged {
				merged = true
			}
			executed[out.StepID] = true
			result.Usage.Calls += out.Usage.Calls
			result.Usage.In += out.Usage.In
			result.Usage.Cached += out.Usage.Cached
			result.Usage.Out += out.Usage.Out
			step := ledger.Find(out.StepID)
			if step == nil {
				continue
			}
			step.Report = out.Report
			if out.Review != nil {
				step.Review = out.Review
			}
			if len(out.Checks) > 0 && (out.Kind == StepWrite || out.Kind == StepScout) {
				step.Checks = nil
				for _, c := range out.Checks {
					step.Checks = append(step.Checks, Check{ID: c.ID, Command: c.Command, Status: c.Status, Output: truncateCheckOutput(c.Output), ExitCode: c.ExitCode, RanAt: time.Now()})
				}
			}
			switch {
			case out.Status == "completed":
				step.Status = StepCompleted
			case out.Status == "failed" || out.Error != "":
				step.Status = StepFailed
				failed = true
			case out.Status == "blocked":
				step.Status = StepBlocked
			default:
				step.Status = StepPending
			}
			step.Updated = time.Now()
		}
		ledger.Updated = time.Now()
		saveErr := ledger.Save(o.ledgerPath)
		result.Ledger = ledger.View()
		o.mu.Unlock()
		if saveErr != nil {
			result.Warning = "could not persist ledger: " + saveErr.Error()
		}
		if failed && a.StopOnFailure {
			break
		}
	}

	// Steps still pending because a dependency failed are blocked, not left
	// silently queued forever.
	o.mu.Lock()
	for _, id := range targets {
		step := ledger.Find(id)
		if step == nil || executed[id] || step.Status == StepCompleted {
			continue
		}
		if step.Status == StepPending {
			blocked := false
			for _, dep := range step.DependsOn {
				d := ledger.Find(dep)
				if d != nil && (d.Status == StepFailed || d.Status == StepBlocked) {
					blocked = true
					break
				}
			}
			if blocked {
				step.Status = StepBlocked
				if step.Report == nil {
					step.Report = &WorkerReport{Status: "blocked", Summary: "dependency did not complete"}
				}
			}
		}
	}
	ledger.Updated = time.Now()
	if err := ledger.Save(o.ledgerPath); err != nil && result.Warning == "" {
		result.Warning = "could not persist ledger: " + err.Error()
	}
	result.Ledger = ledger.View()
	o.mu.Unlock()

	return jsonMarshalIndent(result)
}

func stepStatusSummary(l *Ledger) string {
	counts := map[StepStatus]int{}
	for _, step := range l.Steps {
		counts[step.Status]++
	}
	if len(counts) == 0 {
		return "the ledger has no steps"
	}
	order := []StepStatus{StepPending, StepInProgress, StepCompleted, StepBlocked, StepFailed}
	parts := make([]string, 0, len(order))
	for _, status := range order {
		if counts[status] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[status], status))
		}
	}
	return strings.Join(parts, ", ")
}

func selectPlanSteps(l *Ledger, ids []string) ([]string, error) {
	if len(ids) == 0 {
		var out []string
		for _, s := range l.Steps {
			if s.Status == StepPending || s.Status == StepInProgress {
				out = append(out, s.ID)
			}
		}
		return out, nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if l.Find(id) == nil {
			return nil, fmt.Errorf("unknown step %q", id)
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func readySteps(l *Ledger, targets []string, executed map[string]bool) []*Step {
	target := make(map[string]bool, len(targets))
	for _, id := range targets {
		target[id] = true
	}
	var ready []*Step
	for _, id := range targets {
		step := l.Find(id)
		if step == nil || executed[id] || step.Status == StepCompleted {
			continue
		}
		blocked := false
		for _, dep := range step.DependsOn {
			d := l.Find(dep)
			if d == nil || d.Status != StepCompleted {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		ready = append(ready, step)
	}
	sort.SliceStable(ready, func(i, j int) bool { return ready[i].ID < ready[j].ID })
	return ready
}

func (o *Orchestrator) reserveScoutBudget(n int) (int, error) {
	if n == 0 {
		return 0, nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	cfg := o.cfg
	if cfg.MaxDelegations > 0 {
		if left := cfg.MaxDelegations - o.delegations; n > left {
			return 0, fmt.Errorf("delegation budget exhausted: need %d, only %d remain this turn", n, left)
		}
	}
	per := 0
	if cfg.TurnWorkerTokenBudget > 0 {
		remaining := cfg.TurnWorkerTokenBudget - o.workerTokens
		if remaining <= 0 {
			return 0, fmt.Errorf("worker token budget exhausted for this turn (%d tokens)", cfg.TurnWorkerTokenBudget)
		}
		per = remaining / n
		if cfg.WorkerTokenBudget > 0 && per > cfg.WorkerTokenBudget {
			per = cfg.WorkerTokenBudget
		}
		if per < minWorkerTokenBudget {
			return 0, fmt.Errorf("not enough worker token budget for %d scouts: %d tokens each (min %d)", n, per, minWorkerTokenBudget)
		}
	}
	o.delegations += n
	o.totalDelegations += n
	return per, nil
}

func (o *Orchestrator) reserveWriterBudget(n int) (int, error) {
	if n == 0 {
		return 0, nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	cfg := o.cfg
	if left := cfg.MaxParallelWriters - o.writers; n > left {
		return 0, fmt.Errorf("writer budget exhausted: need %d, only %d remain this turn", n, left)
	}
	per := 0
	if cfg.TurnWriterTokenBudget > 0 {
		remaining := cfg.TurnWriterTokenBudget - o.writerTokens
		if remaining <= 0 {
			return 0, fmt.Errorf("writer token budget exhausted for this turn (%d tokens)", cfg.TurnWriterTokenBudget)
		}
		per = remaining / n
		if cfg.WriterTokenBudget > 0 && per > cfg.WriterTokenBudget {
			per = cfg.WriterTokenBudget
		}
		if per < minWorkerTokenBudget {
			return 0, fmt.Errorf("not enough writer token budget for %d writers: %d each (min %d)", n, per, minWorkerTokenBudget)
		}
	}
	o.writers += n
	o.totalDelegations += n
	return per, nil
}

func (o *Orchestrator) runPlanWave(ctx context.Context, ready, scouts, writers []*Step, goal string, facts []string, scoutTokens, writerTokens int, cfg Config) []planTaskOutcome {
	outcomes := make([]planTaskOutcome, len(ready))
	index := make(map[string]int, len(ready))
	byID := make(map[string]*Step, len(ready))
	for i, step := range ready {
		index[step.ID] = i
		byID[step.ID] = step
		outcomes[i] = planTaskOutcome{StepID: step.ID, Title: step.Title, Kind: normalizeStepKind(step.Kind)}
	}

	var wg sync.WaitGroup
	scoutSem := make(chan struct{}, maxInt(1, cfg.MaxParallelScouts))
	for _, step := range scouts {
		wg.Add(1)
		go func(step *Step) {
			defer wg.Done()
			select {
			case scoutSem <- struct{}{}:
				defer func() { <-scoutSem }()
			case <-ctx.Done():
				i := index[step.ID]
				outcomes[i].Status = "failed"
				outcomes[i].Error = ctx.Err().Error()
				return
			}
			args := delegateArgs{
				StepID:      step.ID,
				Title:       step.Title,
				Objective:   stepObjective(step),
				Role:        step.Role,
				Deliverable: step.Deliverable,
				Files:       append([]string(nil), step.Files...),
			}
			var report *WorkerReport
			var usage ReportUsage
			var merged bool
			var err error
			if o.cfg.FullWorkers && o.worktrees != nil {
				report, usage, _, merged, err = o.runFullWorker(ctx, args, step, goal, facts, cfg.WorkerMaxSteps, scoutTokens)
			} else {
				report, usage, _, err = o.runWorker(ctx, args, step, goal, facts, cfg.WorkerMaxSteps, scoutTokens, o.cfg.FullWorkers)
			}
			i := index[step.ID]
			outcomes[i].Usage = usage
			outcomes[i].Report = report
			outcomes[i].merged = merged
			outcomes[i].Status = report.Status
			if err != nil {
				outcomes[i].Status = "failed"
				outcomes[i].Error = err.Error()
			}
			o.lead.AddUsageStats(agent.UsageStats{Calls: usage.Calls, In: usage.In, Cached: usage.Cached, Out: usage.Out})
		}(step)
	}

	writerSem := make(chan struct{}, maxInt(1, cfg.MaxParallelWriters))
	runs := make([]writerRun, len(writers))
	var packFiles []string
	var packObjectives []string
	for _, step := range writers {
		packFiles = append(packFiles, step.Files...)
		packObjectives = append(packObjectives, stepObjective(step))
	}
	contextPack := ""
	if len(writers) > 0 {
		contextPack = buildContextPack(o.worktrees.Repo, packFiles, strings.Join(packObjectives, " "))
	}
	for i, step := range writers {
		wtask := writerTask{
			StepID:      step.ID,
			Title:       step.Title,
			Objective:   stepObjective(step),
			Role:        step.Role,
			Deliverable: step.Deliverable,
			Files:       append([]string(nil), step.Files...),
		}
		copyStep := *step
		runs[i] = writerRun{
			task:     wtask,
			step:     &copyStep,
			maxSteps: cfg.WriterMaxSteps,
			tokens:   writerTokens,
			context:  contextPack,
			outcome:  writerOutcome{StepID: step.ID, Title: step.Title},
		}
		wg.Add(1)
		go func(r *writerRun) {
			defer wg.Done()
			select {
			case writerSem <- struct{}{}:
				defer func() { <-writerSem }()
			case <-ctx.Done():
				r.outcome.Status = "failed"
				r.outcome.Error = ctx.Err().Error()
				return
			}
			o.runWriter(ctx, r, goal, facts)
		}(&runs[i])
	}
	wg.Wait()

	// Scout steps can declare read-only checks too. Run them sequentially in
	// the lead workspace and gate completion on the result.
	for i := range outcomes {
		if outcomes[i].Kind != StepScout || outcomes[i].Status != "completed" {
			continue
		}
		step := byID[outcomes[i].StepID]
		if step == nil || len(step.Checks) == 0 {
			continue
		}
		checks := runCheckCommands(ctx, o.lead.Env, step.Checks, 0)
		outcomes[i].Checks = checks
		for _, check := range checks {
			if check.Status != CheckPassed {
				outcomes[i].Status = "failed"
				outcomes[i].Error = "check failed: " + check.Command
				break
			}
		}
	}

	var mergeResult writeBatchResult
	for i := range runs {
		r := &runs[i]
		out := &outcomes[index[r.task.StepID]]
		out.Usage = r.outcome.Usage
		out.Report = r.outcome.Report
		out.Review = r.outcome.Review
		out.Worktree = r.outcome.Worktree
		out.Status = r.outcome.Status
		out.Error = r.outcome.Error
		out.Warning = r.outcome.Warning
		out.Checks = r.outcome.Checks
		if r.outcome.Status == "completed" && r.patch != "" {
			out.merged = true
			o.mergeMu.Lock()
			err := o.mergeWriter(ctx, r, &mergeResult)
			o.mergeMu.Unlock()
			out.Merged = err == nil
			out.Checks = r.outcome.Checks
			if err != nil {
				out.Status = "failed"
				out.Error = combineWarnings(err, r.outcome.Warning)
			}
		}
		if r.worktree != nil && !o.cfg.KeepWorktrees && out.Merged {
			_ = r.worktree.Remove()
		}
		out.Worktree = r.outcome.Worktree
		o.lead.AddUsageStats(agent.UsageStats{Calls: out.Usage.Calls, In: out.Usage.In, Cached: out.Usage.Cached, Out: out.Usage.Out})
	}
	o.mu.Lock()
	for i := range outcomes {
		if outcomes[i].Kind == StepWrite {
			o.writerTokens += outcomes[i].Usage.In + outcomes[i].Usage.Out
		} else {
			o.workerTokens += outcomes[i].Usage.In + outcomes[i].Usage.Out
		}
	}
	o.mu.Unlock()
	return outcomes
}

func stepObjective(step *Step) string {
	if strings.TrimSpace(step.Objective) != "" {
		return step.Objective
	}
	return step.Title
}

func firstError(errs ...error) string {
	for _, err := range errs {
		if err != nil {
			return err.Error()
		}
	}
	return ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

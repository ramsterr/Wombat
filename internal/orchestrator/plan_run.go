package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/tools"
)

type runPlanTool struct{ o *Orchestrator }

type runPlanArgs struct {
	Steps         []string `json:"steps"`
	MaxParallel   int      `json:"max_parallel"`
	StopOnFailure bool     `json:"stop_on_failure"`
	Background    bool     `json:"background"`
	Cancel        bool     `json:"cancel"`
}

func (t *runPlanTool) Name() string  { return "run_plan" }
func (t *runPlanTool) Class() string { return "delegate" }
func (t *runPlanTool) Description() string {
	return "Execute the task ledger as a dependency graph. Ready steps run in parallel: scouts research, write steps run in isolated worktrees with owned files, an independent reviewer checks each patch, and passing patches merge automatically. Scheduling is continuous, so a slow step does not hold back independent or newly-ready work; timed-out and transient failures get one retry. Failed steps block only their dependents. Use after update_plan for multi-step work instead of dispatching steps by hand. Set background=true to return immediately and keep working while it runs; call run_plan with no steps to wait for it, and cancel=true to stop it. Canceled or interrupted steps stay pending and completed steps are skipped, so a later run_plan resumes the plan."
}
func (t *runPlanTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"steps":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "optional step ids to run (default: every pending step)"},
			"max_parallel":    map[string]any{"type": "integer", "description": "maximum steps running at once (default 4, max 6)"},
			"stop_on_failure": map[string]any{"type": "boolean", "description": "stop scheduling new work after a failure (independent steps already running still finish)"},
			"background":      map[string]any{"type": "boolean", "description": "start the plan and return immediately instead of waiting for it"},
			"cancel":          map[string]any{"type": "boolean", "description": "cancel the running plan and leave unfinished steps pending"},
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
	StepID        string         `json:"step_id"`
	Title         string         `json:"title"`
	Kind          string         `json:"kind"`
	Status        string         `json:"status"`
	Report        *WorkerReport  `json:"report,omitempty"`
	Review        *Review        `json:"review,omitempty"`
	Usage         ReportUsage    `json:"usage"`
	Checks        []checkOutcome `json:"checks,omitempty"`
	Merged        bool           `json:"merged,omitempty"`
	Worktree      string         `json:"worktree,omitempty"`
	WorkerSession string         `json:"worker_session,omitempty"`
	Warning       string         `json:"warning,omitempty"`
	Error         string         `json:"error,omitempty"`
	merged        bool
}

type planWave struct {
	Index int               `json:"index"`
	Tasks []planTaskOutcome `json:"tasks"`
}

type planRunResult struct {
	Status  string      `json:"status,omitempty"`
	Running []string    `json:"running,omitempty"`
	Message string      `json:"message,omitempty"`
	Waves   []planWave  `json:"waves"`
	Usage   ReportUsage `json:"usage"`
	Ledger  LedgerView  `json:"ledger"`
	Warning string      `json:"warning,omitempty"`
}

// planSession is one live plan run. The scheduler goroutine owns the
// scheduler state; the session itself coordinates joins, cancel, and status.
type planSession struct {
	s      *planScheduler
	cancel context.CancelFunc
	done   chan struct{}
}

func (o *Orchestrator) runPlan(ctx context.Context, a runPlanArgs) (string, error) {
	if o.cfg.FullWorkers && o.worktrees != nil {
		o.ensureWriterBase()
	}
	if a.Cancel && len(a.Steps) > 0 {
		return "", fmt.Errorf("cancel cannot be combined with steps")
	}
	o.mu.Lock()
	active := o.planRun
	o.mu.Unlock()
	if active != nil {
		finished := false
		select {
		case <-active.done:
			finished = true
		default:
		}
		if finished {
			o.mu.Lock()
			if o.planRun == active {
				o.planRun = nil
			}
			o.mu.Unlock()
			if a.Cancel || a.Background || (len(a.Steps) == 0 && active.s.result.Status != "cancelled") {
				return o.planResultJSON(active)
			}
		} else {
			if len(a.Steps) > 0 {
				return "", fmt.Errorf("a plan is already running; call run_plan with no steps to wait or cancel=true to stop it")
			}
			if a.Cancel {
				active.cancel()
				<-active.done
				return o.planResultJSON(active)
			}
			if a.Background {
				return o.planStatusJSON(active)
			}
			<-active.done
			return o.planResultJSON(active)
		}
	}
	if a.Cancel {
		return jsonMarshalIndent(planRunResult{Status: "idle", Message: "no plan is running"})
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

	o.mu.Lock()
	goal := ledger.Goal
	facts := append([]string(nil), ledger.Facts...)
	o.mu.Unlock()

	runCtx := ctx
	if a.Background {
		runCtx = context.Background()
	}
	runCtx, cancel := context.WithCancel(runCtx)
	s := &planScheduler{
		o:             o,
		ctx:           runCtx,
		cfg:           cfg,
		goal:          goal,
		facts:         facts,
		targets:       targets,
		parallel:      parallel,
		stopOnFailure: a.StopOnFailure,
		executed:      make(map[string]bool),
		running:       make(map[string]bool),
		kinds:         make(map[string]string),
		writerFiles:   make(map[string]string),
		done:          make(chan planTaskResult, maxInt(1, parallel)),
		waveTasks:     make(map[int][]string),
		outcomes:      make(map[string]planTaskOutcome),
	}
	sess := &planSession{s: s, cancel: cancel, done: make(chan struct{})}
	o.mu.Lock()
	if o.planRun != nil {
		o.mu.Unlock()
		cancel()
		return "", fmt.Errorf("another plan started")
	}
	o.planRun = sess
	o.mu.Unlock()
	go func() {
		s.run()
		o.finishPlan(s)
		close(sess.done)
	}()
	if a.Background {
		return o.planStatusJSON(sess)
	}
	<-sess.done
	return o.planResultJSON(sess)
}

// finishPlan persists the terminal state. A canceled plan leaves unfinished
// steps pending so a later run_plan can resume it; only a plan that really
// finished marks unscheduled targets blocked.
func (o *Orchestrator) finishPlan(s *planScheduler) {
	canceled := s.ctx.Err() != nil
	o.mu.Lock()
	if !canceled {
		for _, id := range s.targets {
			step := o.ledger.Find(id)
			if step == nil || s.executed[id] || step.Status == StepCompleted || step.Status == StepFailed || step.Status == StepBlocked {
				continue
			}
			reason := "not scheduled"
			for _, dep := range step.DependsOn {
				d := o.ledger.Find(dep)
				if d == nil || d.Status != StepCompleted {
					reason = "dependency did not complete"
					break
				}
			}
			step.Status = StepBlocked
			if step.Report == nil {
				step.Report = &WorkerReport{Status: "blocked", Summary: reason}
			}
		}
	}
	switch {
	case canceled:
		s.result.Status = "cancelled"
		s.result.Message = "plan canceled; unfinished steps stay pending. Call run_plan again to resume."
	case s.failed:
		s.result.Status = "failed"
	default:
		s.result.Status = "completed"
	}
	o.ledger.Updated = time.Now()
	saveErr := o.ledger.Save(o.ledgerPath)
	s.result.Ledger = o.ledger.View()
	if saveErr != nil && s.result.Warning == "" {
		s.result.Warning = "could not persist ledger: " + saveErr.Error()
	}
	o.mu.Unlock()
	if s.merged && o.worktrees != nil {
		_ = o.worktrees.Unstage()
	}
	if canceled {
		o.planProgress("plan canceled; unfinished steps are pending")
	} else {
		o.planProgress("plan finished (%s)", s.result.Status)
	}
}

func (o *Orchestrator) planResultJSON(sess *planSession) (string, error) {
	o.mu.Lock()
	result := sess.s.result
	o.mu.Unlock()
	return jsonMarshalIndent(result)
}

func (o *Orchestrator) planStatusJSON(sess *planSession) (string, error) {
	select {
	case <-sess.done:
		return o.planResultJSON(sess)
	default:
	}
	o.mu.Lock()
	running := make([]string, 0, len(o.planActive))
	for id := range o.planActive {
		running = append(running, id)
	}
	sort.Strings(running)
	result := planRunResult{
		Status:  "running",
		Running: running,
		Usage:   sess.s.result.Usage,
		Ledger:  o.ledger.View(),
		Message: "plan is running; call run_plan with no steps to wait, or cancel=true to stop it",
	}
	o.mu.Unlock()
	return jsonMarshalIndent(result)
}

func (o *Orchestrator) planProgress(format string, args ...any) {
	o.hooksMu.Lock()
	defer o.hooksMu.Unlock()
	if o.lead.Hooks.OnTool != nil {
		o.lead.Hooks.OnTool("plan", fmt.Sprintf(format, args...))
	}
}

// planScheduler runs the dependency graph continuously. A slow step keeps
// its own slot but no longer holds a whole wave open: as soon as one step
// finishes, any newly-ready step can start in the free slot.
type planScheduler struct {
	o             *Orchestrator
	ctx           context.Context
	cfg           Config
	goal          string
	facts         []string
	targets       []string
	parallel      int
	stopOnFailure bool

	executed    map[string]bool
	running     map[string]bool
	kinds       map[string]string
	writerFiles map[string]string
	done        chan planTaskResult

	waveSeq   int
	waveOrder []int
	waveTasks map[int][]string
	outcomes  map[string]planTaskOutcome
	result    planRunResult
	failed    bool
	merged    bool
}

type planTaskRun struct {
	step         *Step
	kind         string
	scoutTokens  int
	writerTokens int
	contextPack  string
}

type planTaskResult struct {
	outcome planTaskOutcome
}

const (
	planStepAttempts = 2
	planRetryDelay   = 250 * time.Millisecond
)

func (s *planScheduler) run() {
	defer s.finishWaves()
	for {
		if s.ctx.Err() == nil && !(s.failed && s.stopOnFailure) {
			s.launchReady()
		}
		if len(s.running) == 0 {
			return
		}
		res := <-s.done
		delete(s.running, res.outcome.StepID)
		s.apply(res.outcome)
	}
}

func (s *planScheduler) launchReady() {
	s.o.mu.Lock()
	ready := readySteps(s.o.ledger, s.targets, s.executed)
	s.o.mu.Unlock()

	var candidates []*Step
	for _, step := range ready {
		if !s.running[step.ID] {
			candidates = append(candidates, step)
		}
	}
	slots := s.parallel - len(s.running)
	if slots <= 0 || len(candidates) == 0 {
		return
	}
	candidates = s.pickByKind(candidates, slots)
	if len(candidates) == 0 {
		return
	}
	wave := s.nextWave()

	var scouts, writers []*Step
	for _, step := range candidates {
		kind := normalizeStepKind(step.Kind)
		if kind != StepWrite {
			scouts = append(scouts, step)
			continue
		}
		switch {
		case len(step.Files) == 0:
			s.fail(step, "write step has no owned files", wave)
		case s.cfg.RequireVerification && len(step.Checks) == 0:
			s.fail(step, "write step has no checks", wave)
		case s.o.worktrees == nil:
			msg := s.o.writerWarn
			if msg == "" {
				msg = "writers are not enabled"
			}
			s.fail(step, msg, wave)
		case s.writerConflict(step):
			s.fail(step, "file ownership overlaps a running writer", wave)
		default:
			writers = append(writers, step)
		}
	}

	if len(writers) > 0 {
		tasks := make([]writerTask, 0, len(writers))
		for _, step := range writers {
			tasks = append(tasks, writerTask{StepID: step.ID, Title: step.Title, Objective: stepObjective(step), Files: step.Files})
		}
		if err := validateWriters(tasks); err != nil {
			for _, step := range writers {
				s.fail(step, err.Error(), wave)
			}
			writers = nil
		}
	}

	scoutTokens := 0
	if len(scouts) > 0 {
		tokens, err := s.o.reserveScoutBudget(len(scouts))
		if err != nil {
			for _, step := range scouts {
				s.block(step, err.Error(), wave)
			}
			scouts = nil
		} else {
			scoutTokens = tokens
		}
	}
	writerTokens := 0
	if len(writers) > 0 {
		tokens, err := s.o.reserveWriterBudget(len(writers))
		if err != nil {
			for _, step := range writers {
				s.block(step, err.Error(), wave)
			}
			writers = nil
		} else {
			writerTokens = tokens
		}
	}

	contextPack := ""
	if len(writers) > 0 {
		var files, objectives []string
		for _, step := range writers {
			files = append(files, step.Files...)
			objectives = append(objectives, stepObjective(step))
		}
		contextPack = buildContextPack(s.o.worktrees.Repo, files, strings.Join(objectives, " "))
	}
	for _, step := range scouts {
		s.start(step, StepScout, scoutTokens, 0, "", wave)
	}
	for _, step := range writers {
		s.start(step, StepWrite, 0, writerTokens, contextPack, wave)
	}
}

// pickByKind prevents one kind of work from monopolising every slot. Ready
// steps with the longest chain of dependents go first, so the critical path
// is never stuck behind cheap independent work. Writers win ties and both
// kinds stay under their configured concurrency limits.
func (s *planScheduler) pickByKind(ready []*Step, slots int) []*Step {
	activeScouts, activeWriters := 0, 0
	for id := range s.running {
		switch s.kinds[id] {
		case StepWrite:
			activeWriters++
		default:
			activeScouts++
		}
	}
	writerRoom := s.o.freeWriterSlots()
	scoutRoom := s.cfg.MaxParallelScouts - activeScouts
	if writerRoom < 0 {
		writerRoom = 0
	}
	if scoutRoom < 0 {
		scoutRoom = 0
	}
	// When both kinds are ready, do not let writers consume every slot.
	// At least one scout can make progress alongside a slow writer batch.
	if len(ready) > 0 && writerRoom >= slots && slots > 1 {
		hasScout := false
		for _, step := range ready {
			if normalizeStepKind(step.Kind) != StepWrite {
				hasScout = true
				break
			}
		}
		if hasScout && writerRoom > 0 {
			writerRoom = slots - 1
		}
	}
	deps := s.targetDeps()
	memo := make(map[string]int, len(ready))
	sort.SliceStable(ready, func(i, j int) bool {
		di := s.chainDepth(ready[i].ID, deps, memo, map[string]bool{})
		dj := s.chainDepth(ready[j].ID, deps, memo, map[string]bool{})
		if di != dj {
			return di > dj
		}
		wi := normalizeStepKind(ready[i].Kind) == StepWrite
		wj := normalizeStepKind(ready[j].Kind) == StepWrite
		if wi != wj {
			return wi
		}
		return ready[i].ID < ready[j].ID
	})
	selected := make([]*Step, 0, slots)
	writers, scouts := 0, 0
	for _, step := range ready {
		if len(selected) >= slots {
			break
		}
		if normalizeStepKind(step.Kind) == StepWrite {
			if writers < writerRoom {
				selected = append(selected, step)
				writers++
			}
			continue
		}
		if scouts < scoutRoom {
			selected = append(selected, step)
			scouts++
		}
	}
	return selected
}

// targetDeps snapshots the dependency edges between the plan's target steps.
func (s *planScheduler) targetDeps() map[string][]string {
	deps := make(map[string][]string, len(s.targets))
	s.o.mu.Lock()
	for _, id := range s.targets {
		if step := s.o.ledger.Find(id); step != nil {
			deps[id] = append([]string(nil), step.DependsOn...)
		}
	}
	s.o.mu.Unlock()
	return deps
}

// chainDepth returns how many not-yet-scheduled dependents sit below id. The
// longest chain wins, which is the standard critical-path heuristic.
func (s *planScheduler) chainDepth(id string, deps map[string][]string, memo map[string]int, visiting map[string]bool) int {
	if d, ok := memo[id]; ok {
		return d
	}
	if visiting[id] {
		return 0
	}
	visiting[id] = true
	best := 0
	for target, targetDeps := range deps {
		for _, dep := range targetDeps {
			if dep != id {
				continue
			}
			if d := 1 + s.chainDepth(target, deps, memo, visiting); d > best {
				best = d
			}
			break
		}
	}
	delete(visiting, id)
	memo[id] = best
	return best
}

func (s *planScheduler) nextWave() int {
	s.waveSeq++
	s.waveOrder = append(s.waveOrder, s.waveSeq)
	return s.waveSeq
}

func (s *planScheduler) start(step *Step, kind string, scoutTokens, writerTokens int, contextPack string, wave int) {
	run := planTaskRun{step: step, kind: kind, scoutTokens: scoutTokens, writerTokens: writerTokens, contextPack: contextPack}
	s.running[step.ID] = true
	s.kinds[step.ID] = kind
	s.waveTasks[wave] = append(s.waveTasks[wave], step.ID)
	s.o.mu.Lock()
	if current := s.o.ledger.Find(step.ID); current != nil {
		current.Status = StepInProgress
	}
	if s.o.planActive == nil {
		s.o.planActive = make(map[string]bool)
	}
	s.o.planActive[step.ID] = true
	s.o.mu.Unlock()
	s.o.planProgress("started %s (%s)", step.ID, kind)
	if kind == StepWrite {
		for _, file := range step.Files {
			s.writerFiles[file] = step.ID
		}
	}
	go func() {
		out := s.executeWithRetry(run)
		s.done <- planTaskResult{outcome: out}
	}()
}

func (s *planScheduler) writerConflict(step *Step) bool {
	for _, file := range step.Files {
		if owner, ok := s.writerFiles[file]; ok && owner != step.ID {
			return true
		}
	}
	return false
}

func (s *planScheduler) executeWithRetry(run planTaskRun) planTaskOutcome {
	out := s.executePlanTask(s.ctx, run, s.goal, s.facts, s.cfg)
	// A full-access scout without a worktree edits the lead workspace. Never
	// replay it after a timeout: a retry could apply the same side effects
	// twice. Writers and worktree-backed workers are isolated and safe.
	canRetry := run.kind == StepWrite || !s.cfg.FullWorkers || s.o.worktrees != nil
	if !canRetry {
		return out
	}
	for attempt := 2; attempt <= planStepAttempts; attempt++ {
		if s.ctx.Err() != nil || !retryablePlanOutcome(out) {
			break
		}
		if planRetryDelay > 0 {
			select {
			case <-time.After(planRetryDelay):
			case <-s.ctx.Done():
				return out
			}
		}
		next := s.executePlanTask(s.ctx, run, s.goal, s.facts, s.cfg)
		next.Usage.Calls += out.Usage.Calls
		next.Usage.In += out.Usage.In
		next.Usage.Cached += out.Usage.Cached
		next.Usage.Out += out.Usage.Out
		if next.Error == "" {
			next.Warning = combineWarnings(fmt.Errorf("retried after: %s", oneLine(out.Error, 180)), next.Warning)
		}
		out = next
	}
	return out
}

func retryablePlanOutcome(out planTaskOutcome) bool {
	if out.Status != "failed" && out.Error == "" {
		return false
	}
	msg := strings.ToLower(out.Error)
	for _, needle := range []string{
		"timed out", "timeout", "deadline exceeded",
		"connection reset", "connection refused", "unexpected eof",
		"temporarily unavailable", "temporary failure",
		"bad gateway", "service unavailable", "gateway timeout",
		"rate limit", "too many requests", "429", "502", "503", "504",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func (s *planScheduler) executePlanTask(ctx context.Context, run planTaskRun, goal string, facts []string, cfg Config) planTaskOutcome {
	step := run.step
	out := planTaskOutcome{StepID: step.ID, Title: step.Title, Kind: run.kind}
	if run.kind == StepWrite {
		copyStep := *step
		r := writerRun{
			task: writerTask{
				StepID: step.ID, Title: step.Title, Objective: stepObjective(step),
				Role: step.Role, Deliverable: step.Deliverable, Files: append([]string(nil), step.Files...),
			},
			step:     &copyStep,
			maxSteps: cfg.WriterMaxSteps,
			tokens:   run.writerTokens,
			context:  run.contextPack,
			outcome:  writerOutcome{StepID: step.ID, Title: step.Title},
		}
		s.o.runWriter(ctx, &r, goal, facts)
		out.Usage = r.outcome.Usage
		out.Report = r.outcome.Report
		out.Review = r.outcome.Review
		out.Worktree = r.outcome.Worktree
		out.WorkerSession = r.outcome.WorkerSession
		out.Status = r.outcome.Status
		out.Error = r.outcome.Error
		out.Warning = r.outcome.Warning
		out.Checks = r.outcome.Checks
		if r.outcome.Status == "completed" && r.patch != "" {
			out.merged = true
			s.o.mergeMu.Lock()
			err := s.o.mergeWriter(ctx, &r, &writeBatchResult{})
			s.o.mergeMu.Unlock()
			out.Merged = err == nil
			out.Checks = r.outcome.Checks
			if err != nil {
				out.Status = "failed"
				out.Error = combineWarnings(err, r.outcome.Warning)
			}
		}
		if r.worktree != nil && out.Merged {
			s.o.releaseWorktree(r.worktree, cfg.KeepWorktrees)
			r.worktree = nil
		}
		if ctx.Err() != nil && out.Status != "completed" && out.Status != "needs_input" {
			out.Status = "pending"
			out.Error = ""
			out.Warning = combineWarnings(ctx.Err(), out.Warning)
			if out.Report != nil {
				out.Report.Status = "pending"
				out.Report.Summary = "interrupted; run run_plan again to resume"
			}
			if r.worktree != nil {
				s.o.releaseWorktree(r.worktree, cfg.KeepWorktrees)
				r.worktree = nil
			}
		}
		out.Worktree = r.outcome.Worktree
		return out
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
	if cfg.FullWorkers && s.o.worktrees != nil {
		report, usage, _, merged, err = s.o.runFullWorker(ctx, args, step, goal, facts, cfg.WorkerMaxSteps, run.scoutTokens)
	} else {
		report, usage, _, err = s.o.runWorker(ctx, args, step, goal, facts, cfg.WorkerMaxSteps, run.scoutTokens, cfg.FullWorkers)
	}
	out.Usage = usage
	out.Report = report
	out.merged = merged
	if report != nil {
		out.Status = report.Status
	}
	if err != nil {
		out.Status = "failed"
		out.Error = err.Error()
	}
	if out.Status == "completed" && len(step.Checks) > 0 {
		checks := runCheckCommands(ctx, s.o.lead.Env, step.Checks, 0)
		out.Checks = checks
		for _, check := range checks {
			if check.Status != CheckPassed {
				out.Status = "failed"
				out.Error = "check failed: " + check.Command
				break
			}
		}
	}
	if ctx.Err() != nil && out.Status != "completed" && out.Status != "needs_input" {
		out.Status = "pending"
		out.Error = ""
		out.Warning = combineWarnings(ctx.Err(), out.Warning)
		if out.Report != nil {
			out.Report.Status = "pending"
			out.Report.Summary = "interrupted; run run_plan again to resume"
		}
	}
	return out
}

func (s *planScheduler) fail(step *Step, message string, wave int) {
	out := planTaskOutcome{
		StepID: step.ID, Title: step.Title, Kind: normalizeStepKind(step.Kind),
		Status: "failed", Error: message,
		Report: &WorkerReport{Status: "failed", Summary: message},
	}
	s.record(out, wave)
	s.o.planProgress("failed %s: %s", step.ID, message)
}

func (s *planScheduler) block(step *Step, message string, wave int) {
	out := planTaskOutcome{
		StepID: step.ID, Title: step.Title, Kind: normalizeStepKind(step.Kind),
		Status: "blocked", Error: message,
		Report: &WorkerReport{Status: "blocked", Summary: message},
	}
	s.record(out, wave)
	s.o.planProgress("blocked %s: %s", step.ID, message)
}

func (s *planScheduler) record(out planTaskOutcome, wave int) {
	s.executed[out.StepID] = true
	s.waveTasks[wave] = append(s.waveTasks[wave], out.StepID)
	s.outcomes[out.StepID] = out
	s.o.mu.Lock()
	if step := s.o.ledger.Find(out.StepID); step != nil {
		step.Status = StepFailed
		if out.Status == "blocked" {
			step.Status = StepBlocked
		}
		step.Report = out.Report
		step.Updated = time.Now()
	}
	delete(s.o.planActive, out.StepID)
	s.o.mu.Unlock()
	if out.Status == "failed" {
		s.failed = true
	}
	if out.Error != "" && s.result.Warning == "" {
		s.result.Warning = out.Error
	}
}

func (s *planScheduler) apply(out planTaskOutcome) {
	if out.Kind == StepWrite {
		for file, owner := range s.writerFiles {
			if owner == out.StepID {
				delete(s.writerFiles, file)
			}
		}
	}
	s.outcomes[out.StepID] = out
	s.executed[out.StepID] = true
	if out.merged {
		s.merged = true
	}
	s.o.mu.Lock()
	step := s.o.ledger.Find(out.StepID)
	delete(s.o.planActive, out.StepID)
	s.result.Usage.Calls += out.Usage.Calls
	s.result.Usage.In += out.Usage.In
	s.result.Usage.Cached += out.Usage.Cached
	s.result.Usage.Out += out.Usage.Out
	if step != nil {
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
		case out.Status == "blocked":
			step.Status = StepBlocked
		default:
			step.Status = StepPending
		}
		step.Updated = time.Now()
	}
	if out.Kind == StepWrite {
		s.o.writerTokens += out.Usage.In + out.Usage.Out
	} else {
		s.o.workerTokens += out.Usage.In + out.Usage.Out
	}
	s.o.mu.Unlock()
	if out.Kind == StepWrite {
		s.o.releaseWriterSlots(1)
	}
	s.o.mu.Lock()
	s.o.ledger.Updated = time.Now()
	saveErr := s.o.ledger.Save(s.o.ledgerPath)
	s.result.Ledger = s.o.ledger.View()
	s.o.mu.Unlock()
	if out.Status == "failed" || out.Error != "" {
		s.failed = true
	}
	if step != nil {
		s.o.planProgress("finished %s (%s)", out.StepID, step.Status)
	}
	if saveErr != nil && s.result.Warning == "" {
		s.result.Warning = "could not persist ledger: " + saveErr.Error()
	}
	s.o.lead.AddUsageStats(agent.UsageStats{Calls: out.Usage.Calls, In: out.Usage.In, Cached: out.Usage.Cached, Out: out.Usage.Out})
}

func (s *planScheduler) finishWaves() {
	for _, index := range s.waveOrder {
		wave := planWave{Index: index}
		for _, id := range s.waveTasks[index] {
			if out, ok := s.outcomes[id]; ok {
				wave.Tasks = append(wave.Tasks, out)
			}
		}
		s.result.Waves = append(s.result.Waves, wave)
	}
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
	if left := cfg.MaxParallelWriters - o.activeWriters; n > left {
		return 0, fmt.Errorf("writer slots busy: need %d, only %d free (running writers: %d)", n, left, o.activeWriters)
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
	o.activeWriters += n
	o.totalDelegations += n
	return per, nil
}

// releaseWriterSlots frees slots reserved by reserveWriterBudget or taken by
// delegate_write. It is safe to call from a plan worker goroutine.
func (o *Orchestrator) releaseWriterSlots(n int) {
	if n <= 0 {
		return
	}
	o.mu.Lock()
	o.activeWriters -= n
	if o.activeWriters < 0 {
		o.activeWriters = 0
	}
	o.mu.Unlock()
}

// addActiveWriter accounts for a writer that gates itself with its own
// semaphore (delegate_write) rather than reserving through the scheduler.
func (o *Orchestrator) addActiveWriter() {
	o.mu.Lock()
	o.activeWriters++
	o.mu.Unlock()
}

// freeWriterSlots reports how many writer slots are currently available.
func (o *Orchestrator) freeWriterSlots() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	left := o.cfg.MaxParallelWriters - o.activeWriters
	if left < 0 {
		return 0
	}
	return left
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

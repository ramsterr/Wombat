package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
	"github.com/ramsterr/Wombat/internal/worktree"
)

type delegateWriteTool struct{ o *Orchestrator }

type writerTask struct {
	StepID      string   `json:"step_id"`
	Title       string   `json:"title"`
	Objective   string   `json:"objective"`
	Context     string   `json:"context"`
	Role        string   `json:"role"`
	Deliverable string   `json:"deliverable"`
	Files       []string `json:"files"`
	MaxSteps    int      `json:"max_steps"`
}

type delegateWriteArgs struct {
	Tasks []writerTask `json:"tasks"`
}

func (t *delegateWriteTool) Name() string  { return "delegate_write" }
func (t *delegateWriteTool) Class() string { return "write" }
func (t *delegateWriteTool) Description() string {
	return "Run one to six coding tasks in isolated git worktrees, at most max_parallel_writers at a time (default 2). Each writer owns an explicit, non-overlapping file list, runs the ledger checks for its step, gets an independent review, and is merged with a three-way fallback only if checks and review pass. For dependent work use run_plan write steps instead; keep dependent edits in the lead."
}
func (t *delegateWriteTool) Schema() map[string]any {
	task := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"step_id":     map[string]any{"type": "string", "description": "ledger step this writer implements (must exist and have checks unless verification is disabled)"},
			"title":       map[string]any{"type": "string", "description": "short writer title"},
			"objective":   map[string]any{"type": "string", "description": "concrete implementation outcome"},
			"context":     map[string]any{"type": "string", "description": "constraints and findings the writer cannot infer"},
			"role":        map[string]any{"type": "string", "description": "optional role, e.g. implementer"},
			"deliverable": map[string]any{"type": "string", "description": "exact output and behavior expected"},
			"files":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "every file the writer is allowed to change (repo-relative; no globs)"},
			"max_steps":   map[string]any{"type": "integer", "description": "optional writer step budget (default from config, max 100)"},
		},
		"required": []string{"step_id", "title", "objective", "files"},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tasks": map[string]any{
				"type":        "array",
				"description": "independent writers with disjoint file ownership (1-6)",
				"minItems":    1,
				"maxItems":    maxFleetTasks,
				"items":       task,
			},
		},
		"required": []string{"tasks"},
	}
}

func (t *delegateWriteTool) Run(ctx context.Context, raw json.RawMessage, _ *tools.Env) (string, error) {
	var a delegateWriteArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	return t.o.delegateWrite(ctx, a)
}

type writerOutcome struct {
	StepID        string         `json:"step_id"`
	Title         string         `json:"title"`
	Status        string         `json:"status"`
	Report        *WorkerReport  `json:"report,omitempty"`
	Usage         ReportUsage    `json:"usage"`
	ChangedFiles  []string       `json:"changed_files,omitempty"`
	Checks        []checkOutcome `json:"checks,omitempty"`
	Review        *Review        `json:"review,omitempty"`
	Merged        bool           `json:"merged"`
	Worktree      string         `json:"worktree,omitempty"`
	WorkerSession string         `json:"worker_session,omitempty"`
	Warning       string         `json:"warning,omitempty"`
	Error         string         `json:"error,omitempty"`
}

type writeBatchResult struct {
	Tasks   []writerOutcome `json:"tasks"`
	Usage   ReportUsage     `json:"usage"`
	Ledger  LedgerView      `json:"ledger"`
	Warning string          `json:"warning,omitempty"`
}

type writerRun struct {
	task           writerTask
	step           *Step
	maxSteps       int
	tokens         int
	context        string
	worktree       *worktree.Worktree
	patch          string
	outcome        writerOutcome
	tokenUsed      int
	full           bool
	allowEmpty     bool
	session        string
	ws             *session.Session
	model          string
	base           string
	worktreeChecks bool
}

func (o *Orchestrator) delegateWrite(ctx context.Context, a delegateWriteArgs) (string, error) {
	if o.worktrees == nil {
		return "", fmt.Errorf("writers are unavailable: %s", o.writerWarn)
	}
	touched := false
	defer func() {
		if touched {
			_ = o.worktrees.Unstage()
		}
	}()
	if len(a.Tasks) == 0 {
		return "", fmt.Errorf("delegate_write needs at least one task")
	}
	if len(a.Tasks) > maxFleetTasks {
		return "", fmt.Errorf("too many writers: %d (max %d)", len(a.Tasks), maxFleetTasks)
	}
	if err := validateWriters(a.Tasks); err != nil {
		return "", err
	}
	o.mu.Lock()
	cfg := o.cfg
	if o.ledger == nil {
		o.mu.Unlock()
		return "", fmt.Errorf("no ledger")
	}
	runs := make([]writerRun, len(a.Tasks))
	for i, task := range a.Tasks {
		step := o.ledger.Find(task.StepID)
		if step == nil {
			o.mu.Unlock()
			return "", fmt.Errorf("task %d: unknown step id %q", i+1, task.StepID)
		}
		if cfg.RequireVerification && len(step.Checks) == 0 {
			o.mu.Unlock()
			return "", fmt.Errorf("task %d: step %q has no checks; add at least one check before using a writer", i+1, task.StepID)
		}
		for _, dep := range step.DependsOn {
			d := o.ledger.Find(dep)
			if d == nil || d.Status != StepCompleted {
				o.mu.Unlock()
				return "", fmt.Errorf("task %d: step %q depends on %q which is not completed", i+1, task.StepID, dep)
			}
		}
		maxSteps := task.MaxSteps
		if maxSteps <= 0 {
			maxSteps = cfg.WriterMaxSteps
		}
		if maxSteps > maxWorkerStepsCap {
			maxSteps = maxWorkerStepsCap
		}
		copyStep := *step
		runs[i] = writerRun{
			task:     task,
			step:     &copyStep,
			maxSteps: maxSteps,
			outcome:  writerOutcome{StepID: task.StepID, Title: task.Title},
		}
	}
	perWriter := 0
	if cfg.TurnWriterTokenBudget > 0 {
		remaining := cfg.TurnWriterTokenBudget - o.writerTokens
		if remaining <= 0 {
			o.mu.Unlock()
			return "", fmt.Errorf("writer token budget exhausted for this turn (%d tokens)", cfg.TurnWriterTokenBudget)
		}
		perWriter = remaining / len(runs)
		if cfg.WriterTokenBudget > 0 && perWriter > cfg.WriterTokenBudget {
			perWriter = cfg.WriterTokenBudget
		}
		if perWriter < minWorkerTokenBudget {
			o.mu.Unlock()
			return "", fmt.Errorf("not enough writer token budget for %d writers: %d each (min %d)", len(runs), perWriter, minWorkerTokenBudget)
		}
	}
	for i := range runs {
		runs[i].tokens = perWriter
	}
	o.totalDelegations += len(runs)
	goal := o.ledger.Goal
	facts := append([]string(nil), o.ledger.Facts...)
	o.mu.Unlock()

	var allFiles []string
	var objectives []string
	for _, r := range runs {
		allFiles = append(allFiles, r.task.Files...)
		objectives = append(objectives, r.task.Objective)
	}
	pack := buildContextPack(o.worktrees.Repo, allFiles, strings.Join(objectives, " "))
	for i := range runs {
		runs[i].context = pack
	}

	parallel := cfg.MaxParallelWriters
	if parallel < 1 {
		parallel = 1
	}
	if parallel > len(runs) {
		parallel = len(runs)
	}
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i := range runs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				runs[i].outcome.Error = ctx.Err().Error()
				return
			}
			o.addActiveWriter()
			defer o.releaseWriterSlots(1)
			o.runWriter(ctx, &runs[i], goal, facts)
		}(i)
	}
	wg.Wait()

	result := writeBatchResult{}
	for _, r := range runs {
		result.Tasks = append(result.Tasks, r.outcome)
		result.Usage.Calls += r.outcome.Usage.Calls
		result.Usage.In += r.outcome.Usage.In
		result.Usage.Cached += r.outcome.Usage.Cached
		result.Usage.Out += r.outcome.Usage.Out
	}
	o.lead.AddUsageStats(agent.UsageStats{
		Calls: result.Usage.Calls, In: result.Usage.In, Cached: result.Usage.Cached, Out: result.Usage.Out,
	})

	for i := range runs {
		r := &runs[i]
		if r.outcome.Error != "" || r.outcome.Status != "completed" || r.patch == "" {
			if r.worktree != nil && !cfg.KeepWorktrees {
				r.outcome.Worktree = r.worktree.Path
			}
			continue
		}
		touched = true
		o.mergeMu.Lock()
		err := o.mergeWriter(ctx, r, &result)
		o.mergeMu.Unlock()
		if err == nil && r.worktree != nil {
			o.releaseWorktree(r.worktree, cfg.KeepWorktrees)
			r.worktree = nil
		}
	}
	result.Tasks = result.Tasks[:0]
	for _, r := range runs {
		result.Tasks = append(result.Tasks, r.outcome)
	}

	o.mu.Lock()
	for i := range runs {
		r := &runs[i]
		if r.outcome.Merged {
			continue
		}
		step := o.ledger.Find(r.task.StepID)
		if step == nil {
			continue
		}
		step.Report = r.outcome.Report
		step.Review = r.outcome.Review
		if len(r.outcome.Checks) > 0 {
			step.Checks = nil
			for _, c := range r.outcome.Checks {
				step.Checks = append(step.Checks, Check{ID: c.ID, Command: c.Command, Status: c.Status, Output: truncateCheckOutput(c.Output), ExitCode: c.ExitCode, RanAt: time.Now()})
			}
		}
		switch r.outcome.Status {
		case "blocked":
			step.Status = StepBlocked
		case "partial", "needs_input":
			step.Status = StepPending
		default:
			step.Status = StepFailed
		}
		step.Updated = time.Now()
	}
	o.writerTokens += result.Usage.In + result.Usage.Out
	o.ledger.Updated = time.Now()
	saveErr := o.ledger.Save(o.ledgerPath)
	result.Ledger = o.ledger.View()
	o.mu.Unlock()
	if saveErr != nil {
		result.Warning = "could not persist ledger: " + saveErr.Error()
	}
	return jsonMarshalIndent(result)
}

func (o *Orchestrator) writerEnv(dir string, files []string) *tools.Env {
	env := *o.lead.Env
	env.Isolate()
	env.Dir = dir
	env.ReadOnly = false
	env.Permit = nil
	env.Checkpoint = nil
	env.DenyGit = true
	env.DenyOutside = true
	env.AllowOutside = false
	// The writer works in a worktree, but a shell command with an absolute
	// path could still name the lead workspace. Block writes there.
	env.BlockedRoots = []string{o.lead.Env.Dir}
	if len(files) > 0 {
		env.WritePaths = append([]string(nil), files...)
	}
	return &env
}

func (o *Orchestrator) writerAgent(r *writerRun, env *tools.Env, ws *session.Session, model string, budget *workerTokenBudget, cancelBudget context.CancelCauseFunc, hooks agent.Hooks, goal string, facts []string) (*agent.Agent, *reportSink) {
	parentUsage := hooks.OnUsage
	hooks.OnUsage = func(u llm.Usage, history int) {
		if budget.add(u) {
			cancelBudget(budget.err())
		}
		if parentUsage != nil {
			parentUsage(u, history)
		}
	}
	sink := &reportSink{}
	writerTools := append(append([]tools.Tool{}, o.writerTools...), &submitReportTool{sink: sink})
	return &agent.Agent{
		Provider:      o.lead.Provider,
		Model:         model,
		Tools:         writerTools,
		Env:           env,
		Sess:          ws,
		MaxSteps:      r.maxSteps,
		Compact:       o.lead.Compact && o.lead.ContextWindow > 0,
		ContextWindow: o.lead.ContextWindow,
		NumCtx:        o.lead.NumCtx,
		SystemExtra: func() string {
			if r.full {
				return workerSystemPromptAccess(delegateArgs{
					Title: r.task.Title, Objective: r.task.Objective, Context: r.task.Context,
					Role: r.task.Role, Deliverable: r.task.Deliverable, Files: r.task.Files,
				}, r.step, goal, facts, true)
			}
			return writerSystemPrompt(r.task, r.step, goal, facts, r.context)
		},
		Hooks: hooks,
	}, sink
}

func writerBriefFor(r *writerRun, goal string, facts []string) string {
	if r.full {
		brief := workerBrief(delegateArgs{
			Title: r.task.Title, Objective: r.task.Objective, Context: r.task.Context,
			Role: r.task.Role, Deliverable: r.task.Deliverable, Files: r.task.Files,
		}, goal, facts)
		if r.context != "" {
			brief += "\n\n" + r.context
		}
		return brief
	}
	return writerBriefWriter(r.task, goal, facts)
}

func (o *Orchestrator) runWriter(ctx context.Context, r *writerRun, goal string, facts []string) {
	cfg := o.cfg
	if r.step != nil {
		if err := validateWorktreeChecks(r.step.Checks, o.worktrees.Repo); err != nil {
			r.outcome.Status = "failed"
			r.outcome.Error = err.Error()
			return
		}
	}
	r.base = o.currentWriterBase()
	wt, err := o.acquireWorktree(r.task.StepID)
	if err != nil {
		r.outcome.Error = err.Error()
		return
	}
	r.worktree = wt
	r.outcome.Worktree = wt.Path
	model := o.lead.Model
	if r.model != "" {
		model = r.model
	}
	r.model = model
	title := "writer: "
	if r.full {
		title = "worker: "
	}
	ws, err := session.CreateAt(cfg.WorkerHome, wt.Path, o.lead.Provider.Name(), model, title+oneLine(r.task.Title, 80))
	if err != nil {
		r.outcome.Error = "create worker session: " + err.Error()
		return
	}
	r.session = ws.Path()
	r.ws = ws
	r.outcome.WorkerSession = r.session
	env := o.writerEnv(wt.Path, r.task.Files)
	defer env.CloseJobs()
	defer env.CloseLSP()
	defer env.CloseBrowser()
	defer env.ClosePreview()
	runCtx, cancel := context.WithTimeoutCause(ctx, cfg.WriterTimeout, fmt.Errorf("writer timed out after %s", cfg.WriterTimeout))
	defer cancel()
	budgetCtx, cancelBudget := context.WithCancelCause(runCtx)
	defer cancelBudget(nil)
	budget := &workerTokenBudget{limit: r.tokens}
	hooks := workerHooks(o.lead.Hooks, r.task.Title, &o.hooksMu)
	writer, sink := o.writerAgent(r, env, ws, model, budget, cancelBudget, hooks, goal, facts)
	runErr := writer.Run(budgetCtx, writerBriefFor(r, goal, facts))
	stats := writer.Usage()
	r.outcome.Usage = usageFrom(stats)
	content := lastAssistantContent(ws)
	report := sink.getReport()
	if report == nil {
		var ok bool
		report, ok = parseReport(content)
		if !ok {
			report = fallbackReport(content)
		}
	}
	report.Usage = r.outcome.Usage
	r.outcome.Report = report
	if budget.exceeded {
		runErr = fmt.Errorf("writer exceeded token budget (%d/%d tokens)", budget.used, budget.limit)
		report.Status = "failed"
	} else if runCtx.Err() == context.DeadlineExceeded {
		runErr = fmt.Errorf("writer timed out after %s", cfg.WriterTimeout)
	}
	if runErr != nil {
		r.outcome.Status = "failed"
		r.outcome.Error = runErr.Error()
		if report.Status != "completed" {
			report.Status = "failed"
		}
		return
	}
	report.Status = normalizeReportStatus(report.Status)
	r.outcome.Status = report.Status
	if report.Status == "needs_input" {
		o.rememberResumable(o.newWriterResumable(r, ws, goal, facts))
		return
	}
	if report.Status != "completed" {
		r.outcome.Error = "writer reported " + report.Status + "; changes were not merged"
		return
	}
	o.finishWriter(ctx, r, env, goal)
}

// finishWriter runs the post-report pipeline: checks, commit, ownership,
// diff, and review. It expects r.outcome.Report to be a completed report.
func (o *Orchestrator) finishWriter(ctx context.Context, r *writerRun, env *tools.Env, goal string) {
	cfg := o.cfg
	var checks []Check
	if r.step != nil {
		checks = r.step.Checks
	}
	r.worktreeChecks = cfg.checkPolicy() != "merge" && len(checks) > 0
	if r.worktreeChecks {
		checkResults := runCheckCommands(ctx, env, checks, 0)
		r.outcome.Checks = checkResults
		for _, c := range checkResults {
			if c.Status != CheckPassed {
				r.outcome.Status = "failed"
				r.outcome.Error = "worktree check failed: " + c.Command
				return
			}
		}
	}
	hasChanges, err := r.worktree.HasChanges()
	if err != nil {
		r.outcome.Status = "failed"
		r.outcome.Error = "check worktree: " + err.Error()
		return
	}
	if !hasChanges {
		if r.allowEmpty {
			r.outcome.Status = "completed"
			return
		}
		r.outcome.Status = "failed"
		r.outcome.Error = "writer made no changes"
		return
	}
	if _, err := r.worktree.Commit("wombat writer: " + r.task.Title); err != nil {
		r.outcome.Status = "failed"
		r.outcome.Error = "commit: " + err.Error()
		return
	}
	changed, err := r.worktree.ChangedFiles()
	if err != nil {
		r.outcome.Status = "failed"
		r.outcome.Error = "changed files: " + err.Error()
		return
	}
	if len(r.task.Files) > 0 {
		if bad := outsideOwnership(changed, r.task.Files); len(bad) > 0 {
			r.outcome.Status = "failed"
			r.outcome.Error = "writer changed files outside declared ownership: " + strings.Join(bad, ", ")
			return
		}
	}
	r.outcome.ChangedFiles = changed
	patch, err := r.worktree.Diff()
	if err != nil {
		r.outcome.Status = "failed"
		r.outcome.Error = "diff: " + err.Error()
		return
	}
	if strings.TrimSpace(patch) == "" {
		if r.allowEmpty {
			r.outcome.Status = "completed"
			return
		}
		r.outcome.Status = "failed"
		r.outcome.Error = "writer produced an empty patch"
		return
	}
	r.patch = patch
	if o.shouldReview(r) {
		review, reviewErr := o.reviewWriter(ctx, r, goal)
		r.outcome.Review = review
		if review != nil {
			r.outcome.Usage.Calls += review.Usage.Calls
			r.outcome.Usage.In += review.Usage.In
			r.outcome.Usage.Cached += review.Usage.Cached
			r.outcome.Usage.Out += review.Usage.Out
		}
		switch {
		case reviewErr != nil:
			r.outcome.Status = "failed"
			r.outcome.Error = "review failed: " + reviewErr.Error()
			return
		case review != nil && (review.Verdict == "changes_requested" || review.Verdict == "rejected"):
			r.outcome.Status = "failed"
			r.outcome.Error = "reviewer " + review.Verdict + ": " + oneLine(review.Summary, 500)
			return
		case review != nil && review.Verdict == "unstructured":
			r.outcome.Warning = "reviewer returned no clear verdict; merged on passing checks"
		}
	}
}

// runFullWorker runs a delegated worker with full tools in an isolated
// worktree and merges any changes after review. Research tasks that make no
// changes still complete normally.
func (o *Orchestrator) runFullWorker(ctx context.Context, a delegateArgs, step *Step, goal string, facts []string, maxSteps, tokenBudget int) (*WorkerReport, ReportUsage, string, bool, error) {
	var copyStep *Step
	if step != nil {
		c := *step
		copyStep = &c
	}
	files := append([]string(nil), a.Files...)
	r := writerRun{
		task: writerTask{
			StepID: a.StepID, Title: a.Title, Objective: a.Objective, Context: a.Context,
			Role: a.Role, Deliverable: a.Deliverable, Files: files,
		},
		step:       copyStep,
		maxSteps:   maxSteps,
		tokens:     tokenBudget,
		context:    buildContextPack(o.worktrees.Repo, files, a.Objective),
		full:       true,
		allowEmpty: true,
		model:      o.cfg.ScoutModel,
		outcome:    writerOutcome{StepID: a.StepID, Title: a.Title},
	}
	o.runWriter(ctx, &r, goal, facts)
	touched := false
	if r.outcome.Status == "completed" && r.patch != "" {
		touched = true
		o.mergeMu.Lock()
		err := o.mergeWriter(ctx, &r, &writeBatchResult{})
		o.mergeMu.Unlock()
		if err != nil {
			r.outcome.Status = "failed"
			r.outcome.Error = combineWarnings(err, r.outcome.Warning)
		}
	}
	if r.outcome.Status == "completed" && r.worktree != nil {
		o.releaseWorktree(r.worktree, o.cfg.KeepWorktrees)
		r.worktree = nil
	}
	if r.outcome.Status != "completed" && r.outcome.Status != "needs_input" && r.outcome.Report != nil {
		r.outcome.Report.Status = "failed"
	}
	if ctx.Err() != nil && r.outcome.Status != "completed" && r.outcome.Status != "needs_input" && r.worktree != nil {
		o.releaseWorktree(r.worktree, o.cfg.KeepWorktrees)
		r.worktree = nil
	}
	var runErr error
	if r.outcome.Error != "" {
		runErr = fmt.Errorf("%s", r.outcome.Error)
	}
	return r.outcome.Report, r.outcome.Usage, r.session, touched, runErr
}

func (o *Orchestrator) reviewWriter(ctx context.Context, r *writerRun, goal string) (*Review, error) {
	cfg := o.cfg
	o.mu.Lock()
	leadModel := o.lead.Model
	o.mu.Unlock()
	model := cfg.reviewerModel(leadModel)
	ws, err := session.CreateAt(cfg.WorkerHome, r.worktree.Path, o.lead.Provider.Name(), model, "review: "+oneLine(r.task.Title, 80))
	if err != nil {
		return nil, fmt.Errorf("create reviewer session: %w", err)
	}
	env := *o.lead.Env
	env.Isolate()
	defer env.CloseJobs()
	defer env.CloseLSP()
	defer env.CloseBrowser()
	defer env.ClosePreview()
	env.Dir = r.worktree.Path
	env.ReadOnly = true
	env.Permit = nil
	env.Checkpoint = nil
	env.DenyGit = true
	env.DenyOutside = true
	env.AllowOutside = false
	env.BlockedRoots = []string{o.lead.Env.Dir}
	if len(r.task.Files) > 0 {
		env.WritePaths = append([]string(nil), r.task.Files...)
	}
	runCtx, cancel := context.WithTimeoutCause(ctx, cfg.WriterTimeout, fmt.Errorf("reviewer timed out after %s", cfg.WriterTimeout))
	defer cancel()
	budgetCtx, cancelBudget := context.WithCancelCause(runCtx)
	defer cancelBudget(nil)
	reviewTokens := 0
	if r.tokens > 0 {
		reviewTokens = r.tokens / 2
		if reviewTokens < minWorkerTokenBudget {
			reviewTokens = minWorkerTokenBudget
		}
	}
	budget := &workerTokenBudget{limit: reviewTokens}
	hooks := workerHooks(o.lead.Hooks, "review: "+r.task.Title, &o.hooksMu)
	parentUsage := hooks.OnUsage
	hooks.OnUsage = func(u llm.Usage, history int) {
		if budget.add(u) {
			cancelBudget(budget.err())
		}
		if parentUsage != nil {
			parentUsage(u, history)
		}
	}
	sink := &reportSink{}
	reviewerTools := append(append([]tools.Tool{}, o.workerTools...), &submitReviewTool{sink: sink})
	reviewer := &agent.Agent{
		Provider:      o.lead.Provider,
		Model:         model,
		Tools:         reviewerTools,
		Env:           &env,
		Sess:          ws,
		MaxSteps:      cfg.ReviewerMaxSteps,
		Compact:       o.lead.Compact && o.lead.ContextWindow > 0,
		ContextWindow: o.lead.ContextWindow,
		NumCtx:        o.lead.NumCtx,
		SystemExtra: func() string {
			return reviewerSystemPrompt(r, goal)
		},
		Hooks: hooks,
	}
	runErr := reviewer.Run(budgetCtx, reviewerBrief(r))
	stats := reviewer.Usage()
	usage := usageFrom(stats)
	content := lastAssistantContent(ws)
	review := sink.getReview()
	if review == nil {
		var ok bool
		review, ok = parseReview(content)
		if !ok {
			review = &Review{Verdict: "unstructured", Summary: "reviewer returned no structured verdict"}
		}
	}
	review.Usage = usage
	if budget.exceeded {
		return review, fmt.Errorf("reviewer exceeded token budget (%d/%d tokens)", budget.used, budget.limit)
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return review, fmt.Errorf("reviewer timed out after %s", cfg.WriterTimeout)
	}
	if runErr != nil {
		return review, runErr
	}
	return review, nil
}

func (o *Orchestrator) shouldReview(r *writerRun) bool {
	switch o.cfg.reviewPolicy() {
	case "never":
		return false
	case "auto":
		return !docsOnly(r.outcome.ChangedFiles)
	default:
		return true
	}
}

// mergeChecksNeeded reports whether the post-merge check pass can be skipped.
// The worktree checks already exercised the patch; a second run is only
// useful when the merge base moved under the writer or when worktree checks
// were deliberately skipped.
func (o *Orchestrator) mergeChecksNeeded(r *writerRun) bool {
	if r.step == nil || len(r.step.Checks) == 0 {
		return false
	}
	switch o.cfg.checkPolicy() {
	case "worktree":
		return false
	case "merge", "both":
		return true
	default:
		if !r.worktreeChecks {
			return true
		}
		base := r.base
		return base == "" || base != o.currentWriterBase()
	}
}

func docsOnly(files []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		switch strings.ToLower(filepath.Ext(f)) {
		case ".md", ".mdx", ".markdown", ".txt", ".rst", ".adoc":
			continue
		}
		if strings.HasPrefix(filepath.ToSlash(f), "docs/") {
			continue
		}
		return false
	}
	return true
}

func reviewerSystemPrompt(r *writerRun, goal string) string {
	var b strings.Builder
	b.WriteString("## Reviewer\n")
	b.WriteString("You are an independent code reviewer for another agent's patch. Do not edit files or commit. ")
	b.WriteString("Judge correctness, scope, regressions, security, and whether the checks actually cover the change. ")
	b.WriteString("Be decisive: approve only when the patch is ready to merge; otherwise request changes with concrete findings.\n\n")
	if goal != "" {
		b.WriteString("Project goal: " + oneLine(goal, 800) + "\n")
	}
	fmt.Fprintf(&b, "Task: %s\nObjective: %s\n", oneLine(r.task.Title, 200), oneLine(r.task.Objective, 1200))
	if len(r.task.Files) > 0 {
		b.WriteString("Owned files: " + strings.Join(r.task.Files, ", ") + "\n")
	}
	if r.outcome.Report != nil {
		fmt.Fprintf(&b, "Writer report: %s\n", oneLine(r.outcome.Report.Summary, 1200))
	}
	if len(r.outcome.Checks) > 0 {
		b.WriteString("Checks run in the worktree:\n")
		for _, c := range r.outcome.Checks {
			fmt.Fprintf(&b, "- %s: %s (%s)\n", c.ID, c.Status, c.Command)
		}
	}
	b.WriteString("\nCall submit_review once with your verdict. If tools are unavailable, return only this JSON object:\n")
	b.WriteString(`{"verdict":"approved|changes_requested|rejected","summary":"...","findings":[{"severity":"blocker|major|minor|nit","path":"...","line":1,"issue":"...","suggestion":"..."}]}`)
	b.WriteString("\n")
	return strings.TrimSpace(b.String())
}

func reviewerBrief(r *writerRun) string {
	var b strings.Builder
	b.WriteString("## Patch to review\n")
	b.WriteString(r.task.Objective)
	b.WriteString("\n\n")
	b.WriteString(truncateText(r.patch, 40_000))
	return strings.TrimSpace(b.String())
}

func (o *Orchestrator) mergeWriter(ctx context.Context, r *writerRun, result *writeBatchResult) error {
	if err := o.worktrees.ApplyPatch3Way(r.patch); err != nil {
		r.outcome.Error = "merge conflict (automatic 3-way merge failed): " + err.Error()
		r.outcome.Warning = "worktree kept for inspection: " + r.outcome.Worktree
		return err
	}
	var stepChecks []Check
	if r.step != nil {
		stepChecks = r.step.Checks
	}
	checks := r.outcome.Checks
	if o.mergeChecksNeeded(r) {
		checks = runCheckCommands(ctx, o.lead.Env, stepChecks, 0)
		r.outcome.Checks = checks
	}
	failed := false
	for _, c := range checks {
		if c.Status != CheckPassed {
			failed = true
			break
		}
	}
	if !failed {
		if err := o.worktrees.DiffCheck(); err != nil {
			failed = true
			r.outcome.Error = err.Error()
		}
	}
	if failed {
		if err := o.worktrees.ReversePatch(r.patch); err != nil {
			r.outcome.Warning = "could not revert failed merge: " + err.Error()
		} else {
			r.outcome.Warning = "merged checks failed; patch reverted"
			if r.outcome.Error == "" {
				r.outcome.Error = "merged checks failed"
			}
		}
		_ = o.worktrees.StagePatchPaths(r.patch)
		r.outcome.Status = "failed"
		r.outcome.Merged = false
		return fmt.Errorf("merged checks failed")
	}
	if commit, err := o.worktrees.SnapshotIndex("wombat merge: " + r.task.Title); err != nil {
		r.outcome.Warning = "merged but could not snapshot the index: " + err.Error()
	} else {
		o.setWriterBase(commit)
	}
	r.outcome.Merged = true
	r.outcome.Worktree = ""
	o.mu.Lock()
	if step := o.ledger.Find(r.task.StepID); step != nil {
		step.Status = StepCompleted
		step.Report = r.outcome.Report
		step.Review = r.outcome.Review
		step.Checks = make([]Check, 0, len(checks))
		for _, c := range checks {
			step.Checks = append(step.Checks, Check{
				ID: c.ID, Command: c.Command, Status: c.Status,
				Output: truncateCheckOutput(c.Output), ExitCode: c.ExitCode, RanAt: time.Now(),
			})
		}
		step.Updated = time.Now()
	}
	result.Ledger = o.ledger.View()
	o.mu.Unlock()
	return nil
}

func runCheckCommands(ctx context.Context, env *tools.Env, checks []Check, defaultTimeout time.Duration) []checkOutcome {
	if defaultTimeout <= 0 {
		defaultTimeout = 120 * time.Second
	}
	out := make([]checkOutcome, 0, len(checks))
	for _, c := range checks {
		timeout := defaultTimeout
		if c.TimeoutSeconds > 0 {
			timeout = time.Duration(c.TimeoutSeconds) * time.Second
		}
		run := tools.RunCommand(ctx, env, c.Command, timeout)
		status := CheckPassed
		if run.Failed() {
			status = CheckFailed
		}
		display := run.Display()
		if display == "" {
			display = "(no output)"
		}
		out = append(out, checkOutcome{ID: c.ID, Command: c.Command, Status: status, ExitCode: run.ExitCode, Output: display})
	}
	return out
}

func validateWriters(tasks []writerTask) error {
	seen := make(map[string]string)
	for i, t := range tasks {
		if strings.TrimSpace(t.StepID) == "" {
			return fmt.Errorf("task %d: step_id is required", i+1)
		}
		if strings.TrimSpace(t.Title) == "" {
			return fmt.Errorf("task %d: title is required", i+1)
		}
		if strings.TrimSpace(t.Objective) == "" {
			return fmt.Errorf("task %d: objective is required", i+1)
		}
		if len(t.Files) == 0 {
			return fmt.Errorf("task %d: files must list every path the writer may change", i+1)
		}
		for _, f := range t.Files {
			clean, err := cleanOwnedPath(f)
			if err != nil {
				return fmt.Errorf("task %d: %w", i+1, err)
			}
			if prev, ok := seen[clean]; ok {
				return fmt.Errorf("file ownership overlap: %q is claimed by both %q and %q", clean, prev, t.StepID)
			}
			seen[clean] = t.StepID
		}
	}
	return nil
}

func cleanOwnedPath(path string) (string, error) {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return "", fmt.Errorf("empty file path")
	}
	if filepath.IsAbs(path) || strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("absolute path not allowed: %s", path)
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path escapes workspace: %s", path)
	}
	if clean == ".git" || strings.HasPrefix(clean, ".git/") {
		return "", fmt.Errorf(".git paths are not writable: %s", path)
	}
	if strings.ContainsAny(clean, "*?[") {
		return "", fmt.Errorf("globs are not allowed in ownership: %s", path)
	}
	return clean, nil
}

func outsideOwnership(changed, owned []string) []string {
	allowed := make(map[string]bool, len(owned))
	for _, f := range owned {
		if clean, err := cleanOwnedPath(f); err == nil {
			allowed[clean] = true
		}
	}
	var bad []string
	for _, f := range changed {
		f = filepath.ToSlash(f)
		if !allowed[f] {
			bad = append(bad, f)
		}
	}
	sort.Strings(bad)
	return bad
}

func writerSystemPrompt(t writerTask, step *Step, goal string, facts []string, pack string) string {
	var b strings.Builder
	b.WriteString("## Writer worker\n")
	b.WriteString("You are a coding writer in an isolated git worktree. Implement the task yourself using read/write/edit/glob/grep/bash. ")
	b.WriteString("You may only modify the declared files. Do not commit, change git state, touch .git, or edit files outside your ownership. ")
	b.WriteString("Run the supplied checks locally with repo-relative paths (they run in this worktree), then call submit_report with the structured result. ")
	b.WriteString("If you are blocked on a decision only the lead can make, call submit_report with status=needs_input and list your questions; the lead can resume you in the same worktree.\n\n")
	if goal != "" {
		b.WriteString("Project goal: " + oneLine(goal, 800) + "\n")
	}
	if len(facts) > 0 {
		b.WriteString("Known facts:\n")
		for _, f := range facts {
			fmt.Fprintf(&b, "- %s\n", oneLine(f, 400))
		}
	}
	if step != nil {
		fmt.Fprintf(&b, "Ledger step: [%s] %s\n", step.ID, oneLine(step.Title, 200))
		for _, c := range step.Checks {
			fmt.Fprintf(&b, "- check %s: %s\n", c.ID, c.Command)
		}
	}
	b.WriteString("\nOwned files (all other paths are read-only for you):\n")
	for _, f := range t.Files {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	if pack != "" {
		b.WriteString("\n")
		b.WriteString(pack)
		b.WriteString("\n")
	}
	b.WriteString("\nCall submit_report once with this shape (or return only the JSON object if tools are unavailable):\n")
	b.WriteString(`{"status":"completed|partial|blocked|needs_input|failed","summary":"...","findings":[],"changes":[{"path":"...","summary":"..."}],"tests":["..."],"risks":[],"next_steps":[],"questions":[]}`)
	b.WriteString("\nStatus=completed only if the work is done and all checks pass. Include every changed path in changes.\n")
	return strings.TrimSpace(b.String())
}

func writerBriefWriter(t writerTask, goal string, facts []string) string {
	var b strings.Builder
	b.WriteString("## Implementation task\n")
	b.WriteString(t.Objective)
	b.WriteString("\n")
	if t.Context != "" {
		b.WriteString("\n## Context\n")
		b.WriteString(t.Context)
		b.WriteString("\n")
	}
	if t.Deliverable != "" {
		b.WriteString("\n## Deliverable\n")
		b.WriteString(t.Deliverable)
		b.WriteString("\n")
	}
	b.WriteString("\nOnly edit: ")
	b.WriteString(strings.Join(t.Files, ", "))
	b.WriteString(". Run the checks, keep changes minimal, and finish with the JSON report.")
	return strings.TrimSpace(b.String())
}

// validateWorktreeChecks rejects checks that name the lead workspace by
// absolute path. A writer runs its checks inside the worktree; such a check
// would either fail for the wrong reason or tempt the writer to escape.
func validateWorktreeChecks(checks []Check, repo string) error {
	if strings.TrimSpace(repo) == "" {
		return nil
	}
	root := canonicalWorktreePath(repo)
	for _, check := range checks {
		for _, field := range strings.Fields(check.Command) {
			field = strings.Trim(field, `"'`)
			if field == "" || !filepath.IsAbs(field) {
				continue
			}
			if rel, err := filepath.Rel(root, canonicalWorktreePath(field)); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("write-step check %q uses the lead workspace path %q; checks run in the writer worktree, so use a relative path", check.ID, field)
			}
		}
	}
	return nil
}

func canonicalWorktreePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	dir, base := filepath.Split(abs)
	if resolved, err := filepath.EvalSymlinks(filepath.Clean(dir)); err == nil {
		return filepath.Join(resolved, base)
	}
	return abs
}

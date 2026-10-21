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
	return "Run one to six independent coding tasks in isolated git worktrees. Each writer owns an explicit, non-overlapping file list, runs the ledger checks for its step, gets an independent review, and is merged with a three-way fallback only if checks and review pass. For dependent work use run_plan write steps instead; keep dependent edits in the lead."
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
	StepID       string         `json:"step_id"`
	Title        string         `json:"title"`
	Status       string         `json:"status"`
	Report       *WorkerReport  `json:"report,omitempty"`
	Usage        ReportUsage    `json:"usage"`
	ChangedFiles []string       `json:"changed_files,omitempty"`
	Checks       []checkOutcome `json:"checks,omitempty"`
	Review       *Review        `json:"review,omitempty"`
	Merged       bool           `json:"merged"`
	Worktree     string         `json:"worktree,omitempty"`
	Warning      string         `json:"warning,omitempty"`
	Error        string         `json:"error,omitempty"`
}

type writeBatchResult struct {
	Tasks   []writerOutcome `json:"tasks"`
	Usage   ReportUsage     `json:"usage"`
	Ledger  LedgerView      `json:"ledger"`
	Warning string          `json:"warning,omitempty"`
}

type writerRun struct {
	task      writerTask
	step      *Step
	maxSteps  int
	tokens    int
	context   string
	worktree  *worktree.Worktree
	patch     string
	outcome   writerOutcome
	tokenUsed int
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
	if left := cfg.MaxParallelWriters - o.writers; len(a.Tasks) > left {
		o.mu.Unlock()
		return "", fmt.Errorf("writer budget exhausted: need %d, only %d remain this turn", len(a.Tasks), left)
	}
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
	remaining := cfg.TurnWriterTokenBudget - o.writerTokens
	if remaining <= 0 {
		o.mu.Unlock()
		return "", fmt.Errorf("writer token budget exhausted for this turn (%d tokens)", cfg.TurnWriterTokenBudget)
	}
	perWriter := remaining / len(runs)
	if perWriter > cfg.WriterTokenBudget {
		perWriter = cfg.WriterTokenBudget
	}
	if perWriter < minWorkerTokenBudget {
		o.mu.Unlock()
		return "", fmt.Errorf("not enough writer token budget for %d writers: %d each (min %d)", len(runs), perWriter, minWorkerTokenBudget)
	}
	for i := range runs {
		runs[i].tokens = perWriter
	}
	o.writers += len(runs)
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
		if err == nil && r.worktree != nil && !cfg.KeepWorktrees {
			_ = r.worktree.Remove()
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
		case "partial":
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

func (o *Orchestrator) runWriter(ctx context.Context, r *writerRun, goal string, facts []string) {
	cfg := o.cfg
	o.worktreeMu.Lock()
	wt, err := o.worktrees.CreateAt(r.task.StepID, o.currentWriterBase())
	o.worktreeMu.Unlock()
	if err != nil {
		r.outcome.Error = err.Error()
		return
	}
	r.worktree = wt
	r.outcome.Worktree = wt.Path
	ws, err := session.CreateAt(cfg.WorkerHome, wt.Path, o.lead.Provider.Name(), o.lead.Model, "writer: "+oneLine(r.task.Title, 80))
	if err != nil {
		r.outcome.Error = "create writer session: " + err.Error()
		return
	}
	env := *o.lead.Env
	env.Isolate()
	defer env.CloseJobs()
	defer env.CloseLSP()
	defer env.CloseBrowser()
	env.Dir = wt.Path
	env.ReadOnly = false
	env.Permit = nil
	env.Checkpoint = nil
	env.DenyGit = true
	env.DenyOutside = true
	env.AllowOutside = false
	// Writers never ask the user anything and never reach the network.
	env.DenyNetwork = true
	runCtx, cancel := context.WithTimeout(ctx, cfg.WriterTimeout)
	defer cancel()
	budgetCtx, cancelBudget := context.WithCancel(runCtx)
	defer cancelBudget()
	budget := &workerTokenBudget{limit: r.tokens}
	hooks := workerHooks(o.lead.Hooks, r.task.Title, &o.hooksMu)
	parentUsage := hooks.OnUsage
	hooks.OnUsage = func(u llm.Usage, history int) {
		if budget.add(u) {
			cancelBudget()
		}
		if parentUsage != nil {
			parentUsage(u, history)
		}
	}
	sink := &reportSink{}
	writerTools := append(append([]tools.Tool{}, o.writerTools...), &submitReportTool{sink: sink})
	writer := &agent.Agent{
		Provider:      o.lead.Provider,
		Model:         o.lead.Model,
		Tools:         writerTools,
		Env:           &env,
		Sess:          ws,
		MaxSteps:      r.maxSteps,
		Compact:       o.lead.Compact && o.lead.ContextWindow > 0,
		ContextWindow: o.lead.ContextWindow,
		NumCtx:        o.lead.NumCtx,
		SystemExtra: func() string {
			return writerSystemPrompt(r.task, r.step, goal, facts, r.context)
		},
		Hooks: hooks,
	}
	runErr := writer.Run(budgetCtx, writerBriefWriter(r.task, goal, facts))
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
	if report.Status != "completed" {
		r.outcome.Error = "writer reported " + report.Status + "; changes were not merged"
		return
	}
	checks := runCheckCommands(ctx, &env, r.step.Checks, 0)
	r.outcome.Checks = checks
	for _, c := range checks {
		if c.Status != CheckPassed {
			r.outcome.Status = "failed"
			r.outcome.Error = "worktree check failed: " + c.Command
			return
		}
	}
	if _, err := wt.Commit("wombat writer: " + r.task.Title); err != nil {
		r.outcome.Status = "failed"
		r.outcome.Error = "commit: " + err.Error()
		return
	}
	changed, err := wt.ChangedFiles()
	if err != nil {
		r.outcome.Status = "failed"
		r.outcome.Error = "changed files: " + err.Error()
		return
	}
	r.outcome.ChangedFiles = changed
	if bad := outsideOwnership(changed, r.task.Files); len(bad) > 0 {
		r.outcome.Status = "failed"
		r.outcome.Error = "writer changed files outside declared ownership: " + strings.Join(bad, ", ")
		return
	}
	patch, err := wt.Diff()
	if err != nil {
		r.outcome.Status = "failed"
		r.outcome.Error = "diff: " + err.Error()
		return
	}
	if strings.TrimSpace(patch) == "" {
		r.outcome.Status = "failed"
		r.outcome.Error = "writer produced an empty patch"
		return
	}
	r.patch = patch
	if o.cfg.reviewsWriters() {
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
	env.Dir = r.worktree.Path
	env.ReadOnly = true
	env.Permit = nil
	env.Checkpoint = nil
	env.DenyGit = true
	env.DenyOutside = true
	env.AllowOutside = false
	env.DenyNetwork = true
	runCtx, cancel := context.WithTimeout(ctx, cfg.WriterTimeout)
	defer cancel()
	budgetCtx, cancelBudget := context.WithCancel(runCtx)
	defer cancelBudget()
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
			cancelBudget()
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
	checks := runCheckCommands(ctx, o.lead.Env, r.step.Checks, 0)
	r.outcome.Checks = checks
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
	b.WriteString("Run the supplied checks locally, then call submit_report with the structured result. Do not ask questions.\n\n")
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
	b.WriteString(`{"status":"completed|partial|blocked|failed","summary":"...","findings":[],"changes":[{"path":"...","summary":"..."}],"tests":["..."],"risks":[],"next_steps":[]}`)
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

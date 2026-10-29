package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/agents"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
	"github.com/ramsterr/Wombat/internal/worktree"
)

const maxResumableWorkers = 16

// resumableWorker keeps everything needed to answer a worker's question and
// continue its session. Writers keep their worktree and uncommitted changes.
type resumableWorker struct {
	Path     string
	Kind     string
	Args     delegateArgs
	Task     writerTask
	Step     *Step
	MaxSteps int
	Tokens   int
	Model    string
	Goal     string
	Facts    []string
	Context  string
	Report   *WorkerReport
	Usage    ReportUsage
	Session  *session.Session
	Worktree *worktree.Worktree
	Run      *writerRun
	Full     bool
	// Member is set when the worker was a selected custom agent.
	Member *agents.Member
}

type resumeWorkerTool struct{ o *Orchestrator }

type resumeWorkerArgs struct {
	WorkerSession string `json:"worker_session"`
	WorkerID      string `json:"worker_id"`
	Answer        string `json:"answer"`
}

func (t *resumeWorkerTool) Name() string  { return "resume_worker" }
func (t *resumeWorkerTool) Class() string { return "delegate" }
func (t *resumeWorkerTool) Description() string {
	return "Continue a worker that reported status=needs_input. Pass the worker_session from its outcome (or worker_id), answer its questions, and it resumes the same session and worktree where it stopped. Call submit_report again when done."
}
func (t *resumeWorkerTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"worker_session": map[string]any{"type": "string", "description": "worker_session value from the needs_input outcome"},
			"worker_id":      map[string]any{"type": "string", "description": "alias for worker_session"},
			"answer":         map[string]any{"type": "string", "description": "the lead's answer or decision; include any constraints it adds"},
		},
		"required": []string{"answer"},
	}
}

func (t *resumeWorkerTool) Run(ctx context.Context, raw json.RawMessage, _ *tools.Env) (string, error) {
	var a resumeWorkerArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	return t.o.resumeWorker(ctx, a)
}

func (o *Orchestrator) newWriterResumable(r *writerRun, ws *session.Session, goal string, facts []string) *resumableWorker {
	kind := "writer"
	if r.full {
		kind = "worker"
	}
	return &resumableWorker{
		Path:     ws.Path(),
		Kind:     kind,
		Task:     r.task,
		Step:     r.step,
		MaxSteps: r.maxSteps,
		Tokens:   r.tokens,
		Model:    r.model,
		Goal:     goal,
		Facts:    append([]string(nil), facts...),
		Context:  r.context,
		Report:   r.outcome.Report,
		Usage:    r.outcome.Usage,
		Session:  ws,
		Worktree: r.worktree,
		Run:      r,
		Full:     r.full,
	}
}

func (o *Orchestrator) rememberResumable(rec *resumableWorker) {
	if rec == nil || rec.Path == "" {
		return
	}
	var evicted []*resumableWorker
	o.mu.Lock()
	if o.resumables == nil {
		o.resumables = make(map[string]*resumableWorker)
	}
	if _, ok := o.resumables[rec.Path]; !ok {
		o.resumeOrder = append(o.resumeOrder, rec.Path)
	}
	o.resumables[rec.Path] = rec
	for len(o.resumeOrder) > maxResumableWorkers {
		id := o.resumeOrder[0]
		o.resumeOrder = o.resumeOrder[1:]
		if old := o.resumables[id]; old != nil {
			delete(o.resumables, id)
			evicted = append(evicted, old)
		}
	}
	o.mu.Unlock()
	for _, old := range evicted {
		if old.Worktree != nil {
			o.releaseWorktree(old.Worktree, o.cfg.KeepWorktrees)
		}
	}
}

func (o *Orchestrator) findResumable(key string) *resumableWorker {
	o.mu.Lock()
	rec := o.resumables[key]
	o.mu.Unlock()
	return rec
}

func (o *Orchestrator) forgetResumable(key string) {
	o.mu.Lock()
	rec := o.resumables[key]
	delete(o.resumables, key)
	for i, id := range o.resumeOrder {
		if id == key {
			o.resumeOrder = append(o.resumeOrder[:i], o.resumeOrder[i+1:]...)
			break
		}
	}
	o.mu.Unlock()
	if rec != nil && rec.Worktree != nil {
		o.releaseWorktree(rec.Worktree, o.cfg.KeepWorktrees)
	}
}

func (o *Orchestrator) resumeWorker(ctx context.Context, a resumeWorkerArgs) (string, error) {
	o.resumeMu.Lock()
	defer o.resumeMu.Unlock()
	key := strings.TrimSpace(a.WorkerSession)
	if key == "" {
		key = strings.TrimSpace(a.WorkerID)
	}
	if key == "" {
		return "", fmt.Errorf("worker_session is required (take it from a needs_input outcome)")
	}
	if strings.TrimSpace(a.Answer) == "" {
		return "", fmt.Errorf("answer is required")
	}
	rec := o.findResumable(key)
	if rec == nil {
		return "", fmt.Errorf("no resumable worker for %q; it may have finished or been evicted", key)
	}
	if rec.Member != nil {
		return o.resumeCustomAgent(ctx, rec, a.Answer)
	}
	if rec.Kind == "writer" || rec.Kind == "worker" {
		return o.resumeWriter(ctx, rec, a.Answer)
	}
	return o.resumeScout(ctx, rec, a.Answer)
}

func (o *Orchestrator) resumeScout(ctx context.Context, rec *resumableWorker, answer string) (string, error) {
	cfg := o.cfg
	env := *o.lead.Env
	env.Isolate()
	env.ReadOnly = !rec.Full
	env.Permit = nil
	env.Checkpoint = nil
	defer env.CloseJobs()
	defer env.CloseLSP()
	defer env.CloseBrowser()
	defer env.ClosePreview()
	remaining := remainingTokens(rec.Tokens, rec.Usage)
	if rec.Tokens > 0 && remaining <= 0 {
		return "", fmt.Errorf("worker token budget exhausted (%d tokens)", rec.Tokens)
	}
	runCtx, cancel := context.WithTimeoutCause(ctx, cfg.WorkerTimeout, fmt.Errorf("worker timed out after %s", cfg.WorkerTimeout))
	defer cancel()
	budgetCtx, cancelBudget := context.WithCancelCause(runCtx)
	defer cancelBudget(nil)
	budget := &workerTokenBudget{limit: remaining}
	hooks := workerHooks(o.lead.Hooks, rec.Args.Title, &o.hooksMu)
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
	baseTools := o.workerTools
	if rec.Full {
		baseTools = o.writerTools
	}
	workerTools := append(append([]tools.Tool{}, baseTools...), &submitReportTool{sink: sink})
	worker := &agent.Agent{
		Provider:      o.lead.Provider,
		Model:         rec.Model,
		Tools:         workerTools,
		Env:           &env,
		Sess:          rec.Session,
		MaxSteps:      rec.MaxSteps,
		Compact:       o.lead.Compact && o.lead.ContextWindow > 0,
		ContextWindow: o.lead.ContextWindow,
		NumCtx:        o.lead.NumCtx,
		SystemExtra: func() string {
			return workerSystemPromptAccess(rec.Args, rec.Step, rec.Goal, rec.Facts, rec.Full)
		},
		Hooks: hooks,
	}
	runErr := worker.Run(budgetCtx, resumeBrief(rec.Report, answer))
	usage := usageFrom(worker.Usage())
	report := sink.getReport()
	if report == nil {
		content := lastAssistantContent(rec.Session)
		var ok bool
		report, ok = parseReport(content)
		if !ok {
			report = fallbackReport(content)
		}
	}
	report.Status = normalizeReportStatus(report.Status)
	if runErr != nil {
		report.Status = "failed"
		if report.Summary == "" {
			report.Summary = runErr.Error()
		}
	}
	report.Usage = addUsage(rec.Usage, usage)
	report = trimReport(report)

	o.mu.Lock()
	if rec.Args.StepID != "" {
		if step := o.ledger.Find(rec.Args.StepID); step != nil {
			step.Report = report
			step.Status = reportStatusToStep(report.Status)
			step.Updated = time.Now()
		}
	}
	o.workerTokens += usage.In + usage.Out
	o.ledger.Updated = time.Now()
	saveErr := o.ledger.Save(o.ledgerPath)
	ledger := o.ledger.View()
	o.mu.Unlock()
	if report.Status == "needs_input" {
		rec.Report = report
		rec.Usage = report.Usage
		o.rememberResumable(rec)
	} else {
		o.forgetResumable(rec.Path)
	}
	out := delegateResult{
		StepID:        rec.Args.StepID,
		Report:        report,
		Usage:         usage,
		WorkerSession: rec.Path,
		Ledger:        ledger,
	}
	if runErr != nil {
		out.Warning = runErr.Error()
	}
	if saveErr != nil {
		out.Warning = combineWarnings(fmt.Errorf("%s", out.Warning), "could not persist ledger: "+saveErr.Error())
	}
	return jsonMarshalIndent(out)
}

func (o *Orchestrator) resumeWriter(ctx context.Context, rec *resumableWorker, answer string) (string, error) {
	if rec.Run == nil || rec.Worktree == nil || rec.Session == nil {
		return "", fmt.Errorf("writer resume state is incomplete")
	}
	r := rec.Run
	cfg := o.cfg
	env := o.writerEnv(r.worktree.Path, r.task.Files)
	defer env.CloseJobs()
	defer env.CloseLSP()
	defer env.CloseBrowser()
	defer env.ClosePreview()
	remaining := remainingTokens(rec.Tokens, rec.Usage)
	if rec.Tokens > 0 && remaining <= 0 {
		return "", fmt.Errorf("worker token budget exhausted (%d tokens)", rec.Tokens)
	}
	runCtx, cancel := context.WithTimeoutCause(ctx, cfg.WriterTimeout, fmt.Errorf("writer timed out after %s", cfg.WriterTimeout))
	defer cancel()
	budgetCtx, cancelBudget := context.WithCancelCause(runCtx)
	defer cancelBudget(nil)
	budget := &workerTokenBudget{limit: remaining}
	hooks := workerHooks(o.lead.Hooks, r.task.Title, &o.hooksMu)
	writer, sink := o.writerAgent(r, env, rec.Session, rec.Model, budget, cancelBudget, hooks, rec.Goal, rec.Facts)
	runErr := writer.Run(budgetCtx, resumeBrief(rec.Report, answer))
	usage := usageFrom(writer.Usage())
	report := sink.getReport()
	if report == nil {
		content := lastAssistantContent(rec.Session)
		var ok bool
		report, ok = parseReport(content)
		if !ok {
			report = fallbackReport(content)
		}
	}
	report.Status = normalizeReportStatus(report.Status)
	if runErr != nil {
		report.Status = "failed"
		if report.Summary == "" {
			report.Summary = runErr.Error()
		}
	}
	r.outcome.Usage = addUsage(rec.Usage, usage)
	report.Usage = r.outcome.Usage
	r.outcome.Report = report
	r.outcome.Status = report.Status
	if report.Status == "needs_input" {
		o.countWriterDelta(rec, r.outcome.Usage)
		rec.Report = report
		rec.Usage = r.outcome.Usage
		rec.Run = r
		o.rememberResumable(rec)
		o.updateWriterLedger(r)
		return o.writerResumeResult(rec, usage, false, nil)
	}
	if report.Status != "completed" {
		r.outcome.Error = "writer reported " + report.Status + "; changes were not merged"
		o.countWriterDelta(rec, r.outcome.Usage)
		o.forgetResumable(rec.Path)
		rec.Report = report
		rec.Usage = r.outcome.Usage
		o.updateWriterLedger(r)
		return o.writerResumeResult(rec, usage, false, fmt.Errorf("%s", r.outcome.Error))
	}
	o.finishWriter(ctx, r, env, rec.Goal)
	if r.outcome.Status != "completed" {
		report.Status = r.outcome.Status
		if r.outcome.Error != "" {
			report.Summary = r.outcome.Error
		}
	}
	o.countWriterDelta(rec, r.outcome.Usage)
	rec.Report = r.outcome.Report
	rec.Usage = r.outcome.Usage
	merged := false
	if r.outcome.Status == "completed" && r.patch != "" {
		o.mergeMu.Lock()
		err := o.mergeWriter(ctx, r, &writeBatchResult{})
		o.mergeMu.Unlock()
		if err != nil {
			r.outcome.Status = "failed"
			r.outcome.Error = combineWarnings(err, r.outcome.Warning)
		} else {
			merged = true
		}
	}
	if r.outcome.Status == "completed" && r.worktree != nil {
		o.releaseWorktree(r.worktree, cfg.KeepWorktrees)
		r.worktree = nil
		rec.Worktree = nil
		o.updateWriterLedger(r)
	} else if r.outcome.Status == "needs_input" {
		rec.Run = r
		rec.Report = r.outcome.Report
		rec.Usage = r.outcome.Usage
		o.rememberResumable(rec)
	} else {
		o.forgetResumable(rec.Path)
		o.updateWriterLedger(r)
	}
	r.outcome.Merged = merged
	var werr error
	if r.outcome.Error != "" {
		werr = fmt.Errorf("%s", r.outcome.Error)
	}
	return o.writerResumeResult(rec, usage, merged, werr)
}

func (o *Orchestrator) writerResumeResult(rec *resumableWorker, usage ReportUsage, merged bool, err error) (string, error) {
	o.mu.Lock()
	ledger := o.ledger.View()
	o.mu.Unlock()
	out := delegateResult{
		StepID:        rec.Task.StepID,
		Report:        rec.Report,
		Usage:         usage,
		WorkerSession: rec.Path,
		Ledger:        ledger,
		Merged:        merged,
	}
	if rec.Worktree != nil {
		out.Worktree = rec.Worktree.Path
	}
	if err != nil {
		out.Warning = err.Error()
	}
	return jsonMarshalIndent(out)
}

func (o *Orchestrator) countWriterDelta(rec *resumableWorker, total ReportUsage) {
	delta := subtractUsage(total, rec.Usage)
	if delta.In+delta.Out == 0 {
		return
	}
	o.mu.Lock()
	o.writerTokens += delta.In + delta.Out
	o.mu.Unlock()
}

func subtractUsage(a, b ReportUsage) ReportUsage {
	return ReportUsage{
		Calls:  a.Calls - b.Calls,
		In:     a.In - b.In,
		Cached: a.Cached - b.Cached,
		Out:    a.Out - b.Out,
	}
}

func (o *Orchestrator) updateWriterLedger(r *writerRun) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if r.step == nil || o.ledger == nil {
		return
	}
	if step := o.ledger.Find(r.task.StepID); step != nil {
		step.Report = r.outcome.Report
		if r.outcome.Review != nil {
			step.Review = r.outcome.Review
		}
		if len(r.outcome.Checks) > 0 {
			step.Checks = nil
			for _, c := range r.outcome.Checks {
				step.Checks = append(step.Checks, Check{ID: c.ID, Command: c.Command, Status: c.Status, Output: truncateCheckOutput(c.Output), ExitCode: c.ExitCode, RanAt: time.Now()})
			}
		}
		switch r.outcome.Status {
		case "completed":
			step.Status = StepCompleted
		case "blocked":
			step.Status = StepBlocked
		case "partial", "needs_input":
			step.Status = StepPending
		default:
			step.Status = StepFailed
		}
		step.Updated = time.Now()
	}
	o.ledger.Updated = time.Now()
	_ = o.ledger.Save(o.ledgerPath)
}

func remainingTokens(limit int, used ReportUsage) int {
	if limit <= 0 {
		return 0
	}
	left := limit - (used.In + used.Out)
	if left < 0 {
		return 0
	}
	return left
}

func addUsage(a, b ReportUsage) ReportUsage {
	return ReportUsage{Calls: a.Calls + b.Calls, In: a.In + b.In, Cached: a.Cached + b.Cached, Out: a.Out + b.Out}
}

func resumeBrief(report *WorkerReport, answer string) string {
	var b strings.Builder
	b.WriteString("## Follow-up from the lead\n")
	if report != nil && len(report.Questions) > 0 {
		b.WriteString("You asked:\n")
		for _, q := range report.Questions {
			fmt.Fprintf(&b, "- %s\n", oneLine(q, 600))
		}
	}
	b.WriteString("\nLead's answer:\n")
	b.WriteString(strings.TrimSpace(answer))
	b.WriteString("\n\nContinue the original task from where you stopped; do not restart unless the answer requires it. Call submit_report once when done. Use status=needs_input again only for a genuinely new blocker.\n")
	return b.String()
}

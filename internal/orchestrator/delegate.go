package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

type delegation struct {
	args        delegateArgs
	step        *Step
	maxSteps    int
	tokenBudget int
}

type delegationBatch struct {
	items []delegation
	goal  string
	facts []string
}

type delegationOutcome struct {
	args          delegateArgs
	report        *WorkerReport
	usage         ReportUsage
	workerSession string
	merged        bool
	err           error
}

type delegationResult struct {
	outcomes []delegationOutcome
	usage    ReportUsage
	ledger   LedgerView
	warning  string
}

func (o *Orchestrator) delegate(ctx context.Context, a delegateArgs) (string, error) {
	batch, err := o.prepareDelegations([]delegateArgs{a})
	if err != nil {
		return "", err
	}
	result := o.runDelegations(ctx, batch)
	out := result.outcomes[0]
	raw := delegateResult{
		StepID:        out.args.StepID,
		Report:        out.report,
		Usage:         out.usage,
		WorkerSession: out.workerSession,
		Ledger:        result.ledger,
	}
	raw.Warning = combineWarnings(out.err, result.warning)
	return jsonMarshalIndent(raw)
}

func (o *Orchestrator) delegateMany(ctx context.Context, tasks []delegateArgs) (string, error) {
	if len(tasks) < 2 {
		return "", fmt.Errorf("delegate_many needs at least 2 independent tasks; use delegate for a single task")
	}
	batch, err := o.prepareDelegations(tasks)
	if err != nil {
		return "", err
	}
	result := o.runDelegations(ctx, batch)
	raw := fleetResult{
		Usage:   result.usage,
		Ledger:  result.ledger,
		Warning: result.warning,
	}
	for _, out := range result.outcomes {
		raw.Tasks = append(raw.Tasks, delegateOutcome{
			StepID:        out.args.StepID,
			Title:         out.args.Title,
			Report:        out.report,
			Usage:         out.usage,
			WorkerSession: out.workerSession,
			Warning:       errString(out.err),
		})
	}
	return jsonMarshalIndent(raw)
}

func (o *Orchestrator) prepareDelegations(args []delegateArgs) (*delegationBatch, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("no delegations requested")
	}
	if len(args) > maxFleetTasks {
		return nil, fmt.Errorf("too many scouts in one fleet: %d (max %d)", len(args), maxFleetTasks)
	}
	for i := range args {
		if err := validateDelegationArgs(args[i]); err != nil {
			return nil, fmt.Errorf("task %d: %w", i+1, err)
		}
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	// No delegation cap: a turn may spawn as many workers as it needs.
	if o.cfg.MaxDelegations > 0 {
		if left := o.cfg.MaxDelegations - o.delegations; len(args) > left {
			return nil, fmt.Errorf("delegation budget exhausted: need %d, only %d remain this turn", len(args), left)
		}
	}
	ids := make(map[string]bool, len(args))
	for _, a := range args {
		if a.StepID == "" {
			continue
		}
		if ids[a.StepID] {
			// Several workers may work on the same step: that is how a
			// step gets researched from different angles at once.
			continue
		}
		ids[a.StepID] = true
	}

	batch := &delegationBatch{
		goal:  o.ledger.Goal,
		facts: append([]string(nil), o.ledger.Facts...),
	}
	for _, a := range args {
		var stepCopy *Step
		if a.StepID != "" {
			s := o.ledger.Find(a.StepID)
			if s == nil {
				return nil, fmt.Errorf("unknown step id %q: call update_plan first or omit step_id", a.StepID)
			}
			for _, dep := range s.DependsOn {
				if ids[dep] {
					return nil, fmt.Errorf("steps %q and %q are dependent; run dependent steps sequentially", s.ID, dep)
				}
				d := o.ledger.Find(dep)
				if d == nil || d.Status != StepCompleted {
					status := "missing"
					if d != nil {
						status = string(d.Status)
					}
					return nil, fmt.Errorf("step %q depends on %q (%s): finish dependencies before delegating", s.ID, dep, status)
				}
			}
			copy := *s
			stepCopy = &copy
		}
		maxSteps := a.MaxSteps
		if maxSteps <= 0 {
			maxSteps = o.cfg.WorkerMaxSteps
		}
		batch.items = append(batch.items, delegation{
			args:     a,
			step:     stepCopy,
			maxSteps: maxSteps,
		})
	}

	// No token budget: workers run until they are done.
	perWorker := 0
	if o.cfg.TurnWorkerTokenBudget > 0 {
		remainingTokens := o.cfg.TurnWorkerTokenBudget - o.workerTokens
		if remainingTokens <= 0 {
			return nil, fmt.Errorf("worker token budget exhausted for this turn (%d tokens)", o.cfg.TurnWorkerTokenBudget)
		}
		perWorker = remainingTokens / len(args)
		if o.cfg.WorkerTokenBudget > 0 && perWorker > o.cfg.WorkerTokenBudget {
			perWorker = o.cfg.WorkerTokenBudget
		}
		if perWorker < minWorkerTokenBudget {
			return nil, fmt.Errorf("not enough worker token budget for %d scouts: %d tokens each (min %d)", len(args), perWorker, minWorkerTokenBudget)
		}
	}
	for i := range batch.items {
		batch.items[i].tokenBudget = perWorker
	}

	o.delegations += len(args)
	o.totalDelegations += len(args)
	return batch, nil
}

func (o *Orchestrator) runDelegations(ctx context.Context, batch *delegationBatch) *delegationResult {
	if o.cfg.FullWorkers && o.worktrees != nil {
		o.ensureWriterBase()
	}
	parallel := o.cfg.MaxParallelScouts
	if parallel < 1 {
		parallel = 1
	}
	if parallel > len(batch.items) {
		parallel = len(batch.items)
	}
	outcomes := make([]delegationOutcome, len(batch.items))
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i := range batch.items {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				outcomes[i] = delegationOutcome{
					args: batch.items[i].args,
					report: &WorkerReport{
						Status:  "failed",
						Summary: "worker did not start: " + ctx.Err().Error(),
					},
					err: ctx.Err(),
				}
				return
			}
			d := batch.items[i]
			var report *WorkerReport
			var usage ReportUsage
			var workerSession string
			var merged bool
			var err error
			if o.cfg.FullWorkers && o.worktrees != nil {
				report, usage, workerSession, merged, err = o.runFullWorker(ctx, d.args, d.step, batch.goal, batch.facts, d.maxSteps, d.tokenBudget)
			} else {
				report, usage, workerSession, err = o.runWorker(ctx, d.args, d.step, batch.goal, batch.facts, d.maxSteps, d.tokenBudget, o.cfg.FullWorkers)
			}
			if report == nil {
				report = &WorkerReport{Status: "failed", Summary: "worker returned no report"}
			}
			if err != nil && report.Summary == "" {
				report.Status = "failed"
				report.Summary = err.Error()
			}
			outcomes[i] = delegationOutcome{
				args:          d.args,
				report:        report,
				usage:         usage,
				workerSession: workerSession,
				merged:        merged,
				err:           err,
			}
		}(i)
	}
	wg.Wait()
	mergedAny := false
	for _, out := range outcomes {
		if out.merged {
			mergedAny = true
			break
		}
	}
	if mergedAny && o.worktrees != nil {
		_ = o.worktrees.Unstage()
	}

	for _, out := range outcomes {
		o.lead.AddUsageStats(agent.UsageStats{
			Calls:  out.usage.Calls,
			In:     out.usage.In,
			Cached: out.usage.Cached,
			Out:    out.usage.Out,
		})
	}

	result := &delegationResult{outcomes: outcomes}
	for _, out := range outcomes {
		result.usage.Calls += out.usage.Calls
		result.usage.In += out.usage.In
		result.usage.Cached += out.usage.Cached
		result.usage.Out += out.usage.Out
	}

	o.mu.Lock()
	for _, out := range outcomes {
		if out.args.StepID != "" {
			if s := o.ledger.Find(out.args.StepID); s != nil {
				s.Status = reportStatusToStep(out.report.Status)
				s.Report = out.report
				s.Updated = time.Now()
			}
		}
		o.workerTokens += out.usage.In + out.usage.Out
	}
	o.ledger.Updated = time.Now()
	saveErr := o.ledger.Save(o.ledgerPath)
	result.ledger = o.ledger.View()
	o.mu.Unlock()
	if saveErr != nil {
		result.warning = "could not persist ledger: " + saveErr.Error()
	}
	return result
}

func (o *Orchestrator) runWorker(ctx context.Context, a delegateArgs, step *Step, goal string, facts []string, maxSteps, tokenBudget int, full bool) (*WorkerReport, ReportUsage, string, error) {
	o.mu.Lock()
	cfg := o.cfg
	o.mu.Unlock()

	ws, err := session.CreateAt(cfg.WorkerHome, o.lead.Env.Dir, o.lead.Provider.Name(), o.lead.Model, "delegate: "+oneLine(a.Title, 80))
	if err != nil {
		return nil, ReportUsage{}, "", fmt.Errorf("create worker session: %w", err)
	}
	env := *o.lead.Env
	env.ReadOnly = !full
	env.Permit = nil
	env.Isolate()
	defer env.CloseJobs()
	defer env.CloseLSP()
	defer env.CloseBrowser()

	runCtx, cancel := context.WithTimeoutCause(ctx, cfg.WorkerTimeout, fmt.Errorf("worker timed out after %s", cfg.WorkerTimeout))
	defer cancel()
	budgetCtx, cancelBudget := context.WithCancelCause(runCtx)
	defer cancelBudget(nil)
	budget := &workerTokenBudget{limit: tokenBudget}
	hooks := workerHooks(o.lead.Hooks, a.Title, &o.hooksMu)
	parentUsage := hooks.OnUsage
	hooks.OnUsage = func(u llm.Usage, history int) {
		if budget.add(u) {
			cancelBudget(budget.err())
		}
		if parentUsage != nil {
			parentUsage(u, history)
		}
	}

	model := o.lead.Model
	if o.cfg.ScoutModel != "" {
		model = o.cfg.ScoutModel
	}
	sink := &reportSink{}
	baseTools := o.workerTools
	if full {
		baseTools = o.writerTools
	}
	workerTools := append(append([]tools.Tool{}, baseTools...), &submitReportTool{sink: sink})
	worker := &agent.Agent{
		Provider:      o.lead.Provider,
		Model:         model,
		Tools:         workerTools,
		Env:           &env,
		Sess:          ws,
		MaxSteps:      maxSteps,
		Compact:       o.lead.Compact && o.lead.ContextWindow > 0,
		ContextWindow: o.lead.ContextWindow,
		NumCtx:        o.lead.NumCtx,
		SystemExtra: func() string {
			return workerSystemPromptAccess(a, step, goal, facts, full)
		},
		Hooks: hooks,
	}

	brief := workerBrief(a, goal, facts)
	if full && o.worktrees == nil {
		if pack := buildContextPack(o.lead.Env.Dir, a.Files, a.Objective); pack != "" {
			brief += "\n\n" + pack
		}
	}
	runErr := worker.Run(budgetCtx, brief)
	stats := worker.Usage()
	usage := usageFrom(stats)
	content := lastAssistantContent(ws)
	report := sink.getReport()
	if report == nil {
		var ok bool
		report, ok = parseReport(content)
		if !ok {
			report = fallbackReport(content)
		}
	}
	report.Usage = usage
	if budget.exceeded {
		runErr = fmt.Errorf("worker exceeded token budget (%d/%d tokens)", budget.used, budget.limit)
		report.Status = "failed"
	} else if runCtx.Err() == context.DeadlineExceeded {
		runErr = fmt.Errorf("worker timed out after %s", cfg.WorkerTimeout)
	}
	if runErr != nil {
		if report.Status != "completed" {
			report.Status = "failed"
		}
		if report.Summary == "" || report.Status == "failed" {
			report.Summary = "worker stopped: " + runErr.Error()
		}
	}
	return trimReport(report), usage, ws.Path(), runErr
}

type workerTokenBudget struct {
	mu       sync.Mutex
	limit    int
	used     int
	exceeded bool
}

func (b *workerTokenBudget) err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return fmt.Errorf("token budget exceeded (%d/%d tokens)", b.used, b.limit)
}

func (b *workerTokenBudget) add(u llm.Usage) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used += u.In + u.Out
	if b.limit > 0 && b.used > b.limit {
		b.exceeded = true
		return true
	}
	return false
}

func workerHooks(parent agent.Hooks, title string, mu *sync.Mutex) agent.Hooks {
	tag := "worker:" + oneLine(title, 40) + ":"
	lock := func() {
		if mu != nil {
			mu.Lock()
		}
	}
	unlock := func() {
		if mu != nil {
			mu.Unlock()
		}
	}
	return agent.Hooks{
		OnTool: func(name, detail string) {
			lock()
			defer unlock()
			if parent.OnTool != nil {
				parent.OnTool("worker:"+name, tag+" "+detail)
			}
		},
		OnToolResult: func(name, result string, dur time.Duration) {
			lock()
			defer unlock()
			if parent.OnToolResult != nil {
				parent.OnToolResult("worker:"+name, result, dur)
			}
		},
		OnUsage: func(u llm.Usage, history int) {
			lock()
			defer unlock()
			if parent.OnUsage != nil {
				parent.OnUsage(u, -1)
			}
		},
		OnCompact: func(kind string, before, after int) {
			lock()
			defer unlock()
			if parent.OnCompact != nil {
				parent.OnCompact("worker:"+kind, before, after)
			}
		},
	}
}

func lastAssistantContent(s *session.Session) string {
	if s == nil {
		return ""
	}
	for i := len(s.Messages) - 1; i >= 0; i-- {
		m := s.Messages[i]
		if m.Role == "assistant" && strings.TrimSpace(m.Content) != "" {
			return m.Content
		}
	}
	return ""
}

func workerSystemPrompt(a delegateArgs, step *Step, goal string, facts []string) string {
	return workerSystemPromptAccess(a, step, goal, facts, false)
}

func workerSystemPromptAccess(a delegateArgs, step *Step, goal string, facts []string, full bool) string {
	var b strings.Builder
	b.WriteString("## Delegated worker\n")
	if full {
		b.WriteString("You are a full-access worker for a coding agent. You cannot see the lead conversation. ")
		b.WriteString("You may read, write, edit, run bash, use the network, and use every available tool in your workspace (a worktree when available). Fetch any local or web docs, skills, and references you need, and cite what you use. ")
		b.WriteString("Your changes are committed, reviewed, and merged into the lead workspace; keep them scoped to the objective. ")
		b.WriteString("Stay inside the slice and output file in your brief; do not re-derive the whole dataset or repeat the same reads. Research only unknown external facts; after at most four sources, write the partial file and finish. Never run find / or root recursive searches. ")
		b.WriteString("Do not ask the user questions or delegate further. Make reasonable assumptions, state them in risks, and prefer evidence.\n\n")
	} else {
		b.WriteString("You are a read-only research worker for a coding agent. You cannot see the lead conversation. ")
		b.WriteString("Do not modify files, run mutating commands, ask the user questions, or request more work. ")
		b.WriteString("You have read, glob, grep, batch, websearch, webfetch, and bash for non-mutating commands. ")
		b.WriteString("Bash is read-only: ls, which, grep, and curl or HTTP to stdout are fine, but mkdir, redirection, " +
			"downloads to disk, ffmpeg output, git mutations, and file writes are refused. ")
		b.WriteString("If the task needs a file created or downloaded, return status=blocked with the exact command the lead should run. ")
		b.WriteString("Use one batch or command for related reads; do not inspect the same dataset repeatedly. Research only unknown external facts; after at most four sources, write the partial report and finish. Never run find / or root recursive searches. ")
		b.WriteString("Make reasonable assumptions when context is missing, state them in risks, and prefer evidence over speculation.\n\n")
	}
	if goal != "" {
		b.WriteString("Project goal: ")
		b.WriteString(oneLine(goal, 800))
		b.WriteString("\n")
	}
	if len(facts) > 0 {
		b.WriteString("Known facts:\n")
		for _, f := range facts {
			fmt.Fprintf(&b, "- %s\n", oneLine(f, 400))
		}
	}
	if step != nil {
		fmt.Fprintf(&b, "Ledger step: [%s] %s\n", step.ID, oneLine(step.Title, 200))
	}
	if full && len(a.Files) > 0 {
		b.WriteString("Owned files (all other paths are read-only for you):\n")
		for _, f := range a.Files {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	b.WriteString("\nCall submit_report once with your final structured report. If you cannot call tools, return only a JSON object as your final answer, with this shape:\n")
	b.WriteString(`{"status":"completed|partial|blocked|failed","summary":"...","findings":[{"claim":"...","evidence":"...","path":"...","lines":"..."}],"changes":[],"tests":[],"risks":[],"next_steps":[]}`)
	b.WriteString("\n\nRules for the report:\n")
	b.WriteString("- summary: what you established, in at most 6 sentences.\n")
	b.WriteString("- findings: only claims backed by evidence; use path:line references when possible.\n")
	if full {
		b.WriteString("- changes: list every file you created or modified.\n")
	} else {
		b.WriteString("- changes: leave empty; you are read-only.\n")
	}
	b.WriteString("- tests: commands you ran or evidence you inspected, including their outcome.\n")
	b.WriteString("- status=completed only if you answered the objective; use partial/blocked otherwise.\n")
	b.WriteString("- Do not include markdown fences or prose outside the JSON object.\n")
	if a.Deliverable != "" {
		b.WriteString("\nRequired deliverable: ")
		b.WriteString(oneLine(a.Deliverable, 1000))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func workerBrief(a delegateArgs, goal string, facts []string) string {
	var b strings.Builder
	b.WriteString("## Task\n")
	b.WriteString(a.Objective)
	b.WriteString("\n")
	if a.Context != "" {
		b.WriteString("\n## Context\n")
		b.WriteString(a.Context)
		b.WriteString("\n")
	}
	if a.Deliverable != "" {
		b.WriteString("\n## Deliverable\n")
		b.WriteString(a.Deliverable)
		b.WriteString("\n")
	}
	if a.Role != "" {
		b.WriteString("\nRole: ")
		b.WriteString(a.Role)
		b.WriteString("\n")
	}
	if goal != "" {
		b.WriteString("\nProject goal: ")
		b.WriteString(oneLine(goal, 800))
		b.WriteString("\n")
	}
	if len(facts) > 0 {
		b.WriteString("\nKnown facts:\n")
		for _, f := range facts {
			fmt.Fprintf(&b, "- %s\n", oneLine(f, 400))
		}
	}
	b.WriteString("\nWork independently, cite evidence, and finish with the JSON report.")
	return strings.TrimSpace(b.String())
}

func combineWarnings(err error, warning string) string {
	parts := make([]string, 0, 2)
	if err != nil {
		parts = append(parts, err.Error())
	}
	if warning != "" {
		parts = append(parts, warning)
	}
	return strings.Join(parts, "; ")
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

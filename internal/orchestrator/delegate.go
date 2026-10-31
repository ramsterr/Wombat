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
	batch, err := o.prepareDelegations([]delegateArgs{a}, false)
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
		return "", fmt.Errorf("delegate_many needs at least 2 tasks; use delegate for a single task")
	}
	deps, err := validateFleetDAG(tasks)
	if err != nil {
		return "", err
	}
	batch, err := o.prepareDelegations(tasks, deps != nil)
	if err != nil {
		return "", err
	}
	var result *delegationResult
	if deps != nil {
		result = o.runDelegationDAG(ctx, batch, deps)
	} else {
		result = o.runDelegations(ctx, batch)
	}
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

func (o *Orchestrator) prepareDelegations(args []delegateArgs, allowFleetDeps bool) (*delegationBatch, error) {
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
		if id := strings.TrimSpace(args[i].Agent); id != "" {
			switch strings.ToLower(id) {
			case "lead", "default", "me", "self":
				args[i].Agent = ""
				continue
			}
			member := o.teamMember(id)
			if member == nil {
				known := "none selected"
				if names := o.teamIDs(); len(names) > 0 {
					known = strings.Join(names, ", ")
				}
				return nil, fmt.Errorf("task %d: unknown agent %q (known agents: %s)", i+1, id, known)
			}
			args[i].Agent = member.ID
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
	fleetKeys := make(map[string]bool, len(args)*2)
	for _, a := range args {
		if a.ID != "" {
			fleetKeys[a.ID] = true
		}
		if a.StepID == "" {
			continue
		}
		fleetKeys[a.StepID] = true
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
				if allowFleetDeps && fleetKeys[dep] {
					continue
				}
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
				outcomes[i] = failedDelegation(batch.items[i].args, ctx.Err())
				return
			}
			outcomes[i] = o.runOneDelegation(ctx, batch.items[i], batch.goal, batch.facts)
		}(i)
	}
	wg.Wait()
	return o.finalizeDelegations(outcomes)
}

// runDelegationDAG executes a fleet whose tasks may depend on earlier tasks in
// the same fleet. A dependent only starts after its upstream tasks complete,
// and receives their reports in its context.
func (o *Orchestrator) runDelegationDAG(ctx context.Context, batch *delegationBatch, deps map[string][]string) *delegationResult {
	if o.cfg.FullWorkers && o.worktrees != nil {
		o.ensureWriterBase()
	}
	n := len(batch.items)
	outcomes := make([]delegationOutcome, n)
	status := make([]string, n)
	index := make(map[string]int, n)
	for i, item := range batch.items {
		index[fleetID(item.args)] = i
		status[i] = "pending"
	}
	type dagResult struct {
		index   int
		outcome delegationOutcome
	}
	parallel := o.cfg.MaxParallelScouts
	if parallel < 1 {
		parallel = 1
	}
	ch := make(chan dagResult, n)
	running := 0
	started := 0
	remaining := n
	for remaining > 0 {
		for running < parallel && started < n {
			next := -1
			for i := range batch.items {
				if status[i] != "pending" {
					continue
				}
				ready, blocked, reason := dagReady(fleetID(batch.items[i].args), deps, status, index)
				if blocked {
					status[i] = "blocked"
					remaining--
					outcomes[i] = failedDelegation(batch.items[i].args, fmt.Errorf("%s", reason))
					next = -1
					break
				}
				if ready {
					next = i
					break
				}
			}
			if next < 0 {
				break
			}
			status[next] = "running"
			started++
			running++
			item := batch.items[next]
			if len(deps[fleetID(item.args)]) > 0 {
				item.args = withUpstreamContext(item.args, deps[fleetID(item.args)], index, outcomes)
			}
			go func(i int, item delegation) {
				ch <- dagResult{index: i, outcome: o.runOneDelegation(ctx, item, batch.goal, batch.facts)}
			}(next, item)
		}
		if running == 0 {
			// Every remaining task is blocked; the loop above already
			// accounted for them.
			break
		}
		res := <-ch
		running--
		remaining--
		status[res.index] = res.outcome.report.Status
		if status[res.index] == "" {
			status[res.index] = "failed"
		}
		outcomes[res.index] = res.outcome
	}
	for i := range batch.items {
		if status[i] == "pending" {
			status[i] = "blocked"
			outcomes[i] = failedDelegation(batch.items[i].args, fmt.Errorf("dependency did not complete"))
		}
	}
	return o.finalizeDelegations(outcomes)
}

func dagReady(id string, deps map[string][]string, status []string, index map[string]int) (ready, blocked bool, reason string) {
	for _, dep := range deps[id] {
		j, ok := index[dep]
		if !ok {
			blocked = true
			reason = "unknown dependency " + dep
			return
		}
		switch status[j] {
		case "pending", "running":
			return false, false, ""
		case "completed":
		default:
			blocked = true
			reason = "dependency " + dep + " is " + status[j]
			return
		}
	}
	return true, false, ""
}

func fleetID(a delegateArgs) string {
	if strings.TrimSpace(a.ID) != "" {
		return strings.TrimSpace(a.ID)
	}
	return strings.TrimSpace(a.StepID)
}

func withUpstreamContext(a delegateArgs, depIDs []string, index map[string]int, outcomes []delegationOutcome) delegateArgs {
	var b strings.Builder
	if strings.TrimSpace(a.Context) != "" {
		b.WriteString(a.Context)
		b.WriteString("\n\n")
	}
	b.WriteString("## Upstream results\n")
	for _, id := range depIDs {
		j, ok := index[id]
		if !ok || outcomes[j].report == nil {
			continue
		}
		out := outcomes[j]
		fmt.Fprintf(&b, "- [%s] %s: %s\n", id, out.args.Title, oneLine(out.report.Summary, 1200))
		for _, f := range out.report.Findings {
			fmt.Fprintf(&b, "  - %s (%s)\n", oneLine(f.Claim, 400), oneLine(f.Evidence, 300))
		}
		for _, c := range out.report.Changes {
			fmt.Fprintf(&b, "  - changed %s: %s\n", c.Path, oneLine(c.Summary, 200))
		}
	}
	a.Context = strings.TrimSpace(b.String())
	return a
}

func failedDelegation(a delegateArgs, err error) delegationOutcome {
	report := &WorkerReport{Status: "failed", Summary: err.Error()}
	return delegationOutcome{args: a, report: report, err: err}
}

func (o *Orchestrator) runOneDelegation(ctx context.Context, d delegation, goal string, facts []string) delegationOutcome {
	var report *WorkerReport
	var usage ReportUsage
	var workerSession string
	var merged bool
	var err error
	if member := o.teamMember(d.args.Agent); member != nil {
		report, usage, workerSession, err = o.runTeamWorker(ctx, d.args, member, d.step, goal, facts, d.maxSteps, d.tokenBudget)
		if report == nil {
			report = &WorkerReport{Status: "failed", Summary: "agent returned no report"}
		}
		if err != nil && report.Summary == "" {
			report.Status = "failed"
			report.Summary = err.Error()
		}
		return delegationOutcome{
			args:          d.args,
			report:        report,
			usage:         usage,
			workerSession: workerSession,
			err:           err,
		}
	}
	if o.cfg.FullWorkers && o.worktrees != nil {
		report, usage, workerSession, merged, err = o.runFullWorker(ctx, d.args, d.step, goal, facts, d.maxSteps, d.tokenBudget)
	} else {
		report, usage, workerSession, err = o.runWorker(ctx, d.args, d.step, goal, facts, d.maxSteps, d.tokenBudget, o.cfg.FullWorkers)
	}
	if report == nil {
		report = &WorkerReport{Status: "failed", Summary: "worker returned no report"}
	}
	if err != nil && report.Summary == "" {
		report.Status = "failed"
		report.Summary = err.Error()
	}
	return delegationOutcome{
		args:          d.args,
		report:        report,
		usage:         usage,
		workerSession: workerSession,
		merged:        merged,
		err:           err,
	}
}

func (o *Orchestrator) finalizeDelegations(outcomes []delegationOutcome) *delegationResult {
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
	report.Status = normalizeReportStatus(report.Status)
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
	if report.Status == "needs_input" {
		kind := "scout"
		if full {
			kind = "worker"
		}
		o.rememberResumable(&resumableWorker{
			Path: ws.Path(), Kind: kind, Args: a, Step: step,
			MaxSteps: maxSteps, Tokens: tokenBudget, Model: model, Goal: goal,
			Facts: append([]string(nil), facts...), Report: trimReport(report),
			Usage: usage, Session: ws, Full: full,
		})
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
	return workerHooksKind("worker", parent, title, mu)
}

// workerHooksKind tags worker events with the kind of worker that emitted
// them ("worker", "agent:<id>", …) and serialises the stream.
func workerHooksKind(kind string, parent agent.Hooks, title string, mu *sync.Mutex) agent.Hooks {
	tag := kind + ":" + oneLine(title, 40) + ":"
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
		b.WriteString("You cannot ask the user directly, and you cannot delegate further. Make reasonable assumptions, state them in risks, and prefer evidence. If a decision only the lead can make blocks the work, return status=needs_input with specific questions.\n\n")
	} else {
		b.WriteString("You are a read-only research worker for a coding agent. You cannot see the lead conversation. ")
		b.WriteString("Do not modify files, run mutating commands, or request more work. If a decision only the lead can make blocks the work, return status=needs_input with specific questions. ")
		b.WriteString("You have read, glob, grep, websearch, webfetch, and bash for non-mutating commands. ")
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

// validateFleetDAG returns the dependency edges when any task uses depends_on.
// A fleet without dependencies keeps the original all-at-once behavior.
func validateFleetDAG(tasks []delegateArgs) (map[string][]string, error) {
	any := false
	for _, task := range tasks {
		if len(task.DependsOn) > 0 {
			any = true
			break
		}
	}
	if !any {
		return nil, nil
	}
	index := make(map[string]int, len(tasks))
	deps := make(map[string][]string, len(tasks))
	for i, task := range tasks {
		id := fleetID(task)
		if id == "" {
			return nil, fmt.Errorf("task %d: set id (or step_id) when using depends_on", i+1)
		}
		if len(id) > 80 {
			return nil, fmt.Errorf("task %d: id is too long (max 80 chars)", i+1)
		}
		if prev, ok := index[id]; ok {
			return nil, fmt.Errorf("task %d: duplicate id %q (also task %d)", i+1, id, prev+1)
		}
		index[id] = i
		deps[id] = append([]string(nil), task.DependsOn...)
	}
	for id, edges := range deps {
		if len(edges) > maxFleetTasks {
			return nil, fmt.Errorf("task %q has too many dependencies (max %d)", id, maxFleetTasks)
		}
		for _, dep := range edges {
			if _, ok := index[dep]; !ok {
				return nil, fmt.Errorf("task %q depends on unknown fleet id %q", id, dep)
			}
		}
	}
	state := make(map[string]int, len(tasks))
	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("dependency cycle involving %q", id)
		case 2:
			return nil
		}
		state[id] = 1
		for _, dep := range deps[id] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for id := range deps {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return deps, nil
}

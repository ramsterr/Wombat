package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/agents"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

// teamMember looks up a selected custom agent by id.
func (o *Orchestrator) teamMember(id string) *agents.Member {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.teamMemberLocked(id)
}

// teamMemberLocked is teamMember for callers already holding o.mu.
func (o *Orchestrator) teamMemberLocked(id string) *agents.Member {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	for _, m := range o.team {
		if m != nil && strings.EqualFold(m.ID, id) {
			return m
		}
	}
	return nil
}

// teamIDs lists the selected custom agent ids in selection order.
func (o *Orchestrator) teamIDs() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	ids := make([]string, 0, len(o.team))
	for _, m := range o.team {
		if m != nil {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

// teamPromptSection tells the planner who is on the team and how to split
// work between them and itself.
func (o *Orchestrator) teamPromptSection() string {
	o.mu.Lock()
	team := append([]*agents.Member(nil), o.team...)
	maxParallel := o.cfg.MaxParallelAgents
	o.mu.Unlock()
	if len(team) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("### Your team\n")
	b.WriteString("The user selected these specialist agents for this session. Each has its own system prompt, tools, permissions, and model; each runs in its own session and cannot see this conversation.\n")
	for _, m := range team {
		if m == nil || m.Def == nil {
			continue
		}
		desc := oneLine(m.Def.Description, 240)
		if desc == "" {
			desc = "no description"
		}
		fmt.Fprintf(&b, "- %s — %s", m.ID, desc)
		var tags []string
		if m.Model != "" {
			tags = append(tags, "model "+m.Model)
		}
		if m.ReadOnly {
			tags = append(tags, "read-only")
		}
		if len(m.Def.Skills) > 0 {
			tags = append(tags, "skills "+strings.Join(m.Def.Skills, ", "))
		}
		if len(m.Def.MCPServers) > 0 {
			tags = append(tags, "mcp "+strings.Join(m.Def.MCPServers, ", "))
		}
		if len(tags) > 0 {
			fmt.Fprintf(&b, " (%s)", strings.Join(tags, "; "))
		}
		b.WriteString("\n")
	}
	b.WriteString("\nTeam rules:\n")
	b.WriteString("- Assign a step to a specialist by setting its \"agent\" to a member id in update_plan. Leave agent empty to do that step yourself with your normal workers.\n")
	b.WriteString("- Match each step to the member description. Keep synthesis, final artifacts, repository edits, and verification with you unless the step's deliverable is explicitly the member's artifact.\n")
	b.WriteString("- Dependencies are the only ordering. Put independent steps in the same wave with no depends_on so they run at the same time; a dependent step starts when its upstream step completes and receives its report.\n")
	b.WriteString("- For team work, prefer update_plan followed by run_plan over ad-hoc delegation: it makes dependencies and parallelism explicit and records each report on the ledger.\n")
	b.WriteString("- An assigned step runs with that agent's own tools, prompt, permissions, and model; kind=write worktrees apply only to your generic writers. When two assigned steps may create files, list distinct `files` on each so overlapping work is serialized.\n")
	b.WriteString("- Example: \"research topic X and design a deck\" becomes research (agent: math-research) → design (agent: designer, depends_on: [research]) → you assemble the deck from both reports.\n")
	fmt.Fprintf(&b, "- At most %d custom agents run at once; ready assigned steps run in parallel.\n", maxParallel)
	return strings.TrimSpace(b.String())
}

// teamWorkerPrompt composes the system prompt of one custom-agent worker.
func teamWorkerPrompt(m *agents.Member, a delegateArgs, step *Step, goal string, facts []string) string {
	var b strings.Builder
	if m != nil && strings.TrimSpace(m.Persona) != "" {
		b.WriteString(strings.TrimSpace(m.Persona))
		b.WriteString("\n\n")
	}
	b.WriteString("## Team assignment\n")
	b.WriteString("You are a specialist on a multi-agent team. Work the assigned step directly; you cannot delegate, you cannot ask the user, and you cannot see the lead conversation. ")
	if m != nil && m.ReadOnly {
		b.WriteString("You are read-only: do not modify files or run mutating commands. Put your findings and recommendations in the report.\n\n")
	} else {
		b.WriteString("You may create and edit files in the workspace, but other agents may be working in parallel: touch only the files this step names and report every path you change.\n\n")
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
	b.WriteString("\nCall submit_report once with your final structured report. If you cannot call tools, return only a JSON object with this shape:\n")
	b.WriteString(`{"status":"completed|partial|blocked|needs_input|failed","summary":"...","findings":[{"claim":"...","evidence":"...","path":"...","lines":"..."}],"changes":[{"path":"...","summary":"..."}],"tests":[],"risks":[],"next_steps":[],"questions":[]}`)
	b.WriteString("\n\nRules for the report:\n")
	b.WriteString("- summary: what you established, in at most 6 sentences.\n")
	b.WriteString("- findings: only claims backed by evidence; cite paths, lines, or sources.\n")
	if m != nil && m.ReadOnly {
		b.WriteString("- changes: leave empty; you are read-only.\n")
	} else {
		b.WriteString("- changes: list every file you created or modified.\n")
	}
	b.WriteString("- status=completed only if you answered the objective; use partial or blocked otherwise.\n")
	if a.Deliverable != "" {
		b.WriteString("\nRequired deliverable: ")
		b.WriteString(oneLine(a.Deliverable, 1000))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// upstreamContext renders the reports of a step's completed dependencies so
// a dependent worker starts from its team's findings instead of rediscovering
// them.
func (o *Orchestrator) upstreamContext(step *Step) string {
	if step == nil || len(step.DependsOn) == 0 {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	var b strings.Builder
	for _, dep := range step.DependsOn {
		d := o.ledger.Find(dep)
		if d == nil || d.Report == nil || strings.TrimSpace(d.Report.Summary) == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("## Upstream results\n")
		}
		fmt.Fprintf(&b, "- [%s] %s: %s\n", dep, oneLine(d.Title, 120), oneLine(d.Report.Summary, 1200))
		for _, finding := range d.Report.Findings {
			fmt.Fprintf(&b, "  - %s (%s)\n", oneLine(finding.Claim, 400), oneLine(finding.Evidence, 300))
		}
		for _, change := range d.Report.Changes {
			fmt.Fprintf(&b, "  - changed %s: %s\n", change.Path, oneLine(change.Summary, 200))
		}
	}
	return strings.TrimSpace(b.String())
}

// runTeamWorker runs one step with a custom agent. It mirrors runWorker but
// uses the member's persona, toolset, permissions, provider, and model. The
// agent works in the session workspace (no worktree) so its artifacts are
// visible to the lead and to dependent steps.
func (o *Orchestrator) runTeamWorker(ctx context.Context, a delegateArgs, m *agents.Member, step *Step, goal string, facts []string, maxSteps, tokenBudget int) (*WorkerReport, ReportUsage, string, error) {
	if m == nil || m.Def == nil {
		return nil, ReportUsage{}, "", fmt.Errorf("custom agent is unavailable")
	}
	o.mu.Lock()
	cfg := o.cfg
	lead := o.lead
	o.mu.Unlock()
	if lead == nil || lead.Env == nil {
		return nil, ReportUsage{}, "", fmt.Errorf("lead agent is unavailable")
	}
	provider := m.Provider
	if provider == nil {
		provider = lead.Provider
	}
	if provider == nil {
		return nil, ReportUsage{}, "", fmt.Errorf("custom agent %q has no provider", m.ID)
	}
	model := m.Model
	if model == "" {
		model = lead.Model
	}
	// The definition's budget wins; otherwise use the caller's default.
	if m.MaxSteps > 0 {
		maxSteps = m.MaxSteps
	}
	if maxSteps <= 0 {
		maxSteps = cfg.WorkerMaxSteps
	}
	if maxSteps > maxWorkerStepsCap {
		maxSteps = maxWorkerStepsCap
	}

	ws, err := session.CreateAt(cfg.WorkerHome, lead.Env.Dir, provider.Name(), model,
		"agent: "+m.ID+": "+oneLine(a.Title, 60))
	if err != nil {
		return nil, ReportUsage{}, "", fmt.Errorf("create agent session: %w", err)
	}

	env := *lead.Env
	env.ReadOnly = m.ReadOnly
	env.Permit = agents.WorkerPermit(m.PermRules, m.ID)
	env.PermitPreview = nil
	env.AskUser = nil
	env.Checkpoint = nil
	env.Isolate()
	defer env.CloseJobs()
	defer env.CloseLSP()
	defer env.CloseBrowser()
	defer env.ClosePreview()

	runCtx, cancel := context.WithTimeoutCause(ctx, cfg.WorkerTimeout,
		fmt.Errorf("agent %s timed out after %s", m.ID, cfg.WorkerTimeout))
	defer cancel()
	budgetCtx, cancelBudget := context.WithCancelCause(runCtx)
	defer cancelBudget(nil)
	budget := &workerTokenBudget{limit: tokenBudget}
	hooks := workerHooksKind("agent:"+m.ID, lead.Hooks, a.Title, &o.hooksMu)
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
	teamTools := append(append([]tools.Tool{}, m.Tools...), &submitReportTool{sink: sink})
	worker := &agent.Agent{
		Provider:      provider,
		Model:         model,
		Tools:         teamTools,
		Env:           &env,
		Sess:          ws,
		MaxSteps:      maxSteps,
		Compact:       lead.Compact && lead.ContextWindow > 0,
		ContextWindow: lead.ContextWindow,
		NumCtx:        lead.NumCtx,
		SystemExtra: func() string {
			return teamWorkerPrompt(m, a, step, goal, facts)
		},
		Hooks: hooks,
	}

	brief := workerBrief(a, goal, facts)
	runErr := worker.Run(budgetCtx, brief)
	usage := usageFrom(worker.Usage())
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
		runErr = fmt.Errorf("agent %s exceeded token budget (%d/%d tokens)", m.ID, budget.used, budget.limit)
		report.Status = "failed"
	} else if runCtx.Err() == context.DeadlineExceeded {
		runErr = fmt.Errorf("agent %s timed out after %s", m.ID, cfg.WorkerTimeout)
	}
	if runErr != nil {
		if report.Status != "completed" {
			report.Status = "failed"
		}
		if report.Summary == "" || report.Status == "failed" {
			report.Summary = "agent stopped: " + runErr.Error()
		}
	}
	if report.Status == "needs_input" {
		o.rememberResumable(&resumableWorker{
			Path: ws.Path(), Kind: "custom", Args: a, Step: step, Member: m,
			MaxSteps: maxSteps, Tokens: tokenBudget, Model: model, Goal: goal,
			Facts: append([]string(nil), facts...), Report: trimReport(report),
			Usage: usage, Session: ws, Full: !m.ReadOnly,
		})
	}
	return trimReport(report), usage, ws.Path(), runErr
}

// resumeCustomAgent continues a custom agent that asked for a decision.
func (o *Orchestrator) resumeCustomAgent(ctx context.Context, rec *resumableWorker, answer string) (string, error) {
	if rec.Member == nil || rec.Session == nil {
		return "", fmt.Errorf("custom agent resume state is incomplete")
	}
	o.mu.Lock()
	cfg := o.cfg
	lead := o.lead
	o.mu.Unlock()
	provider := rec.Member.Provider
	if provider == nil {
		provider = lead.Provider
	}
	model := rec.Model
	if model == "" {
		model = rec.Member.Model
	}
	if model == "" {
		model = lead.Model
	}
	env := *lead.Env
	env.ReadOnly = rec.Member.ReadOnly
	env.Permit = agents.WorkerPermit(rec.Member.PermRules, rec.Member.ID)
	env.PermitPreview = nil
	env.AskUser = nil
	env.Checkpoint = nil
	env.Isolate()
	defer env.CloseJobs()
	defer env.CloseLSP()
	defer env.CloseBrowser()
	defer env.ClosePreview()

	remaining := remainingTokens(rec.Tokens, rec.Usage)
	if rec.Tokens > 0 && remaining <= 0 {
		return "", fmt.Errorf("agent token budget exhausted (%d tokens)", rec.Tokens)
	}
	runCtx, cancel := context.WithTimeoutCause(ctx, cfg.WorkerTimeout,
		fmt.Errorf("agent %s timed out after %s", rec.Member.ID, cfg.WorkerTimeout))
	defer cancel()
	budgetCtx, cancelBudget := context.WithCancelCause(runCtx)
	defer cancelBudget(nil)
	budget := &workerTokenBudget{limit: remaining}
	hooks := workerHooksKind("agent:"+rec.Member.ID, lead.Hooks, rec.Args.Title, &o.hooksMu)
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
	teamTools := append(append([]tools.Tool{}, rec.Member.Tools...), &submitReportTool{sink: sink})
	worker := &agent.Agent{
		Provider:      provider,
		Model:         model,
		Tools:         teamTools,
		Env:           &env,
		Sess:          rec.Session,
		MaxSteps:      rec.MaxSteps,
		Compact:       lead.Compact && lead.ContextWindow > 0,
		ContextWindow: lead.ContextWindow,
		NumCtx:        lead.NumCtx,
		SystemExtra: func() string {
			return teamWorkerPrompt(rec.Member, rec.Args, rec.Step, rec.Goal, rec.Facts)
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
	if budget.exceeded {
		runErr = fmt.Errorf("agent %s exceeded token budget (%d/%d tokens)", rec.Member.ID, budget.used, budget.limit)
		report.Status = "failed"
	} else if runCtx.Err() == context.DeadlineExceeded {
		runErr = fmt.Errorf("agent %s timed out after %s", rec.Member.ID, cfg.WorkerTimeout)
	}
	if runErr != nil {
		if report.Status != "completed" {
			report.Status = "failed"
		}
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

// reserveTeamBudget claims custom-agent slots and the per-turn worker token
// budget for a wave of team steps.
func (o *Orchestrator) reserveTeamBudget(n int) (int, error) {
	if n <= 0 {
		return 0, nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	cfg := o.cfg
	if left := cfg.MaxParallelAgents - o.activeAgents; n > left {
		return 0, fmt.Errorf("custom agent slots busy: need %d, only %d free", n, left)
	}
	per := 0
	if cfg.TurnWorkerTokenBudget > 0 {
		remaining := cfg.TurnWorkerTokenBudget - o.workerTokens
		if remaining <= 0 {
			return 0, fmt.Errorf("custom agent token budget exhausted for this turn (%d tokens)", cfg.TurnWorkerTokenBudget)
		}
		per = remaining / n
		if cfg.WorkerTokenBudget > 0 && per > cfg.WorkerTokenBudget {
			per = cfg.WorkerTokenBudget
		}
		if per < minWorkerTokenBudget {
			return 0, fmt.Errorf("not enough custom agent token budget for %d agents: %d each (min %d)", n, per, minWorkerTokenBudget)
		}
	}
	o.activeAgents += n
	o.delegations += n
	o.totalDelegations += n
	return per, nil
}

// releaseTeamSlots frees slots claimed by reserveTeamBudget.
func (o *Orchestrator) releaseTeamSlots(n int) {
	if n <= 0 {
		return
	}
	o.mu.Lock()
	o.activeAgents -= n
	if o.activeAgents < 0 {
		o.activeAgents = 0
	}
	o.mu.Unlock()
}

// freeTeamSlots reports how many custom agents can start right now.
func (o *Orchestrator) freeTeamSlots() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	left := o.cfg.MaxParallelAgents - o.activeAgents
	if left < 0 {
		return 0
	}
	return left
}

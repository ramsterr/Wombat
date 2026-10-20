package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

const (
	DefaultMaxDelegations = config.DefaultMaxDelegations
	DefaultWorkerMaxSteps = config.DefaultWorkerMaxSteps
	DefaultWorkerTimeout  = 5 * time.Minute
)

type Config struct {
	MaxDelegations int
	WorkerMaxSteps int
	WorkerTimeout  time.Duration
	WorkerHome     string
}

func (c Config) withDefaults() Config {
	if c.MaxDelegations <= 0 {
		c.MaxDelegations = DefaultMaxDelegations
	}
	if c.WorkerMaxSteps <= 0 {
		c.WorkerMaxSteps = DefaultWorkerMaxSteps
	}
	if c.WorkerTimeout <= 0 {
		c.WorkerTimeout = DefaultWorkerTimeout
	}
	if c.WorkerHome == "" {
		c.WorkerHome = filepath.Join(config.Home(), "workers")
	}
	return c
}

type Orchestrator struct {
	lead        *agent.Agent
	cfg         Config
	workerTools []tools.Tool

	mu               sync.Mutex
	ledger           *Ledger
	ledgerPath       string
	loadWarning      string
	delegations      int
	totalDelegations int
}

func Attach(ag *agent.Agent, cfg Config) (*Orchestrator, error) {
	if ag == nil {
		return nil, errors.New("orchestrator: lead agent is nil")
	}
	if ag.Sess == nil {
		return nil, errors.New("orchestrator: lead agent has no session")
	}
	if ag.Provider == nil {
		return nil, errors.New("orchestrator: lead agent has no provider")
	}
	if ag.Env == nil {
		return nil, errors.New("orchestrator: lead agent has no tool environment")
	}
	for _, t := range ag.Tools {
		if t.Name() == "update_plan" || t.Name() == "delegate" {
			return nil, errors.New("orchestrator: lead agent is already attached")
		}
	}
	cfg = cfg.withDefaults()
	path := LedgerPath(ag.Sess.Path())
	ledger, warning, err := LoadLedger(path)
	if err != nil && warning == "" {
		warning = err.Error()
	}
	o := &Orchestrator{
		lead:        ag,
		cfg:         cfg,
		ledger:      ledger,
		ledgerPath:  path,
		loadWarning: warning,
	}
	for _, t := range ag.Tools {
		if t.Class() == "read" {
			o.workerTools = append(o.workerTools, t)
		}
	}
	if len(o.workerTools) == 0 {
		o.workerTools = []tools.Tool{&tools.Read{}, &tools.Glob{}, &tools.Grep{}}
	}

	prevExtra := ag.SystemExtra
	ag.SystemExtra = func() string {
		var parts []string
		if prevExtra != nil {
			if s := strings.TrimSpace(prevExtra()); s != "" {
				parts = append(parts, s)
			}
		}
		parts = append(parts, o.systemPrompt())
		return strings.Join(parts, "\n\n")
	}
	prevRunStart := ag.RunStart
	ag.RunStart = func() {
		if prevRunStart != nil {
			prevRunStart()
		}
		o.beginTurn()
	}
	ag.Tools = append(ag.Tools, &updatePlanTool{o: o}, &delegateTool{o: o})
	return o, nil
}

func (o *Orchestrator) beginTurn() {
	o.mu.Lock()
	o.delegations = 0
	o.mu.Unlock()
}

func (o *Orchestrator) Delegations() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.totalDelegations
}

func (o *Orchestrator) LedgerPath() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.ledgerPath
}

func (o *Orchestrator) Ledger() LedgerView {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.ledger.View()
}

func (o *Orchestrator) systemPrompt() string {
	o.mu.Lock()
	view := o.ledger.PromptView()
	warning := o.loadWarning
	maxDelegations := o.cfg.MaxDelegations
	o.mu.Unlock()

	var b strings.Builder
	b.WriteString("## Orchestration\n")
	b.WriteString("You are the lead agent. You own the plan, all file edits, commands, and the final answer. ")
	b.WriteString("For work with three or more distinct steps, call update_plan before starting; skip it for trivial one-step requests. ")
	b.WriteString("Keep the ledger current as you learn and when the plan changes.\n\n")
	b.WriteString("### Task ledger\n")
	b.WriteString(view)
	b.WriteString("\n\n")
	b.WriteString("### Delegation\n")
	b.WriteString("Use delegate only for self-contained, read-only investigations whose exploration would otherwise flood your context ")
	b.WriteString("(for example: map a package, trace a call path, survey tests, compare implementations). ")
	b.WriteString("Do the work directly when it is small, when it requires edits, or when you already know where to look. ")
	fmt.Fprintf(&b, "You may delegate at most %d times per user turn, and every delegation must be grounded in the ledger.\n", maxDelegations)
	b.WriteString("Tie each delegation to a ledger step when one exists, and always give the worker a complete objective, relevant context, and a concrete deliverable because it cannot see this conversation. ")
	b.WriteString("Workers are read-only: they cannot edit files, run mutating commands, or delegate further. ")
	b.WriteString("Treat worker reports as evidence, not truth; verify important claims yourself before editing or answering.\n\n")
	b.WriteString("### Ledger rules\n")
	b.WriteString("- Keep one step in_progress while you are working on it.\n")
	b.WriteString("- Mark work completed only when the deliverable actually exists.\n")
	b.WriteString("- Record durable facts (paths, constraints, decisions, test results) in facts.\n")
	b.WriteString("- If a worker is blocked or fails, revise the plan instead of retrying blindly.\n")
	if warning != "" {
		b.WriteString("\nLedger warning: ")
		b.WriteString(oneLine(warning, 500))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func (o *Orchestrator) updatePlan(goal string, facts []string, steps []Step) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if strings.TrimSpace(goal) == "" {
		goal = o.ledger.Goal
	}
	old := make(map[string]Step, len(o.ledger.Steps))
	for _, s := range o.ledger.Steps {
		old[s.ID] = s
	}
	now := time.Now()
	for i := range steps {
		steps[i].ID = strings.TrimSpace(steps[i].ID)
		steps[i].Title = strings.TrimSpace(steps[i].Title)
		steps[i].Objective = strings.TrimSpace(steps[i].Objective)
		steps[i].Role = strings.TrimSpace(steps[i].Role)
		steps[i].Status = StepStatus(strings.ToLower(strings.TrimSpace(string(steps[i].Status))))
		if steps[i].Status == "" {
			steps[i].Status = StepPending
		}
		if prev, ok := old[steps[i].ID]; ok && steps[i].Report == nil {
			steps[i].Report = prev.Report
		}
		steps[i].Updated = now
	}
	if len(facts) > maxFacts {
		facts = facts[:maxFacts]
	}
	for i := range facts {
		facts[i] = oneLine(facts[i], 500)
	}
	next := &Ledger{Goal: oneLine(goal, 1000), Facts: facts, Steps: steps, Updated: now}
	if err := next.Validate(); err != nil {
		return "", err
	}
	o.ledger = next
	if err := o.ledger.Save(o.ledgerPath); err != nil {
		return o.planSummary(false, err), nil
	}
	return o.planSummary(true, nil), nil
}

func (o *Orchestrator) planSummary(saved bool, saveErr error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "plan updated: %d steps (%s)\n", len(o.ledger.Steps), sortedStatusCounts(o.ledger))
	for _, s := range o.ledger.Steps {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", s.Status, s.ID, oneLine(s.Title, 120))
	}
	if !saved && saveErr != nil {
		fmt.Fprintf(&b, "warning: could not persist ledger: %v\n", saveErr)
	}
	return strings.TrimSpace(b.String())
}

func (o *Orchestrator) delegate(ctx context.Context, a delegateArgs) (string, error) {
	if err := validateDelegationArgs(a); err != nil {
		return "", err
	}
	o.mu.Lock()
	if o.delegations >= o.cfg.MaxDelegations {
		o.mu.Unlock()
		return "", fmt.Errorf("delegation budget exhausted (%d per user turn); continue the work yourself or revise the plan", o.cfg.MaxDelegations)
	}
	o.delegations++
	goal := o.ledger.Goal
	facts := append([]string(nil), o.ledger.Facts...)
	var step *Step
	if a.StepID != "" {
		if s := o.ledger.Find(a.StepID); s != nil {
			for _, dep := range s.DependsOn {
				if d := o.ledger.Find(dep); d == nil || d.Status != StepCompleted {
					o.delegations--
					o.mu.Unlock()
					status := "missing"
					if d != nil {
						status = string(d.Status)
					}
					return "", fmt.Errorf("step %q depends on %q (%s): finish dependencies before delegating", s.ID, dep, status)
				}
			}
			copy := *s
			step = &copy
		} else {
			o.delegations--
			o.mu.Unlock()
			return "", fmt.Errorf("unknown step id %q: call update_plan first or omit step_id", a.StepID)
		}
	}
	o.totalDelegations++
	maxSteps := a.MaxSteps
	if maxSteps <= 0 {
		maxSteps = o.cfg.WorkerMaxSteps
	}
	o.mu.Unlock()

	report, usage, workerSession, err := o.runWorker(ctx, a, step, goal, facts, maxSteps)
	if report == nil {
		report = &WorkerReport{Status: "failed"}
	}
	if err != nil && report.Summary == "" {
		report.Status = "failed"
		report.Summary = err.Error()
	}
	if report.Usage.Calls == 0 && usage.Calls > 0 {
		report.Usage = usage
	}
	o.lead.AddUsageStats(agent.UsageStats{Calls: usage.Calls, In: usage.In, Cached: usage.Cached, Out: usage.Out})

	o.mu.Lock()
	if a.StepID != "" {
		if s := o.ledger.Find(a.StepID); s != nil {
			s.Status = reportStatusToStep(report.Status)
			s.Report = report
			s.Updated = time.Now()
		}
	}
	o.ledger.Updated = time.Now()
	saveErr := o.ledger.Save(o.ledgerPath)
	view := o.ledger.View()
	o.mu.Unlock()

	result := delegateResult{
		StepID:        a.StepID,
		Report:        report,
		Usage:         usage,
		WorkerSession: workerSession,
		Ledger:        view,
	}
	if err != nil {
		result.Warning = err.Error()
	}
	if saveErr != nil {
		if result.Warning != "" {
			result.Warning += "; "
		}
		result.Warning += "could not persist ledger: " + saveErr.Error()
	}
	raw, mErr := jsonMarshalIndent(result)
	if mErr != nil {
		return "", mErr
	}
	return raw, nil
}

func (o *Orchestrator) runWorker(ctx context.Context, a delegateArgs, step *Step, goal string, facts []string, maxSteps int) (*WorkerReport, ReportUsage, string, error) {
	o.mu.Lock()
	cfg := o.cfg
	o.mu.Unlock()

	ws, err := session.CreateAt(cfg.WorkerHome, o.lead.Env.Dir, o.lead.Provider.Name(), o.lead.Model, "delegate: "+oneLine(a.Title, 80))
	if err != nil {
		return nil, ReportUsage{}, "", fmt.Errorf("create worker session: %w", err)
	}
	env := *o.lead.Env
	env.ReadOnly = true
	env.Permit = nil
	worker := &agent.Agent{
		Provider:      o.lead.Provider,
		Model:         o.lead.Model,
		Tools:         o.workerTools,
		Env:           &env,
		Sess:          ws,
		MaxSteps:      maxSteps,
		Compact:       o.lead.Compact && o.lead.ContextWindow > 0,
		ContextWindow: o.lead.ContextWindow,
		NumCtx:        o.lead.NumCtx,
		SystemExtra: func() string {
			return workerSystemPrompt(a, step, goal, facts)
		},
		Hooks: workerHooks(o.lead.Hooks, a.Title),
	}

	runCtx, cancel := context.WithTimeout(ctx, cfg.WorkerTimeout)
	defer cancel()
	runErr := worker.Run(runCtx, workerBrief(a, goal, facts))
	stats := worker.Usage()
	usage := usageFrom(stats)
	content := lastAssistantContent(ws)
	report, ok := parseReport(content)
	if !ok {
		report = fallbackReport(content)
	}
	report.Usage = usage
	if runErr != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			runErr = fmt.Errorf("worker timed out after %s", cfg.WorkerTimeout)
		}
		if report.Status != "completed" {
			report.Status = "failed"
		}
		if report.Summary == "" || report.Status == "failed" {
			report.Summary = "worker stopped: " + runErr.Error()
		}
	}
	return trimReport(report), usage, ws.Path(), runErr
}

func workerHooks(parent agent.Hooks, title string) agent.Hooks {
	tag := "worker:" + oneLine(title, 40) + ":"
	return agent.Hooks{
		OnTool: func(name, detail string) {
			if parent.OnTool != nil {
				parent.OnTool("worker:"+name, tag+" "+detail)
			}
		},
		OnToolResult: func(name, result string, dur time.Duration) {
			if parent.OnToolResult != nil {
				parent.OnToolResult("worker:"+name, result, dur)
			}
		},
		OnUsage: func(u llm.Usage, history int) {
			if parent.OnUsage != nil {
				parent.OnUsage(u, -1)
			}
		},
		OnCompact: func(kind string, before, after int) {
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
	var b strings.Builder
	b.WriteString("## Delegated worker\n")
	b.WriteString("You are a read-only research worker for a coding agent. You cannot see the lead conversation. ")
	b.WriteString("Do not modify files, run mutating commands, ask the user questions, or request more work. ")
	b.WriteString("Use read/glob/grep to inspect the repository. Stay inside the working directory unless the brief says otherwise. ")
	b.WriteString("Make reasonable assumptions when context is missing, state them in risks, and prefer evidence over speculation.\n\n")
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
	b.WriteString("\nReturn only a JSON object as your final answer, with this shape:\n")
	b.WriteString(`{"status":"completed|partial|blocked|failed","summary":"...","findings":[{"claim":"...","evidence":"...","path":"...","lines":"..."}],"changes":[],"tests":[],"risks":[],"next_steps":[]}`)
	b.WriteString("\n\nRules for the report:\n")
	b.WriteString("- summary: what you established, in at most 6 sentences.\n")
	b.WriteString("- findings: only claims backed by evidence; use path:line references when possible.\n")
	b.WriteString("- changes: leave empty; you are read-only.\n")
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

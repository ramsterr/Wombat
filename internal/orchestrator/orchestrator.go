package orchestrator

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/tools"
	"github.com/ramsterr/Wombat/internal/worktree"
)

const (
	DefaultMaxDelegations        = config.DefaultMaxDelegations
	DefaultMaxParallelScouts     = config.DefaultMaxParallelScouts
	DefaultWorkerMaxSteps        = config.DefaultWorkerMaxSteps
	DefaultWorkerTokenBudget     = config.DefaultWorkerTokenBudget
	DefaultTurnWorkerTokenBudget = config.DefaultTurnWorkerTokenBudget
	DefaultMaxParallelWriters    = config.DefaultMaxParallelWriters
	DefaultWriterMaxSteps        = config.DefaultWriterMaxSteps
	DefaultWriterTokenBudget     = config.DefaultWriterTokenBudget
	DefaultTurnWriterTokenBudget = config.DefaultTurnWriterTokenBudget
	DefaultWorkerTimeout         = 5 * time.Minute
	DefaultWriterTimeout         = 10 * time.Minute
	maxFleetTasks                = 6
	maxWorkerStepsCap            = 100
	minWorkerTokenBudget         = 1_000
)

type Config struct {
	MaxDelegations        int
	MaxParallelScouts     int
	WorkerMaxSteps        int
	WorkerTokenBudget     int
	TurnWorkerTokenBudget int
	WorkerTimeout         time.Duration
	WorkerHome            string
	// ParallelAgents makes the lead fan out workers by default.
	ParallelAgents bool
	// ScoutModel is the model the read-only workers use. Empty means the
	// lead's model. A cheaper model here is the biggest speed and cost
	// lever, because the workers do the reading.
	ScoutModel            string
	MaxParallelWriters    int
	WriterMaxSteps        int
	WriterTokenBudget     int
	TurnWriterTokenBudget int
	WriterTimeout         time.Duration
	WorktreeRoot          string
	RequireVerification   bool
	EnableWriters         bool
	KeepWorktrees         bool
}

func (c Config) withDefaults() Config {
	if c.MaxDelegations <= 0 {
		c.MaxDelegations = DefaultMaxDelegations
	}
	if c.MaxParallelScouts <= 0 {
		c.MaxParallelScouts = DefaultMaxParallelScouts
	}
	if c.WorkerMaxSteps <= 0 {
		c.WorkerMaxSteps = DefaultWorkerMaxSteps
	}
	if c.WorkerTokenBudget <= 0 {
		c.WorkerTokenBudget = DefaultWorkerTokenBudget
	}
	if c.TurnWorkerTokenBudget <= 0 {
		c.TurnWorkerTokenBudget = DefaultTurnWorkerTokenBudget
	}
	if c.WorkerTimeout <= 0 {
		c.WorkerTimeout = DefaultWorkerTimeout
	}
	if c.WorkerHome == "" {
		c.WorkerHome = filepath.Join(config.Home(), "workers")
	}
	if c.MaxParallelWriters <= 0 {
		c.MaxParallelWriters = DefaultMaxParallelWriters
	}
	if c.WriterMaxSteps <= 0 {
		c.WriterMaxSteps = DefaultWriterMaxSteps
	}
	if c.WriterTokenBudget <= 0 {
		c.WriterTokenBudget = DefaultWriterTokenBudget
	}
	if c.TurnWriterTokenBudget <= 0 {
		c.TurnWriterTokenBudget = DefaultTurnWriterTokenBudget
	}
	if c.WriterTimeout <= 0 {
		c.WriterTimeout = DefaultWriterTimeout
	}
	if c.WorktreeRoot == "" {
		c.WorktreeRoot = filepath.Join(config.Home(), "worktrees")
	}
	return c
}

type Orchestrator struct {
	lead        *agent.Agent
	cfg         Config
	workerTools []tools.Tool
	writerTools []tools.Tool
	worktrees   *worktree.Manager
	writerWarn  string

	mu               sync.Mutex
	mergeMu          sync.Mutex
	worktreeMu       sync.Mutex
	ledger           *Ledger
	ledgerPath       string
	loadWarning      string
	delegations      int
	totalDelegations int
	workerTokens     int
	writers          int
	writerTokens     int
	hooksMu          sync.Mutex
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
		if t.Name() == "update_plan" || t.Name() == "delegate" || t.Name() == "delegate_many" {
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
		// The workers do the reading and the searching, including the web.
		o.workerTools = []tools.Tool{&tools.Read{}, &tools.Glob{}, &tools.Grep{}, &tools.WebSearch{}, &tools.WebFetch{}}
	}
	for _, t := range ag.Tools {
		switch t.Name() {
		case "update_plan", "delegate", "delegate_many", "run_checks", "delegate_write", "question":
			// Workers and writers have nobody to ask.
			continue
		}
		o.writerTools = append(o.writerTools, t)
	}
	if cfg.EnableWriters {
		mgr, err := worktree.New(ag.Env.Dir, cfg.WorktreeRoot)
		if err != nil {
			o.writerWarn = err.Error()
		} else {
			o.worktrees = mgr
			ag.Tools = append(ag.Tools, &delegateWriteTool{o: o})
		}
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
	ag.Tools = append(ag.Tools, &updatePlanTool{o: o}, &delegateTool{o: o}, &delegateManyTool{o: o}, &runChecksTool{o: o})
	return o, nil
}

func (o *Orchestrator) beginTurn() {
	o.mu.Lock()
	o.delegations = 0
	o.workerTokens = 0
	o.writers = 0
	o.writerTokens = 0
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
	writerWarn := o.writerWarn
	o.mu.Unlock()

	var b strings.Builder
	b.WriteString("## Orchestration\n")
	b.WriteString("You are the lead agent: you own the plan, the edits, the commands, and the final answer. ")
	b.WriteString("For work with three or more steps, call update_plan first and keep the ledger current.\n\n")
	b.WriteString("### Task ledger\n")
	b.WriteString(view)
	b.WriteString("\n\n")
	if o.cfg.ParallelAgents {
		if n := o.independentSteps(); n >= 2 {
			fmt.Fprintf(&b, "Your plan has %d independent steps. Dispatch them as one fleet with delegate_many "+
				"(one worker per step) instead of doing them one at a time.\n\n", n)
		}
	}
	b.WriteString("### Delegation\n")
	if o.cfg.ParallelAgents {
		b.WriteString("Parallel work is the default, not the exception. For anything past two steps - research, ")
		b.WriteString("comparison, a task spanning several parts of the codebase - dispatch a fleet with delegate_many: ")
		b.WriteString("two to five independent workers at the same time, each on a different angle, then synthesize. ")
		b.WriteString("That is faster and costs you far fewer tokens than one thread at a time. ")
	}
	b.WriteString("Use delegate for a single investigation. ")
	b.WriteString("Never put dependent steps in one fleet. Do the work yourself when it is small, needs edits, or you already know where to look. ")
	fmt.Fprintf(&b, "At most %d delegations per turn and %d scouts at once; every delegation is grounded in the ledger.\n", maxDelegations, o.cfg.MaxParallelScouts)
	b.WriteString("Give each worker a complete objective, the context it cannot see from this conversation, and a concrete deliverable. ")
	b.WriteString("Workers are read-only and cannot delegate; they have hard caps on steps, tokens, and time, and a fleet shares the turn's token budget. ")
	b.WriteString("Treat their reports as evidence, not truth: reconcile them and verify what matters.\n\n")
	if o.worktrees != nil {
		b.WriteString("### Writers\n")
		b.WriteString("Use delegate_write when coding splits into independent tasks with disjoint files. ")
		b.WriteString("Each writer works in its own git worktree, must declare every file it changes, and must pass the step's checks; ")
		b.WriteString("merge is sequential and check-gated, and a failing writer is kept for inspection. ")
		fmt.Fprintf(&b, "At most %d writers at once, with non-overlapping files.\n\n", o.cfg.MaxParallelWriters)
	} else if writerWarn != "" {
		b.WriteString("### Writers\n")
		b.WriteString("Writers are unavailable: ")
		b.WriteString(oneLine(writerWarn, 300))
		b.WriteString("\n\n")
	}
	b.WriteString("### Verification\n")
	b.WriteString("Give each step one to three concrete checks in update_plan and run them with run_checks after editing. ")
	b.WriteString("A step with failing or pending checks cannot be completed: fix the cause, never delete or weaken the check.\n\n")
	b.WriteString("### Ledger rules\n")
	b.WriteString("- One in_progress step at a time; a scout fleet may have several.\n")
	b.WriteString("- Complete a step only when the deliverable exists and its checks pass.\n")
	b.WriteString("- Record durable facts (paths, constraints, decisions, test results).\n")
	b.WriteString("- If a worker fails, revise the plan instead of retrying blindly.\n")
	if warning != "" {
		b.WriteString("\nLedger warning: ")
		b.WriteString(oneLine(warning, 500))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// independentSteps counts the ledger steps that do not depend on each other.
func (o *Orchestrator) independentSteps() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.ledger == nil {
		return 0
	}
	n := 0
	for _, s := range o.ledger.Steps {
		if s.Status == StepCompleted || s.Status == StepFailed {
			continue
		}
		if len(s.DependsOn) == 0 {
			n++
		}
	}
	return n
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
		if prev, ok := old[steps[i].ID]; ok {
			if steps[i].Report == nil {
				steps[i].Report = prev.Report
			}
			if steps[i].Checks == nil {
				steps[i].Checks = prev.Checks
			} else {
				steps[i].Checks = mergeChecks(prev.Checks, steps[i].Checks)
			}
		}
		steps[i].Updated = now
	}
	for i := range steps {
		if steps[i].Checks != nil {
			steps[i].Checks = normalizeChecks(steps[i].Checks)
		}
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
	if o.cfg.RequireVerification {
		if err := completionGate(next); err != nil {
			return "", err
		}
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

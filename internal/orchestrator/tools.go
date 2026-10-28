package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ramsterr/Wombat/internal/tools"
)

type updatePlanTool struct{ o *Orchestrator }

type updatePlanArgs struct {
	Goal  string     `json:"goal"`
	Facts []string   `json:"facts"`
	Steps []planStep `json:"steps"`
}

type planStep struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Objective   string      `json:"objective"`
	Role        string      `json:"role"`
	Kind        string      `json:"kind"`
	Agent       string      `json:"agent,omitempty"`
	Files       []string    `json:"files"`
	Deliverable string      `json:"deliverable"`
	Status      string      `json:"status"`
	DependsOn   []string    `json:"depends_on"`
	Checks      []planCheck `json:"checks"`
}

type planCheck struct {
	ID             string `json:"id"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

func (t *updatePlanTool) Name() string  { return "update_plan" }
func (t *updatePlanTool) Class() string { return "plan" }
func (t *updatePlanTool) Description() string {
	return "Create or replace the task ledger: the goal, durable facts, and ordered steps. Use for multi-step work, before delegating, and whenever the plan materially changes. Keep sequential work focused on one in_progress step; a parallel scout fleet may have several. Steps are ordered but dependencies are explicit. Set a step's agent to a selected custom agent id to have that specialist run the step; leave it empty to do the work yourself."
}
func (t *updatePlanTool) Schema() map[string]any {
	agentDesc := "optional custom agent id to run this step with that agent's own prompt, tools, and permissions; leave empty to do the step yourself with your workers"
	if names := t.o.teamIDs(); len(names) > 0 {
		agentDesc += " (selected agents: " + strings.Join(names, ", ") + ")"
	}
	stepSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          map[string]any{"type": "string", "description": "short stable id, e.g. survey or fix-parser"},
			"title":       map[string]any{"type": "string", "description": "one-line task title"},
			"objective":   map[string]any{"type": "string", "description": "concrete outcome and scope"},
			"role":        map[string]any{"type": "string", "description": "optional role, e.g. explorer, implementer, verifier"},
			"kind":        map[string]any{"type": "string", "enum": []string{"scout", "write"}, "description": "scout (read-only research, default) or write (owned files in a worktree, then review and merge)"},
			"agent":       map[string]any{"type": "string", "description": agentDesc},
			"files":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "for kind=write: every repo-relative file the writer may change; tasks with overlapping files cannot run together"},
			"deliverable": map[string]any{"type": "string", "description": "exact output expected from a scout or writer"},
			"status":      map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "blocked", "failed"}},
			"depends_on":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"checks": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":              map[string]any{"type": "string", "description": "short stable id, e.g. unit"},
						"command":         map[string]any{"type": "string", "description": "exact command that proves the step, e.g. go test ./..."},
						"timeout_seconds": map[string]any{"type": "integer", "description": "optional per-check timeout (default 120s)"},
					},
					"required": []string{"id", "command"},
				},
				"description": "commands that must pass before this step can be completed (max 12)",
			},
		},
		"required": []string{"id", "title"},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"goal":  map[string]any{"type": "string", "description": "the user-visible goal of this work (omit to keep the current goal)"},
			"facts": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "durable facts discovered so far: paths, constraints, decisions, test results"},
			"steps": map[string]any{"type": "array", "items": stepSchema, "description": "ordered task steps (max 12)"},
		},
		"required": []string{"steps"},
	}
}

func (t *updatePlanTool) Run(_ context.Context, raw json.RawMessage, _ *tools.Env) (string, error) {
	var a updatePlanArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if len(a.Steps) == 0 {
		return "", fmt.Errorf("steps cannot be empty")
	}
	steps := make([]Step, 0, len(a.Steps))
	for _, s := range a.Steps {
		step := Step{
			ID:          s.ID,
			Title:       s.Title,
			Objective:   s.Objective,
			Role:        s.Role,
			Kind:        s.Kind,
			Agent:       s.Agent,
			Files:       s.Files,
			Deliverable: s.Deliverable,
			Status:      StepStatus(s.Status),
			DependsOn:   s.DependsOn,
		}
		if s.Checks != nil {
			step.Checks = make([]Check, 0, len(s.Checks))
			for _, c := range s.Checks {
				step.Checks = append(step.Checks, Check{
					ID:             c.ID,
					Command:        c.Command,
					TimeoutSeconds: c.TimeoutSeconds,
					Status:         CheckPending,
				})
			}
		}
		steps = append(steps, step)
	}
	return t.o.updatePlan(a.Goal, a.Facts, steps)
}

type delegateTool struct{ o *Orchestrator }

type delegateArgs struct {
	ID          string   `json:"id,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	StepID      string   `json:"step_id"`
	Title       string   `json:"title"`
	Objective   string   `json:"objective"`
	Context     string   `json:"context"`
	Role        string   `json:"role"`
	Deliverable string   `json:"deliverable"`
	Files       []string `json:"files,omitempty"`
	MaxSteps    int      `json:"max_steps"`
	// Agent routes the task to one of the selected custom agents instead of
	// a generic worker.
	Agent string `json:"agent,omitempty"`
}

func (t *delegateTool) Name() string  { return "delegate" }
func (t *delegateTool) Class() string { return "delegate" }
func (t *delegateTool) Description() string {
	return "Delegate one self-contained task to a fresh worker with its own context. " +
		"Use when exploration would flood your context. For two or more independent angles, use delegate_many - that is " +
		"the default for multi-step work. Do the work directly when it is small. " +
		"Workers are full-access by default: they can read, write, edit, run commands, use the network, and use every non-orchestration tool in an isolated worktree; changes are reviewed and merged. They cannot delegate further. " +
		"Set agent to a selected custom agent id to run the task with that specialist instead. Returns a structured JSON report with evidence and usage."
}
func (t *delegateTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"step_id":     map[string]any{"type": "string", "description": "ledger step this work belongs to (recommended; must exist)"},
			"title":       map[string]any{"type": "string", "description": "short delegation title"},
			"objective":   map[string]any{"type": "string", "description": "the concrete question to answer or investigation to perform"},
			"context":     map[string]any{"type": "string", "description": "everything the worker needs that it cannot infer from the repo: paths, hypotheses, constraints, prior findings"},
			"role":        map[string]any{"type": "string", "description": "optional role, e.g. explorer or test-surveyor"},
			"deliverable": map[string]any{"type": "string", "description": "exact output expected, including which claims need evidence"},
			"files":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "optional repo-relative file ownership; omit to let a full-access worker edit anywhere in its worktree"},
			"max_steps":   map[string]any{"type": "integer", "description": "optional worker step budget (default from config, max 100)"},
			"agent":       map[string]any{"type": "string", "description": t.o.delegateAgentDescription()},
		},
		"required": []string{"title", "objective"},
	}
}

func (t *delegateTool) Run(ctx context.Context, raw json.RawMessage, _ *tools.Env) (string, error) {
	var a delegateArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	return t.o.delegate(ctx, a)
}

type delegateManyTool struct{ o *Orchestrator }

type delegateManyArgs struct {
	Tasks []delegateArgs `json:"tasks"`
}

func (t *delegateManyTool) Name() string  { return "delegate_many" }
func (t *delegateManyTool) Class() string { return "delegate" }
func (t *delegateManyTool) Description() string {
	return "Run two to six full-access workers, each with its own context, session, and isolated worktree. " +
		"USE THIS BY DEFAULT: for any task with more than two steps, for research, for comparison, or for anything that " +
		"spans several parts of the codebase, dispatch a fleet with one call instead of exploring one thread at " +
		"a time. Give each worker a distinct angle and a self-contained brief. Add id and depends_on to build a DAG: " +
		"a dependent worker starts after its upstream tasks and receives their reports in its context. " +
		"Workers can read, write, edit, run commands, use the network, and use every non-orchestration tool; changes are reviewed and merged, and tasks with no changes still complete. Returns one structured JSON report per worker plus total usage; reconcile conflicting reports yourself."
}
func (t *delegateManyTool) Schema() map[string]any {
	taskSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          map[string]any{"type": "string", "description": "fleet id, required when any task uses depends_on (defaults to step_id)"},
			"depends_on":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "fleet ids that must finish first; their reports are added to this task's context"},
			"step_id":     map[string]any{"type": "string", "description": "ledger step this worker belongs to (recommended; must exist)"},
			"title":       map[string]any{"type": "string", "description": "short worker title"},
			"objective":   map[string]any{"type": "string", "description": "the concrete question or task this worker must handle"},
			"context":     map[string]any{"type": "string", "description": "self-contained context the worker cannot infer from the repo"},
			"role":        map[string]any{"type": "string", "description": "optional role, e.g. explorer or test-surveyor"},
			"deliverable": map[string]any{"type": "string", "description": "exact output expected, including evidence requirements"},
			"files":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "optional repo-relative ownership, for workers that may write"},
			"max_steps":   map[string]any{"type": "integer", "description": "optional worker step budget (default from config, max 100)"},
			"agent":       map[string]any{"type": "string", "description": t.o.delegateAgentDescription()},
		},
		"required": []string{"title", "objective"},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tasks": map[string]any{
				"type":        "array",
				"description": "workers to run (2-6); omit depends_on for independent parallel work",
				"minItems":    2,
				"maxItems":    maxFleetTasks,
				"items":       taskSchema,
			},
		},
		"required": []string{"tasks"},
	}
}

func (t *delegateManyTool) Run(ctx context.Context, raw json.RawMessage, _ *tools.Env) (string, error) {
	var a delegateManyArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	return t.o.delegateMany(ctx, a.Tasks)
}

// delegateAgentDescription names the team for the direct-delegation tools.
func (o *Orchestrator) delegateAgentDescription() string {
	desc := "optional custom agent id; leave empty for a generic worker"
	if names := o.teamIDs(); len(names) > 0 {
		desc += " (selected agents: " + strings.Join(names, ", ") + ")"
	}
	return desc
}

func jsonMarshalIndent(v any) (string, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

type delegateResult struct {
	StepID        string        `json:"step_id,omitempty"`
	Report        *WorkerReport `json:"report"`
	Usage         ReportUsage   `json:"usage"`
	WorkerSession string        `json:"worker_session,omitempty"`
	Worktree      string        `json:"worktree,omitempty"`
	Ledger        LedgerView    `json:"ledger"`
	Merged        bool          `json:"merged,omitempty"`
	Warning       string        `json:"warning,omitempty"`
}

type fleetResult struct {
	Tasks   []delegateOutcome `json:"tasks"`
	Usage   ReportUsage       `json:"usage"`
	Ledger  LedgerView        `json:"ledger"`
	Warning string            `json:"warning,omitempty"`
}

type delegateOutcome struct {
	StepID        string        `json:"step_id,omitempty"`
	Title         string        `json:"title"`
	Report        *WorkerReport `json:"report"`
	Usage         ReportUsage   `json:"usage"`
	WorkerSession string        `json:"worker_session,omitempty"`
	Warning       string        `json:"warning,omitempty"`
}

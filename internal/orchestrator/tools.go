package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ramsterr/Wombat/internal/tools"
)

type updatePlanTool struct{ o *Orchestrator }

type updatePlanArgs struct {
	Goal  string     `json:"goal"`
	Facts []string   `json:"facts"`
	Steps []planStep `json:"steps"`
}

type planStep struct {
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	Objective string      `json:"objective"`
	Role      string      `json:"role"`
	Status    string      `json:"status"`
	DependsOn []string    `json:"depends_on"`
	Checks    []planCheck `json:"checks"`
}

type planCheck struct {
	ID             string `json:"id"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

func (t *updatePlanTool) Name() string  { return "update_plan" }
func (t *updatePlanTool) Class() string { return "plan" }
func (t *updatePlanTool) Description() string {
	return "Create or replace the task ledger: the goal, durable facts, and ordered steps. Use for multi-step work, before delegating, and whenever the plan materially changes. Keep sequential work focused on one in_progress step; a parallel scout fleet may have several. Steps are ordered but dependencies are explicit."
}
func (t *updatePlanTool) Schema() map[string]any {
	stepSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":         map[string]any{"type": "string", "description": "short stable id, e.g. survey or fix-parser"},
			"title":      map[string]any{"type": "string", "description": "one-line task title"},
			"objective":  map[string]any{"type": "string", "description": "concrete outcome and scope"},
			"role":       map[string]any{"type": "string", "description": "optional role, e.g. explorer, implementer, verifier"},
			"status":     map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "blocked", "failed"}},
			"depends_on": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"checks": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":              map[string]any{"type": "string", "description": "short stable id, e.g. unit"},
						"command":         map[string]any{"type": "string", "description": "exact command that proves the step, e.g. go test ./..."},
						"timeout_seconds": map[string]any{"type": "integer", "description": "optional per-check timeout; omit for no timeout"},
					},
					"required": []string{"id", "command"},
				},
				"description": "commands that prove this step",
			},
		},
		"required": []string{"id", "title"},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"goal":  map[string]any{"type": "string", "description": "the user-visible goal of this work (omit to keep the current goal)"},
			"facts": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "durable facts discovered so far: paths, constraints, decisions, test results"},
			"steps": map[string]any{"type": "array", "items": stepSchema, "description": "ordered task steps"},
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
			ID:        s.ID,
			Title:     s.Title,
			Objective: s.Objective,
			Role:      s.Role,
			Status:    StepStatus(s.Status),
			DependsOn: s.DependsOn,
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
	StepID      string `json:"step_id"`
	Title       string `json:"title"`
	Objective   string `json:"objective"`
	Context     string `json:"context"`
	Role        string `json:"role"`
	Deliverable string `json:"deliverable"`
}

func (t *delegateTool) Name() string  { return "delegate" }
func (t *delegateTool) Class() string { return "delegate" }
func (t *delegateTool) Description() string {
	return "Delegate one self-contained, read-only investigation to a fresh worker with its own context. " +
		"Use when exploration would flood your context. For two or more independent angles, use delegate_many - that is " +
		"the default for multi-step work. Do the work directly when it is small or requires edits. " +
		"Workers cannot edit files, run mutating commands, or delegate. Returns a structured JSON report with evidence and usage."
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
	return "Run two to five independent, read-only workers concurrently, each with its own context and session. " +
		"USE THIS BY DEFAULT: for any task with more than two steps, for research, for comparison, or for anything that " +
		"spans several parts of the codebase, dispatch a fleet of workers with one call instead of exploring one thread at " +
		"a time. It is faster and keeps your context small. Give each worker a distinct angle and a self-contained brief. " +
		"Do not use it for dependent steps, edits, or work you can do directly. Returns one structured JSON report per " +
		"worker plus total usage; reconcile conflicting reports yourself."
}
func (t *delegateManyTool) Schema() map[string]any {
	taskSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"step_id":     map[string]any{"type": "string", "description": "ledger step this scout belongs to (recommended; must exist and be unique in the fleet)"},
			"title":       map[string]any{"type": "string", "description": "short scout title"},
			"objective":   map[string]any{"type": "string", "description": "the concrete question this scout must answer"},
			"context":     map[string]any{"type": "string", "description": "self-contained context the scout cannot infer from the repo"},
			"role":        map[string]any{"type": "string", "description": "optional role, e.g. explorer or test-surveyor"},
			"deliverable": map[string]any{"type": "string", "description": "exact output expected, including evidence requirements"},
		},
		"required": []string{"title", "objective"},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tasks": map[string]any{
				"type":        "array",
				"description": "independent read-only investigations to run concurrently (2-6)",
				"minItems":    2,
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
	Ledger        LedgerView    `json:"ledger"`
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

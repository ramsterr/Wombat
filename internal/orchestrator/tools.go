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
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Objective string   `json:"objective"`
	Role      string   `json:"role"`
	Status    string   `json:"status"`
	DependsOn []string `json:"depends_on"`
}

func (t *updatePlanTool) Name() string  { return "update_plan" }
func (t *updatePlanTool) Class() string { return "plan" }
func (t *updatePlanTool) Description() string {
	return "Create or replace the task ledger: the goal, durable facts, and ordered steps. Use for multi-step work, before delegating, and whenever the plan materially changes. Keep exactly one step in_progress while working. Steps are ordered but dependencies are explicit."
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
		steps = append(steps, Step{
			ID:        s.ID,
			Title:     s.Title,
			Objective: s.Objective,
			Role:      s.Role,
			Status:    StepStatus(s.Status),
			DependsOn: s.DependsOn,
		})
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
	MaxSteps    int    `json:"max_steps"`
}

func (t *delegateTool) Name() string  { return "delegate" }
func (t *delegateTool) Class() string { return "delegate" }
func (t *delegateTool) Description() string {
	return "Delegate one self-contained, read-only investigation to a fresh worker with its own context. Use only when exploration would flood the lead context; do the work directly when it is small or requires edits. Workers cannot edit files, run mutating commands, or delegate. Returns a structured JSON report with evidence and usage."
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
			"max_steps":   map[string]any{"type": "integer", "description": "optional worker step budget (default from config, max 100)"},
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

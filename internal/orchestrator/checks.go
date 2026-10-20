package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/tools"
)

type runChecksTool struct{ o *Orchestrator }

type runChecksArgs struct {
	StepID         string `json:"step_id"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

func (t *runChecksTool) Name() string  { return "run_checks" }
func (t *runChecksTool) Class() string { return "verify" }
func (t *runChecksTool) Description() string {
	return "Run the ledger checks for one step in the workspace and record pass/fail evidence. Use after edits and before marking a step completed. Checks run with the same permissions as bash."
}
func (t *runChecksTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"step_id":         map[string]any{"type": "string", "description": "ledger step whose checks should run"},
			"timeout_seconds": map[string]any{"type": "integer", "description": "optional per-check timeout in seconds; omit for no timeout"},
		},
		"required": []string{"step_id"},
	}
}

func (t *runChecksTool) Run(ctx context.Context, raw json.RawMessage, _ *tools.Env) (string, error) {
	var a runChecksArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	return t.o.runChecks(ctx, a)
}

type checkOutcome struct {
	ID       string      `json:"id"`
	Command  string      `json:"command"`
	Status   CheckStatus `json:"status"`
	ExitCode int         `json:"exit_code"`
	Output   string      `json:"output,omitempty"`
}

type checkRunResult struct {
	StepID  string         `json:"step_id"`
	Passed  bool           `json:"passed"`
	Checks  []checkOutcome `json:"checks"`
	Ledger  LedgerView     `json:"ledger"`
	Warning string         `json:"warning,omitempty"`
}

func (o *Orchestrator) runChecks(ctx context.Context, a runChecksArgs) (string, error) {
	o.mu.Lock()
	step := o.ledger.Find(a.StepID)
	if step == nil {
		o.mu.Unlock()
		return "", fmt.Errorf("unknown step id %q", a.StepID)
	}
	checks := append([]Check(nil), step.Checks...)
	env := *o.lead.Env
	o.mu.Unlock()
	if len(checks) == 0 {
		return "", fmt.Errorf("step %q has no checks; add checks in update_plan first", a.StepID)
	}
	defaultTimeout := time.Duration(a.TimeoutSeconds) * time.Second
	result := checkRunResult{StepID: a.StepID, Passed: true}
	updates := make(map[string]Check, len(checks))
	for _, c := range checks {
		timeout := defaultTimeout
		if c.TimeoutSeconds > 0 {
			timeout = time.Duration(c.TimeoutSeconds) * time.Second
		}
		if err := env.Check("bash", c.Command); err != nil {
			result.Passed = false
			out := checkOutcome{ID: c.ID, Command: c.Command, Status: CheckFailed, Output: err.Error()}
			result.Checks = append(result.Checks, out)
			updates[c.ID] = Check{ID: c.ID, Command: c.Command, TimeoutSeconds: c.TimeoutSeconds, Status: CheckFailed, Output: truncateCheckOutput(err.Error())}
			continue
		}
		run := tools.RunCommand(ctx, &env, c.Command, timeout)
		status := CheckPassed
		if run.Failed() {
			status = CheckFailed
			result.Passed = false
		}
		display := run.Display()
		if display == "" {
			display = "(no output)"
		}
		out := checkOutcome{ID: c.ID, Command: c.Command, Status: status, ExitCode: run.ExitCode, Output: display}
		result.Checks = append(result.Checks, out)
		updates[c.ID] = Check{
			ID:             c.ID,
			Command:        c.Command,
			TimeoutSeconds: c.TimeoutSeconds,
			Status:         status,
			Output:         truncateCheckOutput(display),
			ExitCode:       run.ExitCode,
			RanAt:          time.Now(),
		}
		if run.Err != nil && ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	o.mu.Lock()
	if step = o.ledger.Find(a.StepID); step != nil {
		for i := range step.Checks {
			if c, ok := updates[step.Checks[i].ID]; ok {
				step.Checks[i] = c
			}
		}
		step.Updated = time.Now()
		o.ledger.Updated = time.Now()
	}
	saveErr := o.ledger.Save(o.ledgerPath)
	result.Ledger = o.ledger.View()
	o.mu.Unlock()
	if saveErr != nil {
		result.Warning = "could not persist ledger: " + saveErr.Error()
	}
	return jsonMarshalIndent(result)
}

func truncateCheckOutput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxCheckOutput {
		return s
	}
	return s[:maxCheckOutput] + "\n…[truncated]"
}

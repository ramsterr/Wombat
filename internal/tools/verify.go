package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/project"
)

// Verify runs the project's own checks. It exists because a model saying
// "tests pass" is only meaningful if the command was the project's test
// command, discovered from the repository, not invented for the occasion.
type Verify struct{}

type verifyArgs struct {
	Kind      string `json:"kind"`
	Command   string `json:"command"`
	TimeoutMS int    `json:"timeout_ms"`
	FailFast  bool   `json:"fail_fast"`
}

type checkResult struct {
	Kind     string
	Command  string
	Output   string
	Failed   bool
	TimedOut bool
}

func (t *Verify) Name() string  { return "verify" }
func (t *Verify) Class() string { return "bash" }
func (t *Verify) Description() string {
	return "Run the project's detected tests, build, lint, typecheck, or format check. Detection covers Go, Node (npm/pnpm/yarn/bun), Python, Rust, Maven, Gradle, Ruby, PHP, CMake, and Makefile projects. Use kind=auto for every detected check, or pass an explicit command. There is no default timeout."
}
func (t *Verify) Schema() map[string]any {
	return obj(map[string]any{
		"kind":       map[string]any{"type": "string", "enum": []string{"auto", "test", "build", "typecheck", "lint", "format"}, "description": "which detected check to run (default: test)"},
		"command":    desc("optional exact command; overrides detection"),
		"timeout_ms": map[string]any{"type": "integer", "description": "optional timeout; omitted means run to completion"},
		"fail_fast":  map[string]any{"type": "boolean", "description": "with kind=auto, stop at the first failure"},
	})
}

func (t *Verify) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[verifyArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	kind := strings.ToLower(strings.TrimSpace(a.Kind))
	if kind == "" {
		kind = "test"
	}
	detected := project.Detect(env.Dir)
	var checks []struct{ kind, command string }
	if strings.TrimSpace(a.Command) != "" {
		checks = append(checks, struct{ kind, command string }{"custom", strings.TrimSpace(a.Command)})
	} else if kind == "auto" {
		for _, k := range project.KindNames {
			if cmd, ok := detected.Command(k); ok {
				checks = append(checks, struct{ kind, command string }{k, cmd})
			}
		}
		if len(checks) == 0 {
			return "", fmt.Errorf("no checks detected for this project (%s)", detected.Kind)
		}
	} else {
		cmd, ok := detected.Command(kind)
		if !ok {
			return "", fmt.Errorf("no %s command detected for this %s project; pass command explicitly", kind, detected.Kind)
		}
		checks = append(checks, struct{ kind, command string }{kind, cmd})
	}
	timeout := time.Duration(a.TimeoutMS) * time.Millisecond
	commands := make([]string, 0, len(checks))
	mutates := false
	for _, c := range checks {
		if err := env.checkPolicy("bash", c.command); err != nil {
			return "", err
		}
		if target := shellSecretTarget(c.command); target != "" {
			if err := env.permitPreview("sensitive-read", target, ""); err != nil {
				return "", err
			}
		}
		if err := env.guardDangerous(c.command); err != nil {
			return "", err
		}
		commands = append(commands, c.command)
		if MayMutate(c.command) {
			mutates = true
		}
	}
	if err := env.permitPreview("bash", "verify: "+strings.Join(commands, "; "), ""); err != nil {
		return "", err
	}
	if mutates {
		if err := env.Snapshot("before verify"); err != nil {
			return "", err
		}
	}
	results := make([]checkResult, 0, len(checks))
	for _, c := range checks {
		res := RunCommand(ctx, env, c.command, timeout)
		r := checkResult{Kind: c.kind, Command: c.command, Output: res.Display(), Failed: res.Failed(), TimedOut: res.TimedOut}
		results = append(results, r)
		if r.Failed && a.FailFast {
			break
		}
	}
	var b strings.Builder
	pass, fail := 0, 0
	for _, r := range results {
		if r.Failed {
			fail++
		} else {
			pass++
		}
	}
	fmt.Fprintf(&b, "project: %s at %s\n", detected.Kind, detected.Root)
	fmt.Fprintf(&b, "checks: %d passed, %d failed\n", pass, fail)
	for _, r := range results {
		status := "PASS"
		if r.Failed {
			status = "FAIL"
			if r.TimedOut {
				status = "TIMEOUT"
			}
		}
		fmt.Fprintf(&b, "\n=== %s (%s): %s ===\n%s\n", r.Kind, status, r.Command, r.Output)
	}
	if fail > 0 {
		return b.String(), fmt.Errorf("%d check(s) failed", fail)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

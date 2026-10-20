package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Bash struct{}

type bashArgs struct {
	Command   string `json:"command"`
	TimeoutMS int    `json:"timeout_ms"`
}

func (t *Bash) Name() string  { return "bash" }
func (t *Bash) Class() string { return "bash" }
func (t *Bash) Description() string {
	return "Run a shell command in the working directory. Returns combined stdout/stderr and exit code. Use for builds, tests, git, and listing files."
}
func (t *Bash) Schema() map[string]any {
	return obj(map[string]any{
		"command":    desc("shell command to run via sh -c"),
		"timeout_ms": map[string]any{"type": "integer", "description": "timeout in milliseconds (default 120000)"},
	}, "command")
}

func (t *Bash) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[bashArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if strings.TrimSpace(a.Command) == "" {
		return "", errors.New("empty command")
	}
	if err := env.checkPolicy("bash", a.Command); err != nil {
		return "", err
	}
	if env.DenyMutate && MayMutate(a.Command) {
		return "", fmt.Errorf("chat mode: commands that change things are disabled — switch to create mode to run them")
	}
	if err := env.permit("bash", a.Command); err != nil {
		return "", err
	}
	if MayMutate(a.Command) {
		if err := env.Snapshot("before mutating bash"); err != nil {
			return "", err
		}
	}
	timeout := time.Duration(a.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 120 * time.Second
	}

	result := RunCommand(ctx, env, a.Command, timeout)
	if errors.Is(result.Err, context.Canceled) {
		return "", result.Err
	}

	if errors.Is(result.Err, context.Canceled) {
		return "", result.Err
	}

	out := result.Display()
	if out == "" {
		out = "(no output)"
	}
	return Truncate(out, 800, 0.3, 400*1024), nil
}

// looksLikePermissionDenied reports whether a command failed because the
// operating system refused the write.
func looksLikePermissionDenied(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "operation not permitted") ||
		strings.Contains(lower, "permission denied")
}

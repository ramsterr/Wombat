package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
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
	if !env.Allowed("bash", a.Command) {
		return "", errors.New("permission denied by user")
	}
	timeout := time.Duration(a.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", a.Command)
	cmd.Dir = env.Dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err = cmd.Run()

	out := buf.String()
	if runCtx.Err() == context.DeadlineExceeded {
		out += fmt.Sprintf("\nerror: timed out after %s", timeout)
	} else if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			out += fmt.Sprintf("\nexit code: %d", exitErr.ExitCode())
		} else {
			out += "\nerror: " + err.Error()
		}
	}
	out = strings.TrimSpace(out)
	if out == "" {
		out = "(no output)"
	}
	return Truncate(out, 220, 0.25, 48*1024), nil
}

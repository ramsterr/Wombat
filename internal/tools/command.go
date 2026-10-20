package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/sandbox"
)

type CommandResult struct {
	Output   string
	ExitCode int
	TimedOut bool
	Err      error
}

func RunCommand(ctx context.Context, env *Env, command string, timeout time.Duration) CommandResult {
	return RunCommandWithPolicy(ctx, env, command, timeout, env.sandboxPolicy())
}

// RunCommandWithPolicy runs a command under a specific sandbox policy.
func RunCommandWithPolicy(ctx context.Context, env *Env, command string, timeout time.Duration, policy sandbox.Policy) CommandResult {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	name, args := "sh", []string{"-c", command}
	if env != nil {
		var err error
		name, args, err = policy.Command(command)
		if err != nil {
			return CommandResult{Err: fmt.Errorf("sandbox: %w", err)}
		}
	}
	cmd := exec.CommandContext(runCtx, name, args...)
	if env != nil {
		cmd.Dir = env.Dir
	}
	cmd.WaitDelay = 2 * time.Second
	prepareCommand(cmd)
	buf := &cappedBuffer{max: 1024 * 1024}
	cmd.Stdout = buf
	cmd.Stderr = buf
	err := cmd.Run()
	out := strings.TrimSpace(buf.String())
	result := CommandResult{Output: out}
	if runCtx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
		result.Err = runCtx.Err()
		return result
	}
	if errors.Is(runCtx.Err(), context.Canceled) {
		result.Err = runCtx.Err()
		return result
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			return result
		}
		result.Err = err
	}
	return result
}

type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.max <= 0 || b.Len() < b.max {
		left := b.max - b.Len()
		if b.max <= 0 || len(p) <= left {
			_, _ = b.Buffer.Write(p)
		} else {
			_, _ = b.Buffer.Write(p[:left])
		}
	}
	return len(p), nil
}

func (r CommandResult) Failed() bool {
	return r.TimedOut || r.Err != nil || r.ExitCode != 0
}

func (r CommandResult) Display() string {
	out := r.Output
	switch {
	case r.TimedOut:
		out += "\nerror: timed out"
	case r.Err != nil && !errors.Is(r.Err, context.Canceled):
		out += "\nerror: " + r.Err.Error()
	case r.ExitCode != 0:
		out += fmt.Sprintf("\nexit code: %d", r.ExitCode)
	}
	return strings.TrimSpace(out)
}

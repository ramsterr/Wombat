package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/sandbox"
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
	// Capability escalations come first: if the user refuses the network or
	// a dangerous command, there is no point asking about the command itself.
	if !env.allowDangerous(a.Command) {
		return "", fmt.Errorf("blocked dangerous command: %s\n  %s", a.Command, sandbox.DangerHint())
	}
	policy := env.sandboxPolicy()
	policy, ok := env.escalateNetwork(ctx, policy, a.Command)
	if !ok {
		return "", fmt.Errorf("network access is blocked by the sandbox: %s\n  %s", a.Command, sandbox.NetworkHint())
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

	result := RunCommandWithPolicy(ctx, env, a.Command, timeout, policy)
	if errors.Is(result.Err, context.Canceled) {
		return "", result.Err
	}

	// A command that the sandbox stopped from writing somewhere else fails
	// with an operating system error. Offer the user a retry with the write
	// restrictions lifted.
	if result.Failed() && policy.Enabled() && looksLikePermissionDenied(result.Output) {
		switch env.askSandbox(SandboxWrite, a.Command) {
		case SandboxAllowOnce:
			relaxed := policy
			relaxed.Mode = sandbox.Off
			result = RunCommandWithPolicy(ctx, env, a.Command, timeout, relaxed)
		case SandboxAllowSession:
			env.widenSandbox(func(s *sandbox.Settings) { s.Mode = sandbox.Off })
			result = RunCommandWithPolicy(ctx, env, a.Command, timeout, env.sandboxPolicy())
		}
	}
	if errors.Is(result.Err, context.Canceled) {
		return "", result.Err
	}

	out := result.Display()
	if out == "" {
		out = "(no output)"
	}
	return Truncate(out, 220, 0.25, 48*1024), nil
}

// escalateNetwork asks the user for network access when the sandbox blocks
// it. It returns the policy to run with and whether the command may run.
func (e *Env) escalateNetwork(_ context.Context, policy sandbox.Policy, command string) (sandbox.Policy, bool) {
	if !policy.Enabled() || policy.AllowNetwork || !IsNetworkCommand(command) {
		return policy, true
	}
	switch e.askSandbox(SandboxNetwork, command) {
	case SandboxAllowOnce:
		policy.AllowNetwork = true
		return policy, true
	case SandboxAllowSession:
		e.widenSandbox(func(s *sandbox.Settings) { s.Network = true })
		widened := e.sandboxPolicy()
		widened.AllowNetwork = true
		return widened, true
	default:
		return policy, false
	}
}

// looksLikePermissionDenied reports whether a command failed because the
// operating system refused the write.
func looksLikePermissionDenied(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "operation not permitted") ||
		strings.Contains(lower, "permission denied")
}

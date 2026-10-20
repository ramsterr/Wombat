package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ramsterr/Wombat/internal/sandbox"
)

type Checkpointer interface {
	Snapshot(reason string, paths []string) (string, error)
}

type Decision struct {
	Allow    bool
	Always   bool
	Feedback string
}

// toolsDeniedNote is what the model sees when a call is refused and there is
// no explanation: it must not claim the work happened.
const toolsDeniedNote = "the user declined this call. Do not claim it was done. " +
	"Continue without it, or tell the user what you need in order to proceed."

type Env struct {
	Dir           string
	Permit        func(kind, detail string) Decision
	ReadOnly      bool
	AllowOutside  bool
	DenyDangerous bool
	DenyNetwork   bool
	DenyGit       bool
	DenyOutside   bool
	DenyMutate    bool
	Mode          string
	Sandbox       sandbox.Policy
	Checkpoint    Checkpointer

	// SandboxState is the session's live sandbox configuration. When set it
	// overrides Sandbox, so a change made mid-session takes effect at once.
	SandboxState *sandbox.State

	// SandboxAsk asks the user whether a command blocked by the sandbox may
	// run anyway. It returns SandboxDeny when the user declines.
	SandboxAsk func(kind, command string) SandboxDecision

	// media collects files a tool wants the model to look at, such as an
	// image the agent downloaded. The agent owns the list.
	media *[]Media

	// spillDir is where a large tool result is written so the model can
	// read the parts it needs instead of carrying the whole thing in its
	// context. The agent sets it.
	spillDir string

	// AskUser asks the user a question and waits for the answer. It is nil
	// where nobody can answer, such as inside a worker.
	AskUser func(q Question) (string, error)
}

// SetSpillDir sets the directory large tool results are written to.
func (e *Env) SetSpillDir(dir string) {
	if e == nil {
		return
	}
	e.spillDir = dir
}

// Media is a file a tool produced for the model to look at.
type Media struct {
	// Path is the file on disk.
	Path string
	// MIME is the content type.
	MIME string
	// Name is a friendly name for the model.
	Name string
}

// SetMediaSink gives the environment the list that collects files for the
// model. The agent owns it and drains it after every tool call.
func (e *Env) SetMediaSink(m *[]Media) {
	if e == nil {
		return
	}
	e.media = m
}

// AddMedia hands a file to the model as an attachment.
func (e *Env) AddMedia(m Media) {
	if e == nil || e.media == nil {
		return
	}
	*e.media = append(*e.media, m)
}

// SandboxDecision is the user's answer to a sandbox escalation request.
type SandboxDecision int

const (
	// SandboxDeny keeps the sandbox in place and refuses the command.
	SandboxDeny SandboxDecision = iota
	// SandboxAllowOnce runs this one command with the restriction lifted.
	SandboxAllowOnce
	// SandboxAllowSession widens the sandbox for the rest of the session.
	SandboxAllowSession
)

// Sandbox escalation kinds.
const (
	SandboxNetwork   = "network"
	SandboxWrite     = "write"
	SandboxDangerous = "dangerous"
)

// sandboxPolicy returns the policy in force for this environment.
func (e *Env) sandboxPolicy() sandbox.Policy {
	if e == nil {
		return sandbox.Policy{}
	}
	if e.SandboxState != nil {
		return e.SandboxState.Policy(e.Dir)
	}
	return e.Sandbox
}

// askSandbox asks the user about a blocked command.
func (e *Env) askSandbox(kind, command string) SandboxDecision {
	if e == nil || e.SandboxAsk == nil {
		return SandboxDeny
	}
	return e.SandboxAsk(kind, command)
}

// allowDangerous asks the user about a command on the dangerous list. It
// returns true when the command may run.
func (e *Env) allowDangerous(command string) bool {
	if e == nil || !IsDangerousCommand(command) {
		return true
	}
	if e.SandboxState != nil && e.SandboxState.Settings().AllowDangerous {
		return true
	}
	if e.SandboxAsk == nil {
		return false
	}
	switch e.askSandbox(SandboxDangerous, command) {
	case SandboxAllowOnce:
		return true
	case SandboxAllowSession:
		e.widenSandbox(func(s *sandbox.Settings) { s.AllowDangerous = true })
		return true
	}
	return false
}

// AskNetwork resolves network access for a tool that reaches the network
// itself, such as webfetch and websearch. The sandbox cannot police those
// calls, because they run inside the agent process, so the decision is made
// here.
func (e *Env) AskNetwork(detail string) error {
	policy := e.sandboxPolicy()
	if !e.DenyNetwork && (!policy.Enabled() || policy.AllowNetwork) {
		return nil
	}
	if e.SandboxAsk == nil {
		return describeNetworkBlock(detail, e)
	}
	switch e.askSandbox(SandboxNetwork, detail) {
	case SandboxAllowOnce:
		return nil
	case SandboxAllowSession:
		e.widenSandbox(func(s *sandbox.Settings) { s.Network = true })
		return nil
	}
	return describeNetworkBlock(detail, e)
}

// widenSandbox changes the session sandbox settings.
func (e *Env) widenSandbox(fn func(*sandbox.Settings)) {
	if e == nil || e.SandboxState == nil {
		return
	}
	e.SandboxState.Update(fn)
}

func (e *Env) Snapshot(reason string, paths ...string) error {
	if e == nil || e.Checkpoint == nil || e.ReadOnly {
		return nil
	}
	if _, err := e.Checkpoint.Snapshot(reason, paths); err != nil {
		return fmt.Errorf("checkpoint failed: %w", err)
	}
	return nil
}

type Tool interface {
	Name() string
	Class() string
	Description() string
	Schema() map[string]any
	Run(ctx context.Context, args json.RawMessage, env *Env) (string, error)
}

func Registry() []Tool {
	return []Tool{&Bash{}, &Read{}, &Write{}, &Edit{}, &MultiEdit{}, &Glob{}, &Grep{}, &Batch{}, &WebFetch{}, &WebSearch{}}
}

// RegistryWithQuestion is the full tool set, including the question tool the
// lead uses to hand decisions back to the user.
func RegistryWithQuestion() []Tool {
	return append(Registry(), &QuestionTool{})
}

func (e *Env) Check(kind, detail string) error {
	if err := e.checkPolicy(kind, detail); err != nil {
		return err
	}
	return e.permit(kind, detail)
}

func (e *Env) checkPolicy(kind, detail string) error {
	if kind == "read" {
		return nil
	}
	if e.ReadOnly && (kind == "write" || kind == "edit" || kind == "bash") {
		return fmt.Errorf("read-only mode: %s is disabled", kind)
	}
	if e.DenyMutate && (kind == "write" || kind == "edit") {
		return fmt.Errorf("chat mode: %s is disabled — switch to create mode to change files", kind)
	}
	if kind == "network" {
		// The web tools reach the network directly, so the sandbox's network
		// rule applies to them too.
		policy := e.sandboxPolicy()
		if e.DenyNetwork || (policy.Enabled() && !policy.AllowNetwork) {
			if e.SandboxAsk == nil {
				return describeNetworkBlock(detail, e)
			}
			return nil // the tool asks the user before it runs
		}
	}
	if kind == "bash" {
		// A dangerous command is always blocked here; the bash tool asks the
		// user first when it can, and falls back to refusing.
		if e.DenyDangerous && IsDangerousCommand(detail) && e.SandboxAsk == nil {
			return fmt.Errorf("blocked dangerous command: %s", detail)
		}
		// The network is blocked by the sandbox. When the user can be asked,
		// the bash tool handles the escalation, so do not refuse here.
		if e.SandboxAsk == nil {
			policy := e.sandboxPolicy()
			if (e.DenyNetwork || (policy.Enabled() && !policy.AllowNetwork)) && IsNetworkCommand(detail) {
				return describeNetworkBlock(detail, e)
			}
		}
		if e.DenyGit && IsGitMutatingCommand(detail) {
			return fmt.Errorf("git mutations are disabled for this worker: %s", detail)
		}
	}
	return nil
}

func (e *Env) permit(kind, detail string) error {
	if kind == "read" || e.Permit == nil {
		return nil
	}
	d := e.Permit(kind, detail)
	if d.Allow {
		return nil
	}
	if d.Feedback != "" {
		return fmt.Errorf("permission denied by user: %s", d.Feedback)
	}
	return fmt.Errorf("permission denied by user: %s", toolsDeniedNote)
}

func decode[T any](args json.RawMessage) (T, error) {
	var v T
	if len(args) == 0 {
		return v, nil
	}
	err := json.Unmarshal(args, &v)
	return v, err
}

func desc(what string) map[string]any {
	return map[string]any{"type": "string", "description": what}
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

// spillThreshold is how large a tool result may get before it is written to
// a file instead of being carried in the model's context.
const spillThreshold = 24 * 1024

// Spill writes a large result to a file and returns a short pointer to it.
// Claude Code does the same with tool output: the model gets the path and
// reads only the parts it needs, instead of carrying the whole thing.
func (e *Env) Spill(name, content string) (string, bool) {
	if e == nil || e.spillDir == "" || len(content) <= spillThreshold {
		return content, false
	}
	if err := os.MkdirAll(e.spillDir, 0o755); err != nil {
		return content, false
	}
	path := filepath.Join(e.spillDir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return content, false
	}
	lines := strings.Count(content, "\n") + 1
	head := content
	if len(head) > spillThreshold {
		head = head[:spillThreshold]
	}
	return fmt.Sprintf("%s\n[the full output is %d lines in %s - read the parts you need]", head, lines, path), true
}

func Truncate(s string, maxLines int, headFraction float64, maxBytes int) string {
	if maxLines <= 0 && maxBytes <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	tooManyLines := maxLines > 0 && len(lines) > maxLines
	tooManyBytes := maxBytes > 0 && len(s) > maxBytes
	if !tooManyLines && !tooManyBytes {
		return s
	}
	if !tooManyLines {
		head := maxBytes * 2 / 3
		if head > len(s) {
			head = len(s)
		}
		tail := len(s) - maxBytes/3
		if tail < head {
			tail = head
		}
		return s[:head] + fmt.Sprintf("\n… [%d bytes omitted] …\n", len(s)-head-(len(s)-tail)) + s[tail:]
	}
	head := int(float64(maxLines) * headFraction)
	if head < 1 {
		head = 1
	}
	tail := maxLines - head
	if tail < 0 {
		tail = 0
	}
	omitted := len(lines) - head - tail
	out := append([]string{}, lines[:head]...)
	out = append(out, fmt.Sprintf("… [%d lines omitted] …", omitted))
	out = append(out, lines[len(lines)-tail:]...)
	return strings.Join(out, "\n")
}

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/sandbox"
)

func TestNetworkEscalationOnce(t *testing.T) {
	if !sandbox.Supported(sandbox.WorkspaceWrite) {
		t.Skip("the sandbox is not enforced on this host")
	}
	dir := t.TempDir()
	state := sandbox.NewState(sandbox.Settings{Mode: sandbox.WorkspaceWrite, Network: false})
	asked := 0
	env := &Env{
		Dir:          dir,
		SandboxState: state,
		SandboxAsk: func(kind, command string) SandboxDecision {
			asked++
			if kind != SandboxNetwork {
				t.Errorf("kind = %q", kind)
			}
			return SandboxAllowOnce
		},
	}
	// The network is off in the session state, but the user allowed this one
	// command, so it runs. The command is classified as a network command but
	// fails immediately, so the test needs no connectivity.
	out, err := (&Bash{}).Run(context.Background(), json.RawMessage(`{"command":"curl --not-a-real-flag"}`), env)
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Error("expected the command to run")
	}
	if asked != 1 {
		t.Errorf("asked = %d", asked)
	}
	if state.Settings().Network {
		t.Error("a one-time approval must not change the session settings")
	}
}

func TestNetworkEscalationSession(t *testing.T) {
	if !sandbox.Supported(sandbox.WorkspaceWrite) {
		t.Skip("the sandbox is not enforced on this host")
	}
	state := sandbox.NewState(sandbox.Settings{Mode: sandbox.WorkspaceWrite, Network: false})
	env := &Env{
		Dir:          t.TempDir(),
		SandboxState: state,
		SandboxAsk: func(kind, command string) SandboxDecision {
			return SandboxAllowSession
		},
	}
	_, err := (&Bash{}).Run(context.Background(), json.RawMessage(`{"command":"curl --not-a-real-flag"}`), env)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Settings().Network {
		t.Error("a session approval should turn the network on for the session")
	}
}

func TestNetworkEscalationDenied(t *testing.T) {
	if !sandbox.Supported(sandbox.WorkspaceWrite) {
		t.Skip("the sandbox is not enforced on this host")
	}
	state := sandbox.NewState(sandbox.Settings{Mode: sandbox.WorkspaceWrite, Network: false})
	env := &Env{
		Dir:          t.TempDir(),
		SandboxState: state,
		SandboxAsk: func(kind, command string) SandboxDecision {
			return SandboxDeny
		},
	}
	_, err := (&Bash{}).Run(context.Background(), json.RawMessage(`{"command":"curl https://example.com"}`), env)
	if err == nil {
		t.Fatal("expected the command to be refused")
	}
	if !strings.Contains(err.Error(), "blocked by the sandbox") {
		t.Errorf("error = %v", err)
	}
}

func TestNetworkEscalationNotAskedWithoutSandbox(t *testing.T) {
	env := &Env{
		Dir: t.TempDir(),
		SandboxAsk: func(kind, command string) SandboxDecision {
			t.Fatal("the user should not be asked when the sandbox is off")
			return SandboxDeny
		},
	}
	out, err := (&Bash{}).Run(context.Background(), json.RawMessage(`{"command":"echo plain"}`), env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "plain") {
		t.Errorf("out = %q", out)
	}
}

func TestWriteEscalationAfterPermissionError(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "target.txt")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	target := filepath.Join(home, ".wombat-escalation-test.txt")
	t.Cleanup(func() { os.Remove(target) })

	// A workspace-write sandbox cannot write into the home directory.
	state := sandbox.NewState(sandbox.Settings{Mode: sandbox.WorkspaceWrite, Network: false})
	asked := 0
	env := &Env{
		Dir:          dir,
		SandboxState: state,
		SandboxAsk: func(kind, command string) SandboxDecision {
			asked++
			if kind != SandboxWrite {
				t.Errorf("kind = %q", kind)
			}
			return SandboxAllowOnce
		},
	}
	if !sandbox.Supported(sandbox.WorkspaceWrite) {
		t.Skip("the sandbox is not enforced on this host")
	}
	_, err = (&Bash{}).Run(context.Background(), json.RawMessage(`{"command":"echo hi > `+target+`"}`), env)
	if err != nil {
		t.Fatal(err)
	}
	if asked != 1 {
		t.Fatalf("asked = %d, want 1", asked)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("the file was not written: %v", statErr)
	}
	_ = outside
}

// TestProductionWiringAsksBeforeRefusing mirrors how the lead agent is
// actually built: a live sandbox state, the dangerous-command list enabled,
// and a callback that can ask the user.
func TestProductionWiringAsksBeforeRefusing(t *testing.T) {
	if !sandbox.Supported(sandbox.WorkspaceWrite) {
		t.Skip("the sandbox is not enforced on this host")
	}
	state := sandbox.NewState(sandbox.Settings{Mode: sandbox.WorkspaceWrite, Network: false})
	var asked []string
	env := &Env{
		Dir:           t.TempDir(),
		SandboxState:  state,
		DenyDangerous: true,
		SandboxAsk: func(kind, command string) SandboxDecision {
			asked = append(asked, kind)
			return SandboxAllowOnce
		},
	}

	// A network command reaches the user instead of being refused outright.
	if _, err := (&Bash{}).Run(context.Background(), json.RawMessage(`{"command":"curl --not-a-real-flag"}`), env); err != nil {
		t.Fatalf("network command: %v", err)
	}
	if len(asked) == 0 || asked[0] != SandboxNetwork {
		t.Fatalf("asked = %v, want a network question", asked)
	}

	// A dangerous command reaches the user too.
	asked = nil
	if _, err := (&Bash{}).Run(context.Background(), json.RawMessage(`{"command":"sudo rm -rf / --no-preserve-root"}`), env); err != nil {
		t.Fatalf("dangerous command: %v", err)
	}
	if len(asked) == 0 || asked[0] != SandboxDangerous {
		t.Fatalf("asked = %v, want a dangerous command question", asked)
	}
}

func TestDangerousDeniedStops(t *testing.T) {
	env := &Env{
		Dir:           t.TempDir(),
		DenyDangerous: true,
		SandboxAsk: func(kind, command string) SandboxDecision {
			return SandboxDeny
		},
	}
	_, err := (&Bash{}).Run(context.Background(), json.RawMessage(`{"command":"sudo rm -rf / --no-preserve-root"}`), env)
	if err == nil {
		t.Fatal("expected the dangerous command to be refused")
	}
	if !strings.Contains(err.Error(), "blocked dangerous command") {
		t.Errorf("error = %v", err)
	}
}

func TestPermKindLabels(t *testing.T) {
	if !PermNetwork.Outside() {
		t.Error("network is outside the sandbox")
	}
	if PermBash.Outside() {
		t.Error("bash is not outside the sandbox")
	}
	if PermKindFor("write") != PermWrite {
		t.Errorf("PermKindFor(write) = %q", PermKindFor("write"))
	}
	if PermKindFor("outside") != PermOutside {
		t.Errorf("PermKindFor(outside) = %q", PermKindFor("outside"))
	}
}

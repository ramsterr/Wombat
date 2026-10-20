package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandClassification(t *testing.T) {
	dangerous := []string{
		"sudo rm -rf /",
		"rm -rf /",
		"rm -rf ~",
		"git reset --hard HEAD~1",
		"git clean -fd",
		"git push --force origin main",
		"mkfs.ext4 /dev/sda1",
		"dd if=/dev/zero of=/dev/sda",
		"shutdown -h now",
		":(){ :|:& };:",
	}
	for _, command := range dangerous {
		if !IsDangerousCommand(command) {
			t.Errorf("expected dangerous: %q", command)
		}
	}
	safe := []string{
		"go test ./...",
		"git status",
		"rm -rf ./build",
		"rm -rf node_modules",
		"echo sudo",
	}
	for _, command := range safe {
		if IsDangerousCommand(command) {
			t.Errorf("unexpectedly dangerous: %q", command)
		}
	}

	network := []string{
		"curl https://example.com",
		"git clone https://example.com/repo.git",
		"go get example.com/pkg",
		"npm install",
		"pip3 install requests",
	}
	for _, command := range network {
		if !IsNetworkCommand(command) {
			t.Errorf("expected network command: %q", command)
		}
	}
	local := []string{"go test ./...", "git status", "echo curl", "cat file"}
	for _, command := range local {
		if IsNetworkCommand(command) {
			t.Errorf("unexpectedly network command: %q", command)
		}
	}
}

func TestReadOnlyEnvBlocksMutations(t *testing.T) {
	env := &Env{
		Dir:      t.TempDir(),
		ReadOnly: true,
		Permit:   func(string, string) Decision { return Decision{Allow: true} },
	}
	for _, kind := range []string{"write", "edit", "bash"} {
		if err := env.Check(kind, "x"); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("%s should be blocked: %v", kind, err)
		}
	}
	if err := env.Check("read", "x"); err != nil {
		t.Errorf("read should be allowed: %v", err)
	}
}

func TestCheckPathOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	env := &Env{
		Dir:    root,
		Permit: func(string, string) Decision { return Decision{} },
	}
	err := env.CheckPath("write", filepath.Join(outside, "x.txt"))
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected outside-workspace denial, got %v", err)
	}
	if err := env.CheckPath("read", filepath.Join(outside, "x.txt")); err != nil {
		t.Fatalf("outside reads should be allowed: %v", err)
	}

	env.Permit = func(string, string) Decision { return Decision{Allow: true} }
	if err := env.CheckPath("write", filepath.Join(outside, "x.txt")); err != nil {
		t.Fatalf("permitted outside write should be allowed: %v", err)
	}
	env.AllowOutside = true
	if err := env.CheckPath("write", "../x.txt"); err != nil {
		t.Fatalf("allow-outside write should be allowed: %v", err)
	}
}

func TestCheckPathOutsideAsksOnce(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	calls := 0
	env := &Env{
		Dir: root,
		Permit: func(string, string) Decision {
			calls++
			return Decision{Allow: true}
		},
	}
	if err := env.CheckPath("write", filepath.Join(outside, "x.txt")); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("permit calls = %d, want 1", calls)
	}
}

func TestWriteToolCannotEscapeWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	env := &Env{Dir: root, Permit: func(string, string) Decision { return Decision{} }}
	raw, _ := json.Marshal(writeArgs{Path: filepath.Join(outside, "x.txt"), Content: "x"})
	if _, err := (&Write{}).Run(context.Background(), raw, env); err == nil {
		t.Fatal("write outside workspace should fail")
	}
	if _, err := os.Stat(filepath.Join(outside, "x.txt")); !os.IsNotExist(err) {
		t.Fatal("outside file was written")
	}
}

func TestBashPolicyAllowsDangerousAndNetwork(t *testing.T) {
	// Nothing is blocked by default any more: the sandbox is gone and the
	// permission classes decide.
	env := &Env{
		Dir:    t.TempDir(),
		Permit: func(string, string) Decision { return Decision{Allow: true} },
	}
	if _, err := (&Bash{}).Run(context.Background(), json.RawMessage(`{"command":"echo hi"}`), env); err != nil {
		t.Errorf("a plain command should run: %v", err)
	}
	// A deny rule still stops the call.
	env2 := &Env{
		Dir:    t.TempDir(),
		Permit: func(kind, detail string) Decision { return Decision{} },
	}
	if _, err := (&Bash{}).Run(context.Background(), json.RawMessage(`{"command":"echo hi"}`), env2); err == nil {
		t.Error("a denied command should not run")
	}
}

func TestDeniedCallTellsTheModelNotToClaimIt(t *testing.T) {
	env := &Env{
		Dir:    t.TempDir(),
		Permit: func(kind, detail string) Decision { return Decision{} },
	}
	_, err := (&Bash{}).Run(context.Background(), json.RawMessage(`{"command":"echo hi"}`), env)
	if err == nil {
		t.Fatal("expected the call to be denied")
	}
	if !strings.Contains(err.Error(), "Do not claim it was done") {
		t.Errorf("the model should be told not to claim the work: %v", err)
	}
}

func TestDeniedCallKeepsUserFeedback(t *testing.T) {
	env := &Env{
		Dir:    t.TempDir(),
		Permit: func(kind, detail string) Decision { return Decision{Feedback: "use printf instead"} },
	}
	_, err := (&Bash{}).Run(context.Background(), json.RawMessage(`{"command":"echo hi"}`), env)
	if err == nil {
		t.Fatal("expected the call to be denied")
	}
	if !strings.Contains(err.Error(), "use printf instead") {
		t.Errorf("the feedback should be kept: %v", err)
	}
}

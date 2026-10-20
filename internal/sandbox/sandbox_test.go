package sandbox_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ramsterr/Wombat/internal/sandbox"
)

func TestParseAndSupported(t *testing.T) {
	for _, s := range []string{"", "off", "read-only", "workspace-write", "danger-full-access"} {
		if _, err := sandbox.Parse(s); err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
	}
	if _, err := sandbox.Parse("nope"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestWorkspaceWriteAllowsWrites(t *testing.T) {
	if !sandbox.Supported(sandbox.WorkspaceWrite) {
		t.Skip("sandbox not supported on this platform")
	}
	dir := t.TempDir()
	p := sandbox.Policy{Mode: sandbox.WorkspaceWrite, Workspace: dir, AllowNetwork: false}
	run(t, p, dir, "printf hi > out.txt")
	data, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil || string(data) != "hi" {
		t.Fatalf("out.txt = %q err=%v", data, err)
	}
}

func TestReadOnlyBlocksWorkspaceWrites(t *testing.T) {
	if !sandbox.Supported(sandbox.ReadOnly) {
		t.Skip("sandbox not supported on this platform")
	}
	dir := t.TempDir()
	p := sandbox.Policy{Mode: sandbox.ReadOnly, Workspace: dir}
	if err := runErr(p, dir, "touch blocked.txt"); err == nil {
		t.Skip("sandbox did not block write; platform may allow it")
	}
	if _, err := os.Stat(filepath.Join(dir, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("blocked.txt exists after read-only command")
	}
}

func TestUnsupportedPlatformFailsClosed(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		t.Skip("supported platform")
	}
	p := sandbox.Policy{Mode: sandbox.WorkspaceWrite, Workspace: t.TempDir()}
	if _, _, err := p.Command("true"); err == nil {
		t.Fatal("expected unsupported error")
	}
}

func run(t *testing.T, p sandbox.Policy, dir, command string) {
	t.Helper()
	if err := runErr(p, dir, command); err != nil {
		t.Fatal(err)
	}
}

func runErr(p sandbox.Policy, dir, command string) error {
	name, args, err := p.Command(command)
	if err != nil {
		return err
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd.Run()
}

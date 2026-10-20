package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestCreateCommitAndApply(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.name", "Test")
	git(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "a.txt")
	git(t, repo, "commit", "-m", "initial")

	m, err := New(repo, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wt, err := m.Create("writer")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "b.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	patch, err := wt.Commit("writer change")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "b.txt") {
		t.Fatalf("patch missing b.txt: %s", patch)
	}
	if err := m.CheckPatch(patch); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyPatch(patch); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "b.txt"))
	if err != nil || string(data) != "two" {
		t.Fatalf("b.txt = %q err=%v", data, err)
	}
	if err := wt.Remove(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateRejectsDirtyRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.name", "Test")
	git(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "a.txt")
	git(t, repo, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := New(repo, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create("writer"); err == nil || !strings.Contains(err.Error(), "tracked changes") {
		t.Fatalf("expected dirty-repo error, got %v", err)
	}
}

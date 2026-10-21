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

func TestApplyPatch3WayRebasesOntoNewerHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.name", "Test")
	git(t, repo, "config", "user.email", "test@example.com")
	path := filepath.Join(repo, "a.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "a.txt")
	git(t, repo, "commit", "-m", "initial")

	m, err := New(repo, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.Create("first")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first.Path, "a.txt"), []byte("ONE\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patchFirst, err := first.Commit("capitalize")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Create("second")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second.Path, "a.txt"), []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patchSecond, err := second.Commit("append")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyPatch3Way(patchFirst); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyPatch3Way(patchSecond); err != nil {
		t.Fatalf("3-way merge of a non-overlapping hunk failed: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ONE\ntwo\nthree\nfour\n" {
		t.Fatalf("merged file = %q", data)
	}
}

func TestApplyPatch3WayRefusesOverlappingConflict(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.name", "Test")
	git(t, repo, "config", "user.email", "test@example.com")
	path := filepath.Join(repo, "a.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "a.txt")
	git(t, repo, "commit", "-m", "initial")

	m, err := New(repo, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, _ := m.Create("first")
	os.WriteFile(filepath.Join(first.Path, "a.txt"), []byte("one\nTWO-A\nthree\n"), 0o644)
	patchFirst, err := first.Commit("a")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := m.Create("second")
	os.WriteFile(filepath.Join(second.Path, "a.txt"), []byte("one\nTWO-B\nthree\n"), 0o644)
	patchSecond, err := second.Commit("b")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyPatch3Way(patchFirst); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyPatch3Way(patchSecond); err == nil {
		t.Fatal("expected a conflict")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "one\nTWO-A\nthree\n" {
		t.Fatalf("a failed merge changed the worktree: %q", data)
	}
}

func TestApplyPatch3WayHandlesBinaryFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.name", "Test")
	git(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "a.txt")
	git(t, repo, "commit", "-m", "initial")
	m, err := New(repo, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wt, err := m.Create("binary")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "blob.bin"), []byte{0, 1, 2, 3, 255}, 0o644); err != nil {
		t.Fatal(err)
	}
	patch, err := wt.Commit("binary")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyPatch3Way(patch); err != nil {
		t.Fatalf("binary 3-way merge: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "blob.bin"))
	if err != nil || len(data) != 5 || data[4] != 255 {
		t.Fatalf("blob = %v err=%v", data, err)
	}
}

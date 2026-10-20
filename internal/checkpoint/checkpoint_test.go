package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotAndRestore(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "dir", "b.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := New(t.TempDir(), work)
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.Snapshot("test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("empty snapshot id")
	}
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(work, "dir", "b.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore(id); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(work, "a.txt"))
	if err != nil || string(data) != "one" {
		t.Fatalf("a.txt = %q err=%v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(work, "dir", "b.txt"))
	if err != nil || string(data) != "two" {
		t.Fatalf("dir/b.txt = %q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(work, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("new.txt should be removed, err=%v", err)
	}
}

func TestSnapshotPathOutside(t *testing.T) {
	work := t.TempDir()
	m, err := New(t.TempDir(), work)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Snapshot("x", []string{"../outside"}); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("expected outside error, got %v", err)
	}
}

func TestRootForSession(t *testing.T) {
	got := RootForSession("/tmp/home/sessions/x/s.jsonl")
	want := "/tmp/home/sessions/x/s.checkpoints"
	if got != want {
		t.Fatalf("RootForSession = %q, want %q", got, want)
	}
}

func TestScopedSnapshotDoesNotRemoveSiblings(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := New(t.TempDir(), work)
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.Snapshot("edit a", []string{"a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore(id); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(work, "a.txt"))
	if err != nil || string(data) != "a" {
		t.Fatalf("a.txt = %q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(work, "b.txt")); err != nil {
		t.Fatalf("b.txt should survive scoped restore: %v", err)
	}
}

// TestSnapshotSurvivesUnreadableDir makes sure one directory we cannot list
// (a protected trash, another user's folder) does not kill the snapshot.
func TestSnapshotSurvivesUnreadableDir(t *testing.T) {
	ws := t.TempDir()
	locked := filepath.Join(ws, "Protected")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	ok := filepath.Join(ws, "ok.txt")
	if err := os.WriteFile(ok, []byte("fine"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := New(t.TempDir(), ws)
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.Snapshot("test", nil)
	if err != nil {
		t.Fatalf("the snapshot failed on an unreadable directory: %v", err)
	}
	if err := m.Restore(id); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(ok); err != nil {
		t.Fatalf("the readable file was not restored: %v", err)
	}
}

func TestSkipListHidesSystemDirectories(t *testing.T) {
	for _, name := range []string{".Trash", "Library", ".Trashes", "$RECYCLE.BIN"} {
		if !skipDir(name) {
			t.Errorf("%s should be skipped", name)
		}
	}
}

package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestSnapshotSurvivesTooMuchData makes sure a workspace bigger than the
// snapshot limit does not fail the snapshot: it captures what it can and
// carries on.
func TestSnapshotSurvivesTooMuchData(t *testing.T) {
	ws := t.TempDir()
	// A file just over the per-file limit is skipped, and the total limit
	// stops the capture instead of failing it.
	big := make([]byte, 3<<20)
	for i := range big {
		big[i] = byte(i % 251)
	}
	for i := 0; i < 3; i++ {
		name := filepath.Join(ws, "big"+string(rune('a'+i))+".bin")
		if err := os.WriteFile(name, big, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ws, "small.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := New(t.TempDir(), ws)
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.Snapshot("test", nil)
	if err != nil {
		t.Fatalf("the snapshot must not fail on a big workspace: %v", err)
	}
	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 {
		t.Fatal("no checkpoint was written")
	}
	// a restore of a partial snapshot must not delete what it did not capture
	if err := m.Restore(id); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "small.txt")); err != nil {
		t.Fatalf("a captured file was lost: %v", err)
	}
}

// TestBigWorkspaceIsPartialAndFast mimics a home directory: more data than
// the snapshot can hold. It must not fail, must stop early, and a restore
// must not delete the files it never captured.
func TestBigWorkspaceIsPartialAndFast(t *testing.T) {
	ws := t.TempDir()
	data := make([]byte, 1<<20)
	for i := 0; i < 300; i++ {
		name := filepath.Join(ws, "f"+string(rune('a'+i%26))+string(rune('a'+i/26))+".bin")
		if err := os.WriteFile(name, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := New(t.TempDir(), ws)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	id, err := m.Snapshot("test", nil)
	if err != nil {
		t.Fatalf("the snapshot must not fail on a big workspace: %v", err)
	}
	if took := time.Since(start); took > 30*time.Second {
		t.Errorf("the snapshot took %s, it should stop early", took)
	}
	if err := m.Restore(id); err != nil {
		t.Fatalf("restore: %v", err)
	}
	entries, err := os.ReadDir(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 300 {
		t.Errorf("a partial restore deleted files: %d left", len(entries))
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := New(t.TempDir(), work)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := m.Snapshot("test", nil); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	removed, err := m.Prune(2)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list = %d, want 2", len(list))
	}
}

func TestSnapshotDeduplicatesContent(t *testing.T) {
	work := t.TempDir()
	data := []byte(strings.Repeat("same content\n", 1000))
	if err := os.WriteFile(filepath.Join(work, "a.txt"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	m, err := New(root, work)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Snapshot("one", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Snapshot("two", nil); err != nil {
		t.Fatal(err)
	}
	out, err := m.git("cat-file", "--batch-all-objects", "--batch-check=%(objecttype)")
	if err != nil {
		t.Fatal(err)
	}
	blobs := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "blob" {
			blobs++
		}
	}
	if blobs != 1 {
		t.Fatalf("blobs = %d, want one deduplicated blob", blobs)
	}
}

func TestAutomaticRetention(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	m, err := New(root, work)
	if err != nil {
		t.Fatal(err)
	}
	m.SetKeep(2)
	for i := 0; i < 4; i++ {
		if _, err := m.Snapshot("test", nil); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("retained %d snapshots, want 2", len(list))
	}
}

func TestPruneRemovesUnreferencedObjects(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	m, err := New(root, work)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Snapshot("test", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prune(0); err != nil {
		t.Fatal(err)
	}
	out, _ := m.git("rev-list", "--objects", "--all")
	if strings.TrimSpace(out) != "" {
		t.Fatalf("objects left after prune: %s", out)
	}
}

func TestScopedRestoreRemovesCreatedFile(t *testing.T) {
	work := t.TempDir()
	root := t.TempDir()
	m, err := New(root, work)
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.Snapshot("before create", []string{"new.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "new.txt"), []byte("created"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("created file was not removed: %v", err)
	}
}

func TestLegacySnapshotStillRestores(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	m, err := New(root, work)
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.snapshotLegacy("legacy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore(id); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(work, "a.txt"))
	if err != nil || string(data) != "one" {
		t.Fatalf("legacy restore = %q err=%v", data, err)
	}
}

func TestPruneRootHandlesGitSnapshots(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	m, err := New(root, work)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := m.Snapshot("test", nil); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	removed, err := PruneRoot(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
}

func TestSnapshotSkipsLargeUntrackedFile(t *testing.T) {
	work := t.TempDir()
	big := make([]byte, maxUntrackedBytes+1024)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(work, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "small.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	m, err := New(root, work)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Snapshot("test", nil); err != nil {
		t.Fatal(err)
	}
	list, err := m.List()
	if err != nil || len(list) == 0 {
		t.Fatalf("list = %v err=%v", list, err)
	}
	out, err := m.git("ls-tree", "-r", "--name-only", list[0].Commit)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "big.bin") {
		t.Fatalf("large untracked file was captured: %s", out)
	}
	if !strings.Contains(out, "small.txt") {
		t.Fatalf("small untracked file was not captured: %s", out)
	}
}

func TestUndoRestoresLastChanges(t *testing.T) {
	work := t.TempDir()
	path := filepath.Join(work, "a.txt")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("one")
	root := t.TempDir()
	m, err := New(root, work)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Snapshot("before two", []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	write("two")
	if _, err := m.Snapshot("before three", []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	write("three")
	if _, err := m.Snapshot("before four", []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	write("four")
	n, err := m.Undo(2)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("undone = %d, want 2", n)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "two" {
		t.Fatalf("after undo(2) = %q, want two", data)
	}
	list, _ := m.List()
	if len(list) != 1 {
		t.Fatalf("checkpoints after undo = %d, want 1", len(list))
	}
	if _, err := m.Undo(99); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "one" {
		t.Fatalf("after undo(all) = %q, want one", data)
	}
}

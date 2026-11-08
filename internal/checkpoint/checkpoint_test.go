package checkpoint

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

func TestShadowGitPreservesIgnoredFiles(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, ".gitignore"), []byte("*.mp4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "keep.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "clip.mp4"), []byte("ignored but present"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, ".wombat"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".wombat", "state.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := New(t.TempDir(), work)
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.Snapshot("ignored", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "keep.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "clip.mp4"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore(id); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(work, "keep.txt")); err != nil || string(data) != "one" {
		t.Fatalf("keep.txt = %q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(work, "clip.mp4")); err != nil {
		t.Fatalf("ignored existing file was deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("new.txt should be removed, err=%v", err)
	}
	if data, err := os.ReadFile(filepath.Join(work, ".wombat", "state.json")); err != nil || string(data) != "{}" {
		t.Fatalf(".wombat state was touched: %q err=%v", data, err)
	}
}

func TestShadowGitReusesBlobs(t *testing.T) {
	work := t.TempDir()
	data := make([]byte, 2<<20)
	for i := range data {
		data[i] = byte((i * 31) % 251)
	}
	if err := os.WriteFile(filepath.Join(work, "big.bin"), data, 0o644); err != nil {
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
	before := dirSize(t, filepath.Join(root, "git"))
	if _, err := m.Snapshot("two", nil); err != nil {
		t.Fatal(err)
	}
	after := dirSize(t, filepath.Join(root, "git"))
	if after > before+(64<<10) {
		t.Fatalf("second snapshot grew the object store by %d bytes", after-before)
	}
}

func dirSize(t *testing.T, root string) int64 {
	t.Helper()
	var total int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return total
}

func TestUnsafeWorkspaceRefusesRootAndHome(t *testing.T) {
	if reason := unsafeWorkspace("/"); reason == "" {
		t.Fatal("the filesystem root must not be checkpointed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if reason := unsafeWorkspace(home); reason == "" {
		t.Fatal("the home directory must not be checkpointed")
	}
	if reason := unsafeWorkspace(filepath.Dir(home)); reason == "" {
		t.Fatal("the parent of the home directory must not be checkpointed")
	}
	work := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if reason := unsafeWorkspace(work); reason != "" {
		t.Fatalf("a normal project must be checkpointed, got %q", reason)
	}
}

func TestUnsafeWorkspaceSkipsSnapshots(t *testing.T) {
	m, err := New(t.TempDir(), "/")
	if err != nil {
		t.Fatal(err)
	}
	if m.DisabledReason() == "" {
		t.Fatal("root workspace should report why snapshots are off")
	}
	id, err := m.Snapshot("root", nil)
	if err != nil || id != "" {
		t.Fatalf("unsafe snapshot = %q, %v; want a silent skip", id, err)
	}
}

func TestScopedSnapshotStagesOnlyNamedPaths(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "a.txt"), "old-a")
	writeFile(t, filepath.Join(work, "b.txt"), "old-b")
	m, err := New(t.TempDir(), work)
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.Snapshot("first", nil)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := m.git("rev-list", "--count", first); err != nil || count != "1" {
		t.Fatalf("snapshot should be a root commit, count=%q err=%v", count, err)
	}

	writeFile(t, filepath.Join(work, "a.txt"), "new-a")
	writeFile(t, filepath.Join(work, "b.txt"), "new-b")
	second, err := m.Snapshot("second", []string{"a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if a, err := m.git("show", second+":a.txt"); err != nil || a != "new-a" {
		t.Fatalf("a.txt = %q err=%v, want the staged change", a, err)
	}
	if b, err := m.git("show", second+":b.txt"); err != nil || b != "old-b" {
		t.Fatalf("b.txt = %q err=%v, want the unstaged old content", b, err)
	}
}

func TestSnapshotRetentionKeepsNewest(t *testing.T) {
	work := t.TempDir()
	m, err := New(t.TempDir(), work)
	if err != nil {
		t.Fatal(err)
	}
	m.SetKeep(2)
	var ids []string
	for i := 0; i < 4; i++ {
		writeFile(t, filepath.Join(work, "a.txt"), string(rune('a'+i)))
		id, err := m.Snapshot("snap", []string{"a.txt"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("retained %d checkpoints, want 2", len(list))
	}
	refs, err := m.git("for-each-ref", "--format=%(refname)", "refs/checkpoints")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Fields(refs)); n != 2 {
		t.Fatalf("kept %d refs, want 2: %s", n, refs)
	}
	for _, old := range ids[:2] {
		if strings.Contains(refs, old) {
			t.Fatalf("pruned checkpoint %s still has a ref", old)
		}
	}
}

func TestSnapshotSizeLimitDisablesSnapshots(t *testing.T) {
	oldTotal := maxTotalBytes
	maxTotalBytes = 1 << 20
	t.Cleanup(func() { maxTotalBytes = oldTotal })

	work := t.TempDir()
	writeFile(t, filepath.Join(work, "big.bin"), strings.Repeat("x", 2<<20))
	m, err := New(t.TempDir(), work)
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.Snapshot("too big", nil)
	if err != nil || id != "" {
		t.Fatalf("oversized snapshot = %q, %v; want a silent skip", id, err)
	}
	if !strings.Contains(m.DisabledReason(), "larger than") {
		t.Fatalf("reason = %q, want a size explanation", m.DisabledReason())
	}
	// Even a later small edit stays skipped for this session.
	writeFile(t, filepath.Join(work, "small.txt"), "ok")
	if id, err := m.Snapshot("small", []string{"small.txt"}); err != nil || id != "" {
		t.Fatalf("snapshot after limit = %q, %v; want a skip", id, err)
	}
}

func TestRecoverRemovesAbandonedFirstAdd(t *testing.T) {
	work := t.TempDir()
	root := t.TempDir()
	m, err := New(root, work)
	if err != nil {
		t.Fatal(err)
	}
	objects := filepath.Join(m.gitDir, "objects")
	junk := filepath.Join(objects, "ab", "cdef")
	writeFile(t, junk, "left by an interrupted git add")
	tmp := filepath.Join(objects, "pack", "tmp_pack_leftover")
	writeFile(t, tmp, "left by an interrupted gc")
	old := time.Now().Add(-time.Hour)
	for _, path := range []string{
		junk, tmp, filepath.Dir(tmp), filepath.Dir(junk), objects,
	} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := New(root, work); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(junk); !os.IsNotExist(err) {
		t.Fatalf("abandoned object survived recovery: %v", err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("abandoned temp pack survived recovery: %v", err)
	}
	_ = m
}

func TestSessionsShareObjectStore(t *testing.T) {
	parent := t.TempDir()
	work := filepath.Join(parent, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 2<<20)
	for i := range data {
		data[i] = byte((i * 37) % 251)
	}
	if err := os.WriteFile(filepath.Join(work, "big.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := New(filepath.Join(parent, "one.checkpoints"), work)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Snapshot("first", nil); err != nil {
		t.Fatal(err)
	}
	before := sizeOf(first.gitDir)

	second, err := New(filepath.Join(parent, "two.checkpoints"), work)
	if err != nil {
		t.Fatal(err)
	}
	if second.gitDir != first.gitDir {
		t.Fatalf("sessions use different stores: %s vs %s", first.gitDir, second.gitDir)
	}
	if _, err := second.Snapshot("second", nil); err != nil {
		t.Fatal(err)
	}
	after := sizeOf(first.gitDir)
	if after > before+(64<<10) {
		t.Fatalf("a second session grew the shared store by %d bytes", after-before)
	}
}

func TestPruneKeepsOtherSessions(t *testing.T) {
	parent := t.TempDir()
	work := filepath.Join(parent, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "a.txt"), "one")

	first, err := New(filepath.Join(parent, "one.checkpoints"), work)
	if err != nil {
		t.Fatal(err)
	}
	keepID, err := first.Snapshot("keep", nil)
	if err != nil {
		t.Fatal(err)
	}

	second, err := New(filepath.Join(parent, "two.checkpoints"), work)
	if err != nil {
		t.Fatal(err)
	}
	second.SetKeep(1)
	oldID, err := second.Snapshot("old", nil)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "a.txt"), "two")
	newID, err := second.Snapshot("new", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Prune(1); err != nil {
		t.Fatal(err)
	}
	if _, err := second.git("rev-parse", "--verify", second.ref(newID)); err != nil {
		t.Fatalf("newest ref was pruned: %v", err)
	}
	if _, err := second.git("rev-parse", "--verify", second.ref(oldID)); err == nil {
		t.Fatalf("old ref %s survived retention", oldID)
	}
	if _, err := first.git("rev-parse", "--verify", first.ref(keepID)); err != nil {
		t.Fatalf("another session's ref was pruned: %v", err)
	}
}

func TestWorkspaceContainingWombatHomeSkipsSnapshots(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "sessions", "project", "session.checkpoints")
	m, err := New(root, home)
	if err != nil {
		t.Fatal(err)
	}
	if m.DisabledReason() == "" {
		t.Fatal("snapshotting Wombat's own session store must be refused")
	}
	if id, err := m.Snapshot("self", nil); err != nil || id != "" {
		t.Fatalf("self snapshot = %q, %v; want a silent skip", id, err)
	}
}

func TestPruneDropsUnsafeCheckpoints(t *testing.T) {
	work := t.TempDir()
	root := t.TempDir()
	writeFile(t, filepath.Join(work, "a.txt"), "one")
	m, err := New(root, work)
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.Snapshot("before", nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := m.load(id)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Workspace = "/"
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.metaDir, safeID(id)+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := m.Prune(100)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("purged %d checkpoints, want 1", removed)
	}
	if _, err := os.Stat(filepath.Join(m.metaDir, safeID(id)+".json")); !os.IsNotExist(err) {
		t.Fatalf("unsafe manifest survived: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(m.gitDir, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			t.Fatalf("unsafe object survived: %s", entry.Name())
		}
	}
}

// An existing session from the old build keeps its per-session repository and
// its chain commits. The new code must still list, restore, and extend it.
func TestLegacyRepositoryStillWorks(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(t.TempDir(), "old.checkpoints")
	gitDir := filepath.Join(root, "git")
	if err := os.MkdirAll(filepath.Join(root, "meta"), 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"--git-dir", gitDir, "--work-tree", work}, args...)...)
		cmd.Dir = work
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "init", "--bare", gitDir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	run("config", "user.name", "Wombat")
	run("config", "user.email", "wombat@localhost")
	run("config", "core.bare", "false")
	run("config", "core.worktree", work)
	writeFile(t, filepath.Join(work, "a.txt"), "one")
	run("add", "-A", "--", ".")
	run("commit", "-m", "old style")
	id := run("rev-parse", "HEAD")
	manifest := Manifest{ID: id, Workspace: work, Created: time.Now().UTC(), Full: true, Files: []File{{Path: "a.txt", Exists: true}}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "meta", safeID(id)+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := New(root, work)
	if err != nil {
		t.Fatal(err)
	}
	if !m.legacy {
		t.Fatal("old per-session repository was not detected")
	}
	if list, err := m.List(); err != nil || len(list) != 1 {
		t.Fatalf("legacy list = %d, %v; want 1", len(list), err)
	}
	writeFile(t, filepath.Join(work, "a.txt"), "two")
	if err := m.Restore(id); err != nil {
		t.Fatal(err)
	}
	if body, ok := fileBody(t, filepath.Join(work, "a.txt")); !ok || body != "one" {
		t.Fatalf("legacy restore = %q, %v", body, ok)
	}
	newID, err := m.Snapshot("new", []string{"a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if newID == "" {
		t.Fatal("legacy repository did not accept a new snapshot")
	}
	if count, err := m.git("rev-list", "--count", newID); err != nil || count != "1" {
		t.Fatalf("new legacy snapshot is not a root commit: %q, %v", count, err)
	}
}

func TestConcurrentSessionsShareStore(t *testing.T) {
	parent := t.TempDir()
	work := filepath.Join(parent, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "base.txt"), "base")
	a, err := New(filepath.Join(parent, "a.checkpoints"), work)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(filepath.Join(parent, "b.checkpoints"), work)
	if err != nil {
		t.Fatal(err)
	}
	if a.gitDir != b.gitDir {
		t.Fatalf("sessions do not share a store: %s vs %s", a.gitDir, b.gitDir)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for n, m := range map[int]*Manager{0: a, 1: b} {
		wg.Add(1)
		go func(n int, m *Manager) {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				path := filepath.Join(work, fmt.Sprintf("s%d-%d.txt", n, i))
				writeFile(t, path, fmt.Sprintf("s%d-%d", n, i))
				if _, err := m.Snapshot("concurrent", []string{path}); err != nil {
					errs <- err
					return
				}
			}
		}(n, m)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for _, m := range []*Manager{a, b} {
		list, err := m.List()
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 5 {
			t.Fatalf("session has %d checkpoints, want 5", len(list))
		}
	}
	refs, err := a.git("for-each-ref", "--format=%(refname)", "refs/checkpoints")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Fields(refs)); n != 10 {
		t.Fatalf("shared store has %d checkpoint refs, want 10", n)
	}
}

func TestPruneAllKeepsLiveSessionsAndRemovesOrphans(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "sessions", "proj")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()

	liveRoot := filepath.Join(project, "live.checkpoints")
	live, err := New(liveRoot, work)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		writeFile(t, filepath.Join(work, "a.txt"), fmt.Sprintf("v%d", i))
		if _, err := live.Snapshot("live", []string{"a.txt"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(project, "live.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	orphanRoot := filepath.Join(project, "orphan.checkpoints")
	orphan, err := New(orphanRoot, work)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "b.txt"), "orphan")
	if _, err := orphan.Snapshot("orphan", []string{"b.txt"}); err != nil {
		t.Fatal(err)
	}

	sessions, removed, freed, err := PruneAll(home, 1)
	if err != nil {
		t.Fatal(err)
	}
	if sessions != 2 {
		t.Fatalf("sessions = %d, want 2", sessions)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 retained-away checkpoint", removed)
	}
	if freed < 0 {
		t.Fatalf("freed = %d", freed)
	}
	if _, err := os.Stat(orphanRoot); !os.IsNotExist(err) {
		t.Fatalf("orphan checkpoint directory survived: %v", err)
	}
	list, err := live.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("live session kept %d checkpoints, want 1", len(list))
	}
}

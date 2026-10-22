package checkpoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileBody(t *testing.T, path string) (string, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false
		}
		t.Fatal(err)
	}
	return string(raw), true
}

// A checkpoint whose metadata vanished must fail loudly instead of restoring
// nothing and reporting success.
func TestFaultRestoreMissingManifest(t *testing.T) {
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
	if err := os.RemoveAll(filepath.Join(root, "meta")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "a.txt"), "changed")

	if err := m.Restore(id); err == nil {
		t.Fatal("restore without a manifest must return an error")
	}
	if body, ok := fileBody(t, filepath.Join(work, "a.txt")); !ok || body != "changed" {
		t.Fatalf("failed restore must not touch the workspace: a.txt=%q ok=%v", body, ok)
	}
}

// A manifest that does not parse must fail before anything is deleted.
func TestFaultRestoreCorruptManifest(t *testing.T) {
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
	meta := filepath.Join(root, "meta", id+".json")
	if err := os.WriteFile(meta, []byte("{\"id\": "), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "later.txt"), "keep me")

	if err := m.Restore(id); err == nil {
		t.Fatal("restore of a corrupt manifest must return an error")
	}
	if _, ok := fileBody(t, filepath.Join(work, "later.txt")); !ok {
		t.Fatal("failed restore must not delete files created after the snapshot")
	}
}

// One unreadable manifest must not hide every other checkpoint.
func TestFaultListSkipsCorruptManifest(t *testing.T) {
	work := t.TempDir()
	root := t.TempDir()
	writeFile(t, filepath.Join(work, "a.txt"), "one")
	m, err := New(root, work)
	if err != nil {
		t.Fatal(err)
	}
	good, err := m.Snapshot("good", nil)
	if err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(root, "meta", "corrupt.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := m.List()
	if err != nil {
		t.Fatalf("List must tolerate a corrupt manifest: %v", err)
	}
	found := false
	for _, manifest := range list {
		if manifest.ID == good {
			found = true
		}
	}
	if !found {
		t.Fatalf("List dropped the healthy checkpoint %s: %+v", good, list)
	}
}

// The shadow object store is the checkpoint. If it is gone, restore must
// refuse rather than delete files it can no longer vouch for.
func TestFaultRestoreMissingObjectStore(t *testing.T) {
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
	if err := os.RemoveAll(filepath.Join(root, "git")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "a.txt"), "changed")
	writeFile(t, filepath.Join(work, "later.txt"), "created after the snapshot")

	err = m.Restore(id)
	if err == nil {
		t.Fatal("restore without an object store must return an error")
	}
	if body, ok := fileBody(t, filepath.Join(work, "a.txt")); !ok || body != "changed" {
		t.Fatalf("failed restore must not rewrite files: a.txt=%q ok=%v", body, ok)
	}
	if _, ok := fileBody(t, filepath.Join(work, "later.txt")); !ok {
		t.Fatal("failed restore must not delete files created after the snapshot")
	}
}

// A manifest recorded for another directory must never be applied here.
func TestFaultRestoreForeignWorkspace(t *testing.T) {
	work := t.TempDir()
	other := t.TempDir()
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
	meta := filepath.Join(root, "meta", id+".json")
	raw, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Workspace = other
	patched, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meta, patched, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := m.Restore(id); err == nil {
		t.Fatal("restore into a foreign workspace must return an error")
	}
	if _, ok := fileBody(t, filepath.Join(work, "a.txt")); !ok {
		t.Fatal("foreign restore must not delete workspace files")
	}
}

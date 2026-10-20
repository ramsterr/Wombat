package eval

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProtectedPath(t *testing.T) {
	cases := map[string]bool{
		"sum_test.go":          true,
		"testdata/input.txt":   true,
		"tests/check.sh":       true,
		".github/workflows/ci": true,
		"go.mod":               true,
		"src/sum.go":           false,
		"README.md":            false,
	}
	for path, want := range cases {
		if got := protectedPath(path); got != want {
			t.Errorf("protectedPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestRestoreProtected(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "sum_test.go"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "sum.go"), []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	pristine, err := snapshotProtected(work)
	if err != nil || pristine == "" {
		t.Fatalf("snapshot = %q err=%v", pristine, err)
	}
	defer os.RemoveAll(pristine)
	if err := os.WriteFile(filepath.Join(work, "sum_test.go"), []byte("weakened"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "new_test.go"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "sum.go"), []byte("fixed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := restoreProtected(work, pristine); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(work, "sum_test.go"))
	if err != nil || string(data) != "original" {
		t.Fatalf("sum_test.go = %q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(work, "new_test.go")); !os.IsNotExist(err) {
		t.Fatalf("new_test.go should be removed")
	}
	data, err = os.ReadFile(filepath.Join(work, "sum.go"))
	if err != nil || string(data) != "fixed" {
		t.Fatalf("sum.go = %q err=%v", data, err)
	}
}

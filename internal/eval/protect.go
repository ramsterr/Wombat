package eval

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var protectedNames = map[string]bool{
	"go.mod": true, "go.sum": true, "Makefile": true,
	"package.json": true, "package-lock.json": true, "pnpm-lock.yaml": true, "yarn.lock": true,
	"Cargo.toml": true, "Cargo.lock": true, "pyproject.toml": true, "requirements.txt": true,
}

func snapshotProtected(workdir string) (string, error) {
	dir, err := os.MkdirTemp("", "wombat-eval-protect-")
	if err != nil {
		return "", err
	}
	found := false
	err = filepath.WalkDir(workdir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(workdir, path)
		if err != nil {
			return err
		}
		if !protectedPath(filepath.ToSlash(rel)) {
			return nil
		}
		found = true
		dst := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	if !found {
		os.RemoveAll(dir)
		return "", nil
	}
	return dir, nil
}

func restoreProtected(workdir, pristine string) error {
	if pristine == "" {
		return nil
	}
	var remove []string
	err := filepath.WalkDir(workdir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == workdir {
			return nil
		}
		rel, err := filepath.Rel(workdir, path)
		if err != nil {
			return err
		}
		if protectedPath(filepath.ToSlash(rel)) {
			remove = append(remove, path)
			if entry.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(remove, func(i, j int) bool { return len(remove[i]) > len(remove[j]) })
	for _, path := range remove {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return filepath.WalkDir(pristine, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(pristine, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(workdir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
}

func protectedPath(rel string) bool {
	rel = filepath.ToSlash(strings.TrimPrefix(rel, "./"))
	if rel == "" || rel == "." {
		return false
	}
	base := filepath.Base(rel)
	if strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "_test.py") || strings.HasSuffix(base, ".test.js") || strings.HasSuffix(base, ".spec.js") || strings.HasSuffix(base, ".test.ts") || strings.HasSuffix(base, ".spec.ts") {
		return true
	}
	if protectedNames[base] {
		return true
	}
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		switch part {
		case "testdata", "tests", "test", "spec", "specs", "e2e", "__tests__", ".github":
			return true
		}
	}
	return false
}

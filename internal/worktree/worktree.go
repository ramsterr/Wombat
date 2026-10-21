package worktree

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Manager struct {
	Repo string
	Root string
}

type Worktree struct {
	manager *Manager
	Path    string
	Branch  string
	Base    string
}

func New(repo, root string) (*Manager, error) {
	if strings.TrimSpace(repo) == "" {
		repo = "."
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return nil, err
	}
	top, err := gitOutput(abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("writers require a git repository: %w", err)
	}
	top = strings.TrimSpace(top)
	if root == "" {
		root = filepath.Join(os.TempDir(), "wombat-worktrees")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &Manager{Repo: top, Root: root}, nil
}

func (m *Manager) Create(id string) (*Worktree, error) {
	return m.CreateAt(id, "")
}

// CreateAt creates a worktree from base. An empty base means HEAD and
// requires a clean main workspace. A non-empty base is a temporary commit
// containing earlier writer merges, so later waves can build on them without
// moving the user's branch.
func (m *Manager) CreateAt(id, base string) (*Worktree, error) {
	if m == nil {
		return nil, errors.New("worktree manager is nil")
	}
	clean := sanitize(id)
	if clean == "" {
		clean = "writer"
	}
	if base == "" {
		status, err := gitOutput(m.Repo, "status", "--porcelain", "--untracked-files=no")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(status) != "" {
			return nil, errors.New("main workspace has tracked changes; commit or stash them before starting writers")
		}
		head, err := gitOutput(m.Repo, "rev-parse", "HEAD")
		if err != nil {
			return nil, err
		}
		base = strings.TrimSpace(head)
	}
	name := clean + "-" + randomSuffix()
	branch := "wombat/" + name
	path := filepath.Join(m.Root, name)
	if _, err := gitOutput(m.Repo, "worktree", "add", "-b", branch, path, base); err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}
	return &Worktree{manager: m, Path: path, Branch: branch, Base: base}, nil
}

// Head returns the current HEAD commit.
func (m *Manager) Head() (string, error) {
	if m == nil {
		return "", errors.New("worktree manager is nil")
	}
	out, err := gitOutput(m.Repo, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Clean reports whether the main workspace has no tracked or staged changes.
func (m *Manager) Clean() (bool, error) {
	if m == nil {
		return false, errors.New("worktree manager is nil")
	}
	status, err := gitOutput(m.Repo, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(status) == "", nil
}

// SnapshotIndex writes a dangling commit from the staged index, without
// moving any branch. Writer merges are staged while a plan runs, so this is
// the merged state later waves should branch from.
func (m *Manager) SnapshotIndex(message string) (string, error) {
	if m == nil {
		return "", errors.New("worktree manager is nil")
	}
	tree, err := gitOutput(m.Repo, "write-tree")
	if err != nil {
		return "", err
	}
	parent, err := gitOutput(m.Repo, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(message) == "" {
		message = "wombat merged writers"
	}
	commit, err := gitOutput(m.Repo, "-c", "user.name=Wombat", "-c", "user.email=wombat@localhost",
		"commit-tree", strings.TrimSpace(tree), "-p", strings.TrimSpace(parent), "-m", message)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(commit), nil
}

func (w *Worktree) Status() (string, error) {
	if w == nil {
		return "", errors.New("worktree is nil")
	}
	return gitOutput(w.Path, "status", "--porcelain")
}

func (w *Worktree) HasChanges() (bool, error) {
	status, err := w.Status()
	return strings.TrimSpace(status) != "", err
}

func (w *Worktree) Commit(message string) (string, error) {
	if w == nil {
		return "", errors.New("worktree is nil")
	}
	has, err := w.HasChanges()
	if err != nil {
		return "", err
	}
	if !has {
		return "", errors.New("writer made no changes")
	}
	if strings.TrimSpace(message) == "" {
		message = "wombat writer"
	}
	if _, err := gitOutput(w.Path, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := gitOutput(w.Path, "-c", "user.name=Wombat", "-c", "user.email=wombat@localhost", "commit", "-m", message); err != nil {
		return "", err
	}
	return w.Diff()
}

func (w *Worktree) ChangedFiles() ([]string, error) {
	if w == nil {
		return nil, errors.New("worktree is nil")
	}
	out, err := gitOutput(w.Path, "diff", "--name-only", w.Base+"..HEAD")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

func (w *Worktree) Diff() (string, error) {
	if w == nil {
		return "", errors.New("worktree is nil")
	}
	cmd := exec.Command("git", "diff", "--binary", w.Base+"..HEAD")
	cmd.Dir = w.Path
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git diff: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (w *Worktree) Remove() error {
	if w == nil || w.manager == nil {
		return nil
	}
	_, removeErr := gitOutput(w.manager.Repo, "worktree", "remove", "--force", w.Path)
	_, _ = gitOutput(w.manager.Repo, "branch", "-D", w.Branch)
	_, _ = gitOutput(w.manager.Repo, "worktree", "prune")
	return removeErr
}

func (m *Manager) CheckPatch(patch string) error {
	if strings.TrimSpace(patch) == "" {
		return errors.New("empty patch")
	}
	return runGitInput(m.Repo, patch, "apply", "--check", "--binary", "-")
}

func (m *Manager) ApplyPatch(patch string) error {
	if err := m.CheckPatch(patch); err != nil {
		return err
	}
	return runGitInput(m.Repo, patch, "apply", "--binary", "-")
}

// ApplyPatch3Way applies a patch using git's three-way fallback, so a writer
// whose base predates another merge still applies cleanly when the changes do
// not overlap. Git's --3way stages its result and leaves conflict markers on a
// real conflict, so this snapshots the affected files, restores them on
// failure, and unstages the result on success.
func (m *Manager) ApplyPatch3Way(patch string) error {
	if strings.TrimSpace(patch) == "" {
		return errors.New("empty patch")
	}
	paths, err := m.patchPaths(patch)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("patch names no files")
	}
	snaps := make([]fileSnapshot, 0, len(paths))
	for _, path := range paths {
		snap, err := snapshotFile(m.Repo, path)
		if err != nil {
			return err
		}
		snaps = append(snaps, snap)
	}
	if err := runGitInput(m.Repo, patch, "apply", "--3way", "--binary", "-"); err != nil {
		_ = runGitInput(m.Repo, "", append([]string{"reset", "-q", "--"}, paths...)...)
		for _, snap := range snaps {
			_ = snap.restore()
		}
		// Keep the index aligned with the restored worktree; the whole merge
		// batch is unstaged when it finishes.
		_ = runGitInput(m.Repo, "", append([]string{"add", "-A", "--"}, paths...)...)
		return err
	}
	return nil
}

// StagePatchPaths makes the index match the worktree for the files a patch
// touches. It is used after a merge is reverted so the next three-way merge
// sees a consistent index.
func (m *Manager) StagePatchPaths(patch string) error {
	paths, err := m.patchPaths(patch)
	if err != nil || len(paths) == 0 {
		return err
	}
	return runGitInput(m.Repo, "", append([]string{"add", "-A", "--"}, paths...)...)
}

// Unstage returns the index to HEAD while keeping the merged files in the
// worktree. Writer merges are staged while a batch runs so later three-way
// merges have an index that matches the worktree.
func (m *Manager) Unstage() error {
	if m == nil {
		return nil
	}
	return runGitInput(m.Repo, "", "reset", "-q")
}

func (m *Manager) patchPaths(patch string) ([]string, error) {
	cmd := exec.Command("git", "apply", "--numstat", "-z", "--binary", "-")
	cmd.Dir = m.Repo
	cmd.Stdin = strings.NewReader(patch)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git apply --numstat: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	fields := strings.Split(stdout.String(), "\x00")
	seen := map[string]bool{}
	var paths []string
	for _, field := range fields {
		if field == "" {
			continue
		}
		// Each record starts with "<added>\t<deleted>\t"; renames put the
		// old and new paths in separate NUL fields.
		if i := strings.Index(field, "\t"); i >= 0 {
			rest := field[i+1:]
			if j := strings.Index(rest, "\t"); j >= 0 {
				field = rest[j+1:]
			}
		}
		for _, path := range strings.Split(field, "\x00") {
			path = strings.TrimSpace(path)
			if path != "" && !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}
	return paths, nil
}

type fileSnapshot struct {
	path   string
	data   []byte
	mode   os.FileMode
	exists bool
}

func snapshotFile(root, rel string) (fileSnapshot, error) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(root)+string(filepath.Separator)) && filepath.Clean(path) != filepath.Clean(root) {
		return fileSnapshot{}, fmt.Errorf("patch path escapes the workspace: %s", rel)
	}
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileSnapshot{path: path}, nil
	}
	if err != nil {
		return fileSnapshot{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fileSnapshot{}, err
	}
	return fileSnapshot{path: path, data: data, mode: st.Mode().Perm(), exists: true}, nil
}

func (s fileSnapshot) restore() error {
	if !s.exists {
		err := os.Remove(s.path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return os.WriteFile(s.path, s.data, s.mode)
}

func (m *Manager) ReversePatch(patch string) error {
	if strings.TrimSpace(patch) == "" {
		return errors.New("empty patch")
	}
	return runGitInput(m.Repo, patch, "apply", "-R", "--binary", "-")
}

func (m *Manager) DiffCheck() error {
	out, err := gitOutput(m.Repo, "diff", "HEAD", "--check")
	if err == nil && strings.TrimSpace(out) != "" {
		return fmt.Errorf("diff check: %s", strings.TrimSpace(out))
	}
	return err
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

func runGitInput(dir, input string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return nil
}

func randomSuffix() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", os.Getpid())
	}
	return hex.EncodeToString(b[:])
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

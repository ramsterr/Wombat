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
	if m == nil {
		return nil, errors.New("worktree manager is nil")
	}
	clean := sanitize(id)
	if clean == "" {
		clean = "writer"
	}
	status, err := gitOutput(m.Repo, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(status) != "" {
		return nil, errors.New("main workspace has tracked changes; commit or stash them before starting writers")
	}
	base, err := gitOutput(m.Repo, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	base = strings.TrimSpace(base)
	name := clean + "-" + randomSuffix()
	branch := "wombat/" + name
	path := filepath.Join(m.Root, name)
	if _, err := gitOutput(m.Repo, "worktree", "add", "-b", branch, path, base); err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}
	return &Worktree{manager: m, Path: path, Branch: branch, Base: base}, nil
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

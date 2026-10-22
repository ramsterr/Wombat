package checkpoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrPathOutside means a checkpoint path is not inside the workspace.
var ErrPathOutside = errors.New("checkpoint path is outside the workspace")

// File is one path recorded in a snapshot.
type File struct {
	Path   string      `json:"path"`
	Exists bool        `json:"exists"`
	Mode   os.FileMode `json:"mode,omitempty"`
	Size   int64       `json:"size,omitempty"`
	SHA256 string      `json:"sha256,omitempty"`
}

// Manifest describes one checkpoint. The actual file contents live in a
// private git object store under the checkpoint root; this file only records
// metadata, so repeated snapshots do not duplicate unchanged files.
type Manifest struct {
	ID        string    `json:"id"`
	Reason    string    `json:"reason,omitempty"`
	Workspace string    `json:"workspace"`
	Created   time.Time `json:"created"`
	Full      bool      `json:"full,omitempty"`
	Scope     []string  `json:"scope,omitempty"`
	Files     []File    `json:"files"`
	Skipped   []string  `json:"skipped,omitempty"`
	Partial   bool      `json:"partial,omitempty"`
}

// Manager stores checkpoints for one session. It uses a private git
// repository with the user's workspace as its work tree. Git stores each
// blob once, so snapshots only grow when file contents actually change.
type Manager struct {
	root      string
	workspace string
	gitDir    string
	metaDir   string
	disabled  bool

	mu struct {
		sync.Mutex
		last string
	}
}

// RootForSession returns the private checkpoint directory for a session.
func RootForSession(sessionPath string) string {
	return strings.TrimSuffix(sessionPath, filepath.Ext(sessionPath)) + ".checkpoints"
}

// New prepares a shadow git repository for workspace. If git is unavailable,
// checkpointing becomes a no-op instead of breaking edits.
func New(root, workspace string) (*Manager, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("checkpoint root is required")
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		root:      root,
		workspace: abs,
		gitDir:    filepath.Join(root, "git"),
		metaDir:   filepath.Join(root, "meta"),
	}
	if _, err := exec.LookPath("git"); err != nil {
		m.disabled = true
		return m, nil
	}
	if err := m.ensure(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) ensure() error {
	if m.disabled {
		return nil
	}
	if err := os.MkdirAll(m.metaDir, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(m.gitDir, "HEAD")); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(m.gitDir, 0o700); err != nil {
			return err
		}
		cmd := exec.Command("git", "init", "--bare", m.gitDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("init shadow git: %w: %s", err, strings.TrimSpace(string(out)))
		}
		for _, args := range [][]string{
			{"config", "user.name", "Wombat"},
			{"config", "user.email", "wombat@localhost"},
			{"config", "core.bare", "false"},
			{"config", "core.worktree", m.workspace},
			{"config", "core.autocrlf", "false"},
			{"config", "commit.gpgsign", "false"},
			{"config", "tag.gpgsign", "false"},
			{"config", "core.hooksPath", filepath.Join(m.gitDir, "hooks")},
		} {
			if _, err := m.git(args...); err != nil {
				return err
			}
		}
	}
	// Never snapshot Wombat's own state, and never let one huge generated
	// directory turn a checkpoint into a copy of the machine.
	exclude := filepath.Join(m.gitDir, "info", "exclude")
	_ = os.MkdirAll(filepath.Dir(exclude), 0o700)
	_ = os.WriteFile(exclude, []byte(".wombat/\n.git/\n"), 0o600)
	return nil
}

func (m *Manager) git(args ...string) (string, error) {
	base := []string{"--git-dir", m.gitDir, "--work-tree", m.workspace}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Dir = m.workspace
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (m *Manager) rel(path string) (string, error) {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(m.workspace, abs)
	}
	abs, err := filepath.Abs(abs)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(m.workspace, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %s", ErrPathOutside, path)
	}
	return filepath.ToSlash(rel), nil
}

// Snapshot commits the current workspace state into the shadow repository and
// returns the commit hash. With paths set, the manifest and restore are scoped
// to those paths; the object store may still reuse unchanged blobs.
func (m *Manager) Snapshot(reason string, paths []string) (string, error) {
	if m.disabled {
		return "", nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensure(); err != nil {
		return "", err
	}
	// --ignore-errors keeps one unreadable protected directory from making
	// the whole snapshot fail.
	_, _ = m.git("add", "-A", "--ignore-errors", "--", ".")
	if _, err := m.git("commit", "--allow-empty", "--no-verify", "-m", oneLine(reason)); err != nil {
		return "", err
	}
	id, err := m.git("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	manifest := Manifest{
		ID:        id,
		Reason:    oneLine(reason),
		Workspace: m.workspace,
		Created:   time.Now().UTC(),
		Full:      len(paths) == 0,
	}
	if manifest.Full {
		manifest.Files = m.scanFiles("")
	} else {
		for _, path := range paths {
			rel, err := m.rel(path)
			if err != nil {
				return "", err
			}
			manifest.Scope = append(manifest.Scope, rel)
			manifest.Files = append(manifest.Files, m.scanFiles(rel)...)
		}
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(m.metaDir, safeID(id)+".json"), raw, 0o600); err != nil {
		return "", err
	}
	m.mu.last = id
	return id, nil
}

// scanFiles lists the workspace paths a restore must preserve. Git still owns
// the file contents; this scan also remembers ignored files that existed at
// snapshot time so a full restore does not delete generated media merely
// because .gitignore kept it out of the object store.
func (m *Manager) scanFiles(scope string) []File {
	prefix := strings.Trim(scope, "/")
	var files []File
	_ = filepath.WalkDir(m.workspace, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if path != m.workspace && entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == m.workspace {
			return nil
		}
		if entry.IsDir() {
			if skipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(m.workspace, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if prefix != "" && rel != prefix && !strings.HasPrefix(rel, prefix+"/") {
			return nil
		}
		files = append(files, File{Path: rel, Exists: true})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

func (m *Manager) Last() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mu.last
}

func (m *Manager) List() ([]Manifest, error) {
	if m.disabled {
		return nil, nil
	}
	entries, err := os.ReadDir(m.metaDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Manifest
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(m.metaDir, entry.Name()))
		if err != nil {
			continue
		}
		var manifest Manifest
		if json.Unmarshal(raw, &manifest) != nil {
			continue
		}
		out = append(out, manifest)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

func (m *Manager) Restore(id string) error {
	if m.disabled {
		return errors.New("checkpoints need git; install git to restore")
	}
	manifest, err := m.load(id)
	if err != nil {
		return err
	}
	if filepath.Clean(manifest.Workspace) != filepath.Clean(m.workspace) {
		return fmt.Errorf("checkpoint %s belongs to %s", id, manifest.Workspace)
	}
	keep := make(map[string]bool, len(manifest.Files))
	for _, file := range manifest.Files {
		keep[file.Path] = true
	}
	// Restore the recorded tree. A full checkpoint restores everything;
	// a scoped checkpoint only touches the paths named in the original edit.
	checkoutPaths := []string{"."}
	if !manifest.Full {
		checkoutPaths = manifest.Scope
	}
	for _, path := range checkoutPaths {
		if path == "" {
			continue
		}
		if _, err := m.git("checkout", "--force", manifest.ID, "--", path); err != nil {
			// A scoped path that did not exist at snapshot time has nothing
			// to check out; the removal pass below still drops it.
			if strings.Contains(err.Error(), "did not match any file") {
				continue
			}
			return fmt.Errorf("restore %s: %w", id, err)
		}
	}
	// A full restore also removes files created after the snapshot. A
	// scoped restore only removes extras inside its scope.
	var remove []string
	err = filepath.WalkDir(m.workspace, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if path != m.workspace && entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == m.workspace {
			return nil
		}
		if entry.IsDir() {
			if skipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(m.workspace, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if keep[rel] {
			return nil
		}
		if manifest.Full || inScope(rel, manifest.Scope) {
			remove = append(remove, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, path := range remove {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (m *Manager) load(id string) (Manifest, error) {
	var manifest Manifest
	path := filepath.Join(m.metaDir, safeID(id)+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return manifest, fmt.Errorf("load checkpoint %s: %w", id, err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return manifest, fmt.Errorf("parse checkpoint %s: %w", id, err)
	}
	return manifest, nil
}

func inScope(rel string, scope []string) bool {
	for _, s := range scope {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	return false
}

func skipDir(name string) bool {
	switch name {
	case ".git", ".wombat", "node_modules", "vendor", "dist", "build", "target",
		".cache", ".venv", "__pycache__", ".Trash", ".Trashes", "Library",
		"$RECYCLE.BIN", "System Volume Information":
		return true
	}
	return false
}

func safeID(id string) string {
	return strings.NewReplacer("/", "-", string(filepath.Separator), "-").Replace(strings.TrimSpace(id))
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

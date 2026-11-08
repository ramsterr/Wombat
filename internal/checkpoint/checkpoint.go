package checkpoint

import (
	"crypto/sha1"
	"encoding/hex"
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

// Snapshot limits keep one runaway workspace (a home directory, a filesystem
// root, a folder full of generated media) from turning session storage into a
// copy of the machine. When a limit is crossed, checkpoints are skipped for the
// rest of the session; edits still work.
var (
	maxFiles            = 100_000
	maxFileBytes  int64 = 256 << 20
	maxTotalBytes int64 = 2 << 30
)

const (
	// staleTempAge is how old an interrupted git temporary file must be before
	// it is safe to remove.
	staleTempAge = 10 * time.Minute
	// idleRepoAge is how long an object store with no commits must be quiet
	// before it is treated as abandoned.
	idleRepoAge = 5 * time.Minute
)

// File is one path recorded in a snapshot.
type File struct {
	Path   string      `json:"path"`
	Exists bool        `json:"exists"`
	Mode   os.FileMode `json:"mode,omitempty"`
	Size   int64       `json:"size,omitempty"`
	SHA256 string      `json:"sha256,omitempty"`
}

// Manifest describes one checkpoint. The actual file contents live in a
// git object store shared by the workspace's sessions; this file only records
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

// Manager stores checkpoints for one session. Sessions in the same workspace
// share a git object store, with the workspace as its work tree. Git stores
// each blob once, so snapshots and sessions only grow when contents change.
type Manager struct {
	root      string
	workspace string
	gitDir    string
	metaDir   string
	indexFile string
	refPrefix string
	legacy    bool
	disabled  bool
	// skipSnapshots is set when the workspace is unsafe or too large. Listing
	// and restoring existing checkpoints still works; new ones are skipped.
	skipSnapshots bool
	reason        string
	keep          int

	mu struct {
		sync.Mutex
		last string
	}
}

// RootForSession returns the private checkpoint directory for a session.
func RootForSession(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return strings.TrimSuffix(sessionPath, filepath.Ext(sessionPath)) + ".checkpoints"
}

// New prepares a shadow git repository for workspace. If git is unavailable,
// or the workspace is a filesystem root or the user's home, snapshotting is
// disabled instead of filling session storage with the whole machine.
func New(root, workspace string) (*Manager, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("checkpoint root is required")
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	m := &Manager{
		root:      root,
		workspace: abs,
		metaDir:   filepath.Join(root, "meta"),
	}
	m.useStore(workspaceStore(root, abs))
	if _, err := exec.LookPath("git"); err != nil {
		m.disabled = true
		m.reason = "git is not installed"
		return m, nil
	}
	if reason := unsafeWorkspace(abs); reason != "" {
		m.skipSnapshots = true
		m.reason = reason
	} else if home := wombatHome(root); containsPath(abs, home) {
		// Snapshotting Wombat's own state would try to copy the object store
		// into itself.
		m.skipSnapshots = true
		m.reason = "checkpoints are off because the workspace contains Wombat's session store"
	}
	if !m.skipSnapshots || m.legacy {
		if err := m.ensure(); err != nil {
			return nil, err
		}
	}
	m.recover()
	return m, nil
}

// workspaceStore is the object store shared by every session in the same
// workspace. Deduplicating at this level means ten sessions in one repository
// store a file once, not once per session.
func workspaceStore(root, workspace string) string {
	h := sha1.Sum([]byte(filepath.Clean(workspace)))
	return filepath.Join(filepath.Dir(root), ".checkpoints-store-"+hex.EncodeToString(h[:])[:12])
}

// useStore points the manager at a shared object store, unless the session
// already has a legacy per-session repository from an older build.
func (m *Manager) useStore(store string) {
	legacy := filepath.Join(m.root, "git")
	if _, err := os.Stat(filepath.Join(legacy, "HEAD")); err == nil {
		m.gitDir = legacy
		m.legacy = true
		m.refPrefix = "refs/checkpoints/" + sessionKey(m.root)
		return
	}
	m.gitDir = filepath.Join(store, "git")
	m.indexFile = filepath.Join(m.root, "index")
	m.refPrefix = "refs/checkpoints/" + sessionKey(m.root)
	_ = os.MkdirAll(m.root, 0o700)
	_ = os.WriteFile(filepath.Join(m.root, "store"), []byte(store+"\n"), 0o600)
}

func sessionKey(root string) string {
	h := sha1.Sum([]byte(filepath.Clean(root)))
	return hex.EncodeToString(h[:])[:12]
}

// wombatHome recovers <home> from <home>/sessions/<project>/<session>.checkpoints.
func wombatHome(root string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(filepath.Clean(root))))
}

func containsPath(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func storedGitDir(root string) string {
	if _, err := os.Stat(filepath.Join(root, "git", "HEAD")); err == nil {
		return filepath.Join(root, "git")
	}
	raw, err := os.ReadFile(filepath.Join(root, "store"))
	if err != nil {
		return ""
	}
	store := strings.TrimSpace(string(raw))
	if store == "" {
		return ""
	}
	gitDir := filepath.Join(store, "git")
	if _, err := os.Stat(filepath.Join(gitDir, "HEAD")); err != nil {
		return ""
	}
	return gitDir
}

// DisabledReason reports why new checkpoints are not being taken, if they are
// not. An empty string means snapshots are active.
func (m *Manager) DisabledReason() string {
	if m == nil {
		return ""
	}
	return m.reason
}

// SetKeep sets how many snapshots to retain. Zero keeps every snapshot.
func (m *Manager) SetKeep(n int) {
	m.mu.Lock()
	m.keep = n
	m.mu.Unlock()
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
			// Keep unreachable objects from piling up after retention drops a
			// snapshot. The shadow repo only serves Wombat, so a short grace
			// period is enough.
			{"config", "gc.pruneExpire", "1.hour.ago"},
		} {
			if _, err := m.git(args...); err != nil {
				return err
			}
		}
	}
	// Never snapshot Wombat's own state, and never let one huge generated
	// directory turn a checkpoint into a copy of the machine. These patterns
	// match the directories scan() skips, so the manifest and the object store
	// agree on what a checkpoint covers.
	exclude := filepath.Join(m.gitDir, "info", "exclude")
	_ = os.MkdirAll(filepath.Dir(exclude), 0o700)
	_ = os.WriteFile(exclude, []byte(shadowExclude), 0o600)
	return nil
}

const shadowExclude = `.wombat/
.git/
node_modules/
vendor/
dist/
build/
target/
.cache/
.venv/
__pycache__/
.Trash/
.Trashes/
Library/
$RECYCLE.BIN/
System Volume Information/
`

// unsafeWorkspace names directories that must never be checkpointed wholesale:
// a filesystem root, the user's home, and the home's parent (/Users, /home).
func unsafeWorkspace(workspace string) string {
	ws := filepath.Clean(workspace)
	if resolved, err := filepath.EvalSymlinks(ws); err == nil {
		ws = resolved
	}
	root := filepath.Clean(filepath.VolumeName(ws) + string(filepath.Separator))
	if ws == root {
		return "checkpoints are off for the filesystem root (" + ws + ")"
	}
	if home, err := os.UserHomeDir(); err == nil {
		home = filepath.Clean(home)
		if resolved, err := filepath.EvalSymlinks(home); err == nil {
			home = resolved
		}
		switch ws {
		case home:
			return "checkpoints are off for the home directory (" + ws + ")"
		case filepath.Dir(home):
			return "checkpoints are off for " + ws + ", the parent of the home directory"
		}
	}
	rel, err := filepath.Rel(root, ws)
	if err != nil {
		return ""
	}
	parts := strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) < 2 {
		return "checkpoints are off for top-level system directory " + ws
	}
	return ""
}

func (m *Manager) git(args ...string) (string, error) {
	base := []string{"--literal-pathspecs", "--git-dir", m.gitDir, "--work-tree", m.workspace}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Dir = m.workspace
	if m.indexFile != "" {
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+m.indexFile)
	}
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

type scanResult struct {
	files     []File
	overLimit bool
	reason    string
}

// scan lists the workspace paths a restore must preserve. Git still owns the
// file contents; this scan also remembers ignored files that existed at
// snapshot time so a full restore does not delete generated media merely
// because .gitignore kept it out of the object store.
//
// A scoped scan walks only the named path; a full scan walks the workspace.
func (m *Manager) scan(scope string) scanResult {
	out := scanResult{}
	start := m.workspace
	prefix := ""
	if scope != "" {
		prefix = strings.Trim(scope, "/")
		start = filepath.Join(m.workspace, filepath.FromSlash(prefix))
		if _, err := os.Lstat(start); err != nil {
			// A path that does not exist yet has nothing to record. The
			// manifest scope still lets a restore remove it after it appears.
			return out
		}
	}
	var total int64
	_ = filepath.WalkDir(start, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if path != start && entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if path != start && skipDir(entry.Name()) {
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
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		if info.Mode().IsRegular() {
			if info.Size() > maxFileBytes {
				out.overLimit = true
				out.reason = fmt.Sprintf("%s is larger than %s", rel, humanBytes(maxFileBytes))
				return filepath.SkipAll
			}
			total += info.Size()
			if total > maxTotalBytes {
				out.overLimit = true
				out.reason = fmt.Sprintf("the workspace is larger than %s", humanBytes(maxTotalBytes))
				return filepath.SkipAll
			}
		}
		if len(out.files) >= maxFiles {
			out.overLimit = true
			out.reason = fmt.Sprintf("the workspace has more than %d files", maxFiles)
			return filepath.SkipAll
		}
		out.files = append(out.files, File{
			Path: rel, Exists: true, Mode: info.Mode().Perm(), Size: info.Size(),
		})
		return nil
	})
	sort.Slice(out.files, func(i, j int) bool { return out.files[i].Path < out.files[j].Path })
	return out
}

// Snapshot commits the current workspace state into the shadow repository and
// returns the commit hash. With paths set, only those paths are staged and the
// manifest and restore are scoped to them.
func (m *Manager) Snapshot(reason string, paths []string) (string, error) {
	if m.disabled || m.skipSnapshots {
		return "", nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensure(); err != nil {
		return "", err
	}

	var (
		files []File
		scope []string
	)
	if len(paths) == 0 {
		res := m.scan("")
		if res.overLimit {
			m.skip("checkpoints are off: " + res.reason)
			return "", nil
		}
		files = res.files
	} else {
		seen := make(map[string]bool, len(paths))
		for _, path := range paths {
			rel, err := m.rel(path)
			if err != nil {
				return "", err
			}
			if seen[rel] {
				continue
			}
			seen[rel] = true
			scope = append(scope, rel)
			res := m.scan(rel)
			if res.overLimit {
				m.skip("checkpoints are off: " + res.reason)
				return "", nil
			}
			files = append(files, res.files...)
		}
		files = dedupeFiles(files)
	}

	// Stage only the paths the mutation is about. Falling back to `add -A`
	// here was the reason a one-line edit used to copy an entire workspace.
	stage := []string{"add", "-A", "--ignore-errors", "--"}
	if len(scope) == 0 {
		stage = append(stage, ".")
	} else {
		existing := make([]string, 0, len(scope))
		for _, rel := range scope {
			abs := filepath.Join(m.workspace, filepath.FromSlash(rel))
			if _, err := os.Lstat(abs); err == nil {
				existing = append(existing, rel)
				continue
			}
			// A tracked file that is currently absent must still be staged as
			// a deletion. An untracked path that does not exist yet has
			// nothing to stage.
			if out, _ := m.git("ls-files", "--", rel); out != "" {
				existing = append(existing, rel)
			}
		}
		if len(existing) > 0 {
			stage = append(stage, existing...)
		} else {
			stage = nil
		}
	}
	if len(stage) > 0 {
		if _, err := m.git(stage...); err != nil {
			return "", err
		}
	}
	tree, err := m.git("write-tree")
	if err != nil {
		return "", err
	}
	msg := oneLine(reason)
	if msg == "" {
		msg = "checkpoint"
	}
	// Each snapshot is an independent root commit. That lets retention drop a
	// snapshot without rewriting newer history, so its objects can be pruned.
	id, err := m.git("commit-tree", tree, "-m", msg)
	if err != nil {
		return "", err
	}
	if _, err := m.git("update-ref", m.ref(id), id); err != nil {
		return "", err
	}

	manifest := Manifest{
		ID:        id,
		Reason:    oneLine(reason),
		Workspace: m.workspace,
		Created:   time.Now().UTC(),
		Full:      len(paths) == 0,
		Scope:     scope,
		Files:     files,
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(m.metaDir, safeID(id)+".json"), raw, 0o600); err != nil {
		return "", err
	}
	m.mu.last = id
	_, _ = m.pruneLocked(m.keep, false)
	_, _ = m.git("gc", "--auto", "--quiet")
	return id, nil
}

func (m *Manager) skip(reason string) {
	m.skipSnapshots = true
	m.reason = reason
}

func (m *Manager) ref(id string) string {
	return m.refPrefix + "/" + safeID(id)
}

func dedupeFiles(files []File) []File {
	seen := make(map[string]bool, len(files))
	out := files[:0]
	for _, file := range files {
		if seen[file.Path] {
			continue
		}
		seen[file.Path] = true
		out = append(out, file)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
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

// Prune removes all but the newest keep checkpoints and reclaims their objects.
// A keep value of zero keeps every checkpoint.
func (m *Manager) Prune(keep int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.busy() {
		return 0, nil
	}
	return m.pruneLocked(keep, true)
}

func (m *Manager) pruneLocked(keep int, aggressive bool) (int, error) {
	if m.disabled {
		return 0, nil
	}
	if aggressive {
		if purged, err := m.purgeUnsafeLocked(); err != nil {
			return 0, err
		} else if purged > 0 {
			return purged, nil
		}
	}
	if keep <= 0 {
		return 0, nil
	}
	list, err := m.List()
	if err != nil {
		return 0, err
	}
	removed := 0
	for i, manifest := range list {
		if i < keep {
			continue
		}
		_ = os.Remove(filepath.Join(m.metaDir, safeID(manifest.ID)+".json"))
		_, _ = m.git("update-ref", "-d", m.ref(manifest.ID))
		removed++
	}
	if removed == 0 {
		return 0, nil
	}
	if aggressive {
		// Pack live objects and drop unreachable snapshots. The shared store
		// may be in use by another session, so keep the one-hour grace.
		_, _ = m.git("gc", "--quiet", "--prune=1.hour.ago")
	} else {
		_, _ = m.git("prune", "--expire=1.hour.ago")
		_, _ = m.git("gc", "--auto", "--quiet")
	}
	return removed, nil
}

// purgeUnsafeLocked deletes checkpoints taken before a workspace was known to
// be unsafe. An old build could snapshot a whole filesystem before the guard
// existed; those snapshots are huge, unrestorable in practice, and are removed
// rather than merely aged out.
func (m *Manager) purgeUnsafeLocked() (int, error) {
	list, err := m.List()
	if err != nil || len(list) == 0 {
		return 0, err
	}
	unsafe := false
	for _, manifest := range list {
		if unsafeWorkspace(manifest.Workspace) != "" || containsPath(manifest.Workspace, wombatHome(m.root)) {
			unsafe = true
			break
		}
	}
	if !unsafe || m.busy() {
		return 0, nil
	}
	if out, err := m.git("for-each-ref", "--format=%(refname)"); err == nil {
		for _, ref := range strings.Fields(out) {
			_, _ = m.git("update-ref", "-d", ref)
		}
	}
	removeMeta(m.metaDir)
	_ = os.Remove(filepath.Join(m.root, "index"))
	if !m.legacy {
		store := filepath.Dir(m.gitDir)
		matches, _ := filepath.Glob(filepath.Join(filepath.Dir(store), "*.checkpoints"))
		for _, root := range matches {
			if filepath.Clean(root) == filepath.Clean(m.root) {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(root, "store"))
			if err != nil || strings.TrimSpace(string(raw)) != store {
				continue
			}
			removeMeta(filepath.Join(root, "meta"))
			_ = os.Remove(filepath.Join(root, "index"))
		}
	}
	_ = os.RemoveAll(filepath.Join(m.gitDir, "objects"))
	_ = os.MkdirAll(filepath.Join(m.gitDir, "objects"), 0o700)
	_ = os.Remove(filepath.Join(m.gitDir, "packed-refs"))
	return len(list), nil
}

func removeMeta(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
}

// PruneRoot prunes a checkpoint directory by path, without knowing its
// workspace, and returns how many snapshots were removed and how many bytes
// were reclaimed. Abandoned object stores from interrupted first snapshots are
// cleaned up along the way.
func PruneRoot(root string, keep int) (removed int, freed int64, err error) {
	if strings.TrimSpace(root) == "" {
		return 0, 0, errors.New("checkpoint root is required")
	}
	before := sizeOf(root)
	m, err := managerForRoot(root)
	if err != nil {
		return 0, 0, err
	}
	if m.gitDir == "" || m.disabled {
		return 0, 0, nil
	}
	m.keep = keep
	m.recover()
	removed, err = m.Prune(keep)
	freed = sizeOf(root) - before
	if freed < 0 {
		freed = 0
	}
	return removed, freed, err
}

// managerForRoot opens an existing checkpoint directory without creating a new
// repository. It returns a manager with an empty gitDir when there is nothing
// to prune.
func managerForRoot(root string) (*Manager, error) {
	m := &Manager{
		root:      root,
		workspace: root,
		metaDir:   filepath.Join(root, "meta"),
		refPrefix: "refs/checkpoints/" + sessionKey(root),
	}
	if _, err := exec.LookPath("git"); err != nil {
		m.disabled = true
		return m, nil
	}
	m.gitDir = storedGitDir(root)
	if m.gitDir == "" {
		return m, nil
	}
	if _, err := os.Stat(filepath.Join(root, "git", "HEAD")); err == nil {
		m.legacy = true
	} else {
		m.indexFile = filepath.Join(root, "index")
	}
	return m, nil
}

// PruneAll prunes every session checkpoint under home and removes checkpoint
// directories whose session file no longer exists.
func PruneAll(home string, keep int) (sessions, removed int, freed int64, err error) {
	sessionsDir := filepath.Join(home, "sessions")
	before := sizeOf(sessionsDir)
	roots, err := filepath.Glob(filepath.Join(sessionsDir, "*", "*.checkpoints"))
	if err != nil {
		return 0, 0, 0, err
	}
	stores := map[string]bool{}
	for _, root := range roots {
		sessions++
		sessionPath := strings.TrimSuffix(root, ".checkpoints") + ".jsonl"
		if _, statErr := os.Stat(sessionPath); errors.Is(statErr, os.ErrNotExist) {
			if m, err := managerForRoot(root); err == nil && m.gitDir != "" {
				m.deleteRefs()
				stores[m.gitDir] = true
			}
			if err := os.RemoveAll(root); err != nil {
				return sessions, removed, freed, err
			}
			continue
		}
		n, _, err := PruneRoot(root, keep)
		removed += n
		if err != nil {
			return sessions, removed, freed, err
		}
		if m, err := managerForRoot(root); err == nil && m.gitDir != "" {
			stores[m.gitDir] = true
		}
	}
	for gitDir := range stores {
		m := &Manager{gitDir: gitDir, workspace: filepath.Dir(gitDir)}
		if _, err := exec.LookPath("git"); err != nil {
			break
		}
		if m.busy() || !m.idle(time.Minute) {
			continue
		}
		_, _ = m.git("gc", "--quiet", "--prune=1.hour.ago")
	}
	freed = before - sizeOf(sessionsDir)
	if freed < 0 {
		freed = 0
	}
	return sessions, removed, freed, nil
}

func (m *Manager) deleteRefs() {
	if m == nil || m.disabled || m.gitDir == "" {
		return
	}
	out, err := m.git("for-each-ref", "--format=%(refname)", m.refPrefix)
	if err != nil {
		return
	}
	for _, ref := range strings.Fields(out) {
		_, _ = m.git("update-ref", "-d", ref)
	}
}

// recover drops temporary files abandoned by interrupted git commands and,
// for a repository that never got its first commit, the loose objects an
// interrupted `git add` left behind.
func (m *Manager) recover() {
	if m.disabled {
		return
	}
	if m.busy() {
		return
	}
	// Check idleness before removing stale temporary files: a removal updates
	// the directory mtime and would make the object store look active.
	idle := m.idle(idleRepoAge)
	m.removeStaleTemp()
	if !idle || m.hasCommits() {
		return
	}
	_ = os.RemoveAll(filepath.Join(m.gitDir, "objects"))
	_ = os.MkdirAll(filepath.Join(m.gitDir, "objects"), 0o700)
	m.removeSessionIndexes()
}

// removeSessionIndexes drops staging files left by first snapshots that never
// committed. Without their objects the indexes cannot be used.
func (m *Manager) removeSessionIndexes() {
	_ = os.Remove(filepath.Join(m.gitDir, "index"))
	if m.legacy {
		return
	}
	store := filepath.Dir(m.gitDir)
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(store), "*.checkpoints"))
	for _, root := range matches {
		raw, err := os.ReadFile(filepath.Join(root, "store"))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(raw)) != store {
			continue
		}
		_ = os.Remove(filepath.Join(root, "index"))
	}
}

func (m *Manager) hasCommits() bool {
	if out, err := m.git("for-each-ref", "--count=1", "--format=%(refname)", "refs/checkpoints"); err == nil && out != "" {
		return true
	}
	_, err := m.git("rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

func (m *Manager) busy() bool {
	locks := []string{
		filepath.Join(m.gitDir, "index.lock"),
		filepath.Join(m.gitDir, "HEAD.lock"),
		filepath.Join(m.gitDir, "packed-refs.lock"),
		filepath.Join(m.gitDir, "gc.pid"),
	}
	if m.indexFile != "" {
		locks = append(locks, m.indexFile+".lock")
	}
	for _, path := range locks {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	if !m.legacy {
		// Every session sharing this store stages through its own index.
		store := filepath.Dir(m.gitDir)
		matches, _ := filepath.Glob(filepath.Join(filepath.Dir(store), "*.checkpoints", "index.lock"))
		if len(matches) > 0 {
			return true
		}
	}
	locked := false
	_ = filepath.WalkDir(filepath.Join(m.gitDir, "refs"), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(entry.Name(), ".lock") {
			locked = true
			return filepath.SkipAll
		}
		return nil
	})
	return locked
}

// idle reports whether the object store has been quiet for at least age.
func (m *Manager) idle(age time.Duration) bool {
	newest := time.Time{}
	record := func(dir string) {
		if info, err := os.Stat(dir); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	objects := filepath.Join(m.gitDir, "objects")
	record(objects)
	record(filepath.Join(objects, "pack"))
	entries, _ := os.ReadDir(objects)
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) == 2 {
			record(filepath.Join(objects, entry.Name()))
		}
	}
	return newest.IsZero() || time.Since(newest) >= age
}

func (m *Manager) removeStaleTemp() {
	cutoff := time.Now().Add(-staleTempAge)
	for _, dir := range []string{filepath.Join(m.gitDir, "objects"), m.gitDir} {
		_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			if !strings.HasPrefix(entry.Name(), "tmp_") {
				return nil
			}
			info, err := entry.Info()
			if err != nil || !info.ModTime().Before(cutoff) {
				return nil
			}
			_ = os.Remove(path)
			return nil
		})
	}
}

func sizeOf(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
}

func (m *Manager) Restore(id string) error {
	if m.disabled {
		return errors.New("checkpoints need git; install git to restore")
	}
	manifest, err := m.load(id)
	if err != nil {
		return err
	}
	if !sameWorkspace(manifest.Workspace, m.workspace) {
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

func sameWorkspace(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, ea := filepath.EvalSymlinks(a)
	rb, eb := filepath.EvalSymlinks(b)
	return ea == nil && eb == nil && filepath.Clean(ra) == filepath.Clean(rb)
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

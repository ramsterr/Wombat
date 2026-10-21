package checkpoint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxUntrackedBytes matches OpenCode's default for untracked files. Tracked
// files are always captured. Skipping a huge untracked download keeps video
// and model files out of the snapshot objects.
const maxUntrackedBytes = 2 << 20

// gitMeta is the small sidecar record for one shadow-Git snapshot. The tree
// itself lives in Git objects; this file only names the scope and reason.
type gitMeta struct {
	ID      string    `json:"id"`
	Commit  string    `json:"commit"`
	Reason  string    `json:"reason,omitempty"`
	Scope   []string  `json:"scope,omitempty"`
	Created time.Time `json:"created"`
}

func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func (m *Manager) git(args ...string) (string, error) {
	return m.gitInput(nil, args...)
}

func (m *Manager) gitInput(input []byte, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = m.workspace
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	env := append(os.Environ(),
		"GIT_DIR="+m.gitDir,
		"GIT_AUTHOR_NAME=wombat",
		"GIT_AUTHOR_EMAIL=wombat@localhost",
		"GIT_COMMITTER_NAME=wombat",
		"GIT_COMMITTER_EMAIL=wombat@localhost",
	)
	if m.workspace != "" {
		env = append(env, "GIT_WORK_TREE="+m.workspace)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (m *Manager) ensureGit() error {
	if !m.gitOK {
		return errors.New("git is not available")
	}
	if _, err := os.Stat(filepath.Join(m.gitDir, "HEAD")); err == nil {
		return nil
	}
	if err := os.MkdirAll(m.gitDir, 0o700); err != nil {
		return err
	}
	if _, err := m.git("init", "--quiet"); err != nil {
		return err
	}
	for _, kv := range [][2]string{
		{"core.bare", "false"},
		{"core.worktree", m.workspace},
		{"core.autocrlf", "false"},
		{"core.fsmonitor", "false"},
		{"core.untrackedCache", "true"},
		{"feature.manyFiles", "true"},
		{"index.version", "4"},
		{"gc.auto", "0"},
	} {
		if _, err := m.git("config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	exclude := strings.Join([]string{
		"# wombat snapshot excludes",
		".git",
		".wombat",
		"node_modules",
		"vendor",
		"dist",
		"build",
		"target",
		".cache",
		".venv",
		"__pycache__",
		".Trash",
		".Trashes",
		"Library",
		"$RECYCLE.BIN",
		"System Volume Information",
	}, "\n") + "\n"
	if err := os.MkdirAll(filepath.Join(m.gitDir, "info"), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.gitDir, "info", "exclude"), []byte(exclude), 0o600)
}

func (m *Manager) metaPath(id string) string {
	return filepath.Join(m.root, "meta", id+".json")
}

func (m *Manager) writeMeta(meta gitMeta) error {
	path := m.metaPath(meta.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func (m *Manager) loadMeta(id string) (gitMeta, bool) {
	raw, err := os.ReadFile(m.metaPath(id))
	if err != nil {
		return gitMeta{}, false
	}
	var meta gitMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return gitMeta{}, false
	}
	return meta, true
}

// snapshotPaths lists tracked files plus non-ignored untracked files that are
// small enough to snapshot. This mirrors OpenCode: tracked files are always
// captured, while a huge untracked download or video is left out.
func (m *Manager) snapshotPaths() ([]string, error) {
	tracked, err := m.git("ls-files", "-z")
	if err != nil {
		return nil, err
	}
	untracked, err := m.git("ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0)
	seen := map[string]bool{}
	for _, p := range strings.Split(tracked, "\x00") {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range strings.Split(untracked, "\x00") {
		if p == "" || seen[p] {
			continue
		}
		info, err := os.Stat(filepath.Join(m.workspace, filepath.FromSlash(p)))
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxUntrackedBytes {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out, nil
}

func (m *Manager) snapshotGit(reason string, paths []string) (string, error) {
	if err := m.ensureGit(); err != nil {
		return "", err
	}
	files, err := m.snapshotPaths()
	if err != nil {
		return "", err
	}
	if len(files) > 0 {
		var input bytes.Buffer
		for _, p := range files {
			input.WriteString(p)
			input.WriteByte(0)
		}
		if _, err := m.gitInput(input.Bytes(), "--literal-pathspecs", "add", "--all", "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
			return "", err
		}
	} else if _, err := m.git("add", "-A", "--"); err != nil {
		return "", err
	}
	tree, err := m.git("write-tree")
	if err != nil {
		return "", err
	}
	msg := "wombat checkpoint"
	if r := strings.TrimSpace(reason); r != "" {
		msg += ": " + oneLine(r, 400)
	}
	commit, err := m.git("commit-tree", tree, "-m", msg)
	if err != nil {
		return "", err
	}
	id := time.Now().UTC().Format("20060102-150405") + "-" + randomHex(4)
	if _, err := m.git("update-ref", "refs/wombat/"+id, commit); err != nil {
		return "", err
	}
	scope := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		rel, err := m.rel(p)
		if err != nil {
			_, _ = m.git("update-ref", "-d", "refs/wombat/"+id)
			return "", err
		}
		if !seen[rel] {
			seen[rel] = true
			scope = append(scope, rel)
		}
	}
	if err := m.writeMeta(gitMeta{ID: id, Commit: commit, Reason: reason, Scope: scope, Created: time.Now().UTC()}); err != nil {
		_, _ = m.git("update-ref", "-d", "refs/wombat/"+id)
		return "", err
	}
	return id, nil
}

func (m *Manager) listGit() ([]Manifest, error) {
	if !m.gitOK {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(m.gitDir, "HEAD")); err != nil {
		return nil, nil
	}
	out, err := m.git("for-each-ref", "--sort=-committerdate",
		"--format=%(refname:strip=2)%00%(objectname)%00%(committerdate:unix)%00%(subject)",
		"refs/wombat")
	if err != nil {
		return nil, err
	}
	var manifests []Manifest
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x00", 4)
		if len(parts) < 3 {
			continue
		}
		id, commit := parts[0], parts[1]
		manifest := Manifest{ID: id, Commit: commit, Full: true}
		if meta, ok := m.loadMeta(id); ok {
			manifest.ID = meta.ID
			manifest.Commit = meta.Commit
			manifest.Reason = meta.Reason
			manifest.Scope = meta.Scope
			manifest.Created = meta.Created
			manifest.Full = len(meta.Scope) == 0
		} else {
			manifest.Reason = parts[3]
			var unix int64
			_, _ = fmt.Sscanf(parts[2], "%d", &unix)
			manifest.Created = time.Unix(unix, 0).UTC()
		}
		manifests = append(manifests, manifest)
	}
	return manifests, nil
}

func (m *Manager) restoreGit(id string) (bool, error) {
	if !m.gitOK {
		return false, nil
	}
	commit := ""
	var scope []string
	if meta, ok := m.loadMeta(id); ok {
		commit = meta.Commit
		scope = meta.Scope
	}
	if commit == "" {
		for _, ref := range []string{"refs/wombat/" + id, id} {
			if out, err := m.git("rev-parse", "--verify", ref+"^{commit}"); err == nil {
				commit = out
				break
			}
		}
	}
	if commit == "" {
		return false, nil
	}
	if len(scope) == 0 {
		// Full restore: put back every path in the tree and remove files the
		// shadow index knows about that did not exist at this snapshot.
		targetOut, _ := m.git("ls-tree", "-r", "--name-only", "-z", commit)
		target := map[string]bool{}
		for _, p := range strings.Split(targetOut, "\x00") {
			if p != "" {
				target[p] = true
			}
		}
		if _, err := m.git("read-tree", "--reset", "-u", commit); err != nil {
			return true, err
		}
		if _, err := m.git("checkout-index", "-a", "-f"); err != nil {
			return true, err
		}
		all, _ := m.git("ls-files", "--cached", "--others", "--exclude-standard", "-z")
		for _, p := range strings.Split(all, "\x00") {
			if p != "" && !target[p] {
				_ = os.RemoveAll(filepath.Join(m.workspace, filepath.FromSlash(p)))
			}
		}
		return true, nil
	}
	for _, rel := range scope {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		if _, err := m.git("cat-file", "-e", commit+":"+rel); err == nil {
			if _, err := m.git("checkout", commit, "--", rel); err != nil {
				return true, err
			}
			continue
		}
		// The path did not exist at this point in time: remove what the
		// agent created there.
		_ = os.RemoveAll(filepath.Join(m.workspace, filepath.FromSlash(rel)))
	}
	return true, nil
}

func (m *Manager) pruneGit(keep int) (int, error) {
	list, err := m.listGit()
	if err != nil {
		return 0, err
	}
	if keep < 0 {
		keep = 0
	}
	removed := 0
	for i, manifest := range list {
		if i < keep {
			continue
		}
		if _, err := m.git("update-ref", "-d", "refs/wombat/"+manifest.ID); err != nil {
			return removed, err
		}
		_ = os.Remove(m.metaPath(manifest.ID))
		removed++
	}
	if removed > 0 {
		// Pack and drop unreachable objects. This is where Git compresses and
		// delta-encodes the snapshots.
		if _, err := m.git("gc", "--prune=now", "--quiet"); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

func (m *Manager) Snapshot(reason string, paths []string) (string, error) {
	m.mu.Lock()
	var id string
	var err error
	if m.gitOK {
		id, err = m.snapshotGit(reason, paths)
	} else {
		id, err = m.snapshotLegacy(reason, paths)
	}
	if err == nil {
		m.mu.last = id
	}
	keep := m.keep
	m.mu.Unlock()
	if err == nil && keep > 0 {
		_, _ = m.Prune(keep)
	}
	return id, err
}

func (m *Manager) Prune(keep int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := 0
	if m.gitOK {
		n, err := m.pruneGit(keep)
		if err != nil {
			return total, err
		}
		total += n
	}
	n, err := m.pruneLegacy(keep)
	if err != nil {
		return total, err
	}
	total += n
	return total, nil
}

func (m *Manager) List() ([]Manifest, error) {
	var out []Manifest
	if m.gitOK {
		git, err := m.listGit()
		if err != nil {
			return nil, err
		}
		out = append(out, git...)
	}
	legacy, err := m.listLegacy()
	if err != nil {
		return nil, err
	}
	out = append(out, legacy...)
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

func (m *Manager) Restore(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gitOK {
		if ok, err := m.restoreGit(id); ok {
			return err
		}
	}
	return m.restoreLegacy(id)
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if n > 0 && len(s) > n {
		return s[:n] + "…"
	}
	return s
}

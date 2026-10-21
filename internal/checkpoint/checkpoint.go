package checkpoint

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrPathOutside = errors.New("checkpoint path is outside the workspace")

type File struct {
	Path   string      `json:"path"`
	Exists bool        `json:"exists"`
	Mode   os.FileMode `json:"mode,omitempty"`
	Size   int64       `json:"size,omitempty"`
	SHA256 string      `json:"sha256,omitempty"`
}

type Manifest struct {
	ID        string    `json:"id"`
	Commit    string    `json:"commit,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Workspace string    `json:"workspace"`
	Created   time.Time `json:"created"`
	Full      bool      `json:"full,omitempty"`
	Scope     []string  `json:"scope,omitempty"`
	Files     []File    `json:"files"`
	Skipped   []string  `json:"skipped,omitempty"`
	// Partial marks a snapshot that hit the size limits: it holds what it
	// could capture, and a restore only puts back those files.
	Partial bool `json:"partial,omitempty"`
}

type Manager struct {
	root      string
	workspace string
	keep      int
	gitDir    string
	gitOK     bool
	mu        struct {
		sync.Mutex
		last string
	}
}

func New(root, workspace string) (*Manager, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("checkpoint root is required")
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	return &Manager{root: root, workspace: abs, gitDir: filepath.Join(root, "git"), gitOK: gitAvailable()}, nil
}

func RootForSession(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return strings.TrimSuffix(sessionPath, filepath.Ext(sessionPath)) + ".checkpoints"
}

func (m *Manager) Root() string { return m.root }

// SetKeep sets how many snapshots to retain. Zero keeps every snapshot.
func (m *Manager) SetKeep(n int) {
	m.mu.Lock()
	m.keep = n
	m.mu.Unlock()
}

// PruneRoot prunes a checkpoint directory by path, without a workspace.
func PruneRoot(root string, keep int) (int, error) {
	return (&Manager{root: root, gitDir: filepath.Join(root, "git"), gitOK: gitAvailable()}).Prune(keep)
}

func (m *Manager) objectPath(sum string) string {
	if len(sum) < 2 {
		return filepath.Join(m.root, "objects", sum)
	}
	return filepath.Join(m.root, "objects", sum[:2], sum)
}

// storeObject writes a file into the content-addressed object store and
// returns its hash. Identical content is stored once and hardlinked into
// each snapshot.
func (m *Manager) storeObject(src string, mode os.FileMode) (string, error) {
	sum, err := hashFile(src)
	if err != nil {
		return "", err
	}
	object := m.objectPath(sum)
	if _, err := os.Stat(object); err == nil {
		return sum, nil
	}
	if err := os.MkdirAll(filepath.Dir(object), 0o700); err != nil {
		return "", err
	}
	tmp := object + ".tmp-" + randomHex(4)
	if err := copyFile(src, tmp, 0o600); err != nil {
		return "", err
	}
	got, err := hashFile(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if got != sum {
		// The source changed while it was being copied.
		_ = os.Remove(tmp)
		sum = got
		object = m.objectPath(sum)
		if _, err := os.Stat(object); err == nil {
			return sum, nil
		}
	}
	if err := os.Rename(tmp, object); err != nil {
		_ = os.Remove(tmp)
		if _, statErr := os.Stat(object); statErr == nil {
			return sum, nil
		}
		return "", err
	}
	return sum, nil
}

func linkOrCopy(object, dst string, mode os.FileMode) error {
	if err := os.Link(object, dst); err == nil {
		return nil
	}
	return copyFile(object, dst, mode)
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

func (m *Manager) snapshotLegacy(reason string, paths []string) (string, error) {
	id := time.Now().UTC().Format("20060102-150405.000000000") + "-" + randomHex(4)
	dir := filepath.Join(m.root, id)
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o700); err != nil {
		return "", err
	}
	manifest := Manifest{ID: id, Reason: reason, Workspace: m.workspace, Created: time.Now().UTC()}
	var add func(path string) error
	add = func(path string) error {
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(m.workspace, abs)
		}
		abs, err := filepath.Abs(abs)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(m.workspace, abs)
		if err != nil {
			return err
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return fmt.Errorf("%w: %s", ErrPathOutside, path)
		}
		rel = filepath.ToSlash(rel)
		info, err := os.Lstat(abs)
		if errors.Is(err, os.ErrNotExist) {
			manifest.Files = append(manifest.Files, File{Path: rel, Exists: false})
			return nil
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			return filepath.WalkDir(abs, func(p string, entry os.DirEntry, err error) error {
				if manifest.Partial {
					// The limits are reached: stop walking, there is no
					// point enumerating the rest of a huge directory.
					return filepath.SkipAll
				}
				if err != nil {
					// One unreadable directory (a protected trash, another
					// user's folder) must not fail the whole snapshot.
					if p != abs {
						rel, rerr := filepath.Rel(m.workspace, p)
						if rerr == nil {
							manifest.Skipped = append(manifest.Skipped, filepath.ToSlash(rel)+" (unreadable)")
						}
						if entry != nil && entry.IsDir() {
							return filepath.SkipDir
						}
					}
					return nil
				}
				if p == abs {
					return nil
				}
				if entry.IsDir() {
					if skipDir(entry.Name()) {
						return filepath.SkipDir
					}
					return nil
				}
				return add(p)
			})
		}
		if info.Mode()&os.ModeSymlink != 0 {
			manifest.Skipped = append(manifest.Skipped, rel+" (symlink)")
			return nil
		}
		if !info.Mode().IsRegular() {
			manifest.Skipped = append(manifest.Skipped, rel+" (non-regular)")
			return nil
		}
		dst := filepath.Join(dir, "files", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		mode := info.Mode().Perm()
		sum, err := m.storeObject(abs, mode)
		if err != nil {
			// The object store is an optimisation; fall back to the old
			// direct copy rather than failing the checkpoint.
			if err := copyFile(abs, dst, mode); err != nil {
				return err
			}
			sum, err = hashFile(dst)
			if err != nil {
				return err
			}
		} else if err := linkOrCopy(m.objectPath(sum), dst, mode); err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, File{
			Path: rel, Exists: true, Mode: mode, Size: info.Size(), SHA256: sum,
		})
		return nil
	}
	if len(paths) == 0 {
		manifest.Full = true
		if err := add(m.workspace); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
	} else {
		for _, path := range paths {
			rel, err := m.rel(path)
			if err != nil {
				_ = os.RemoveAll(dir)
				return "", err
			}
			manifest.Scope = append(manifest.Scope, rel)
			if err := add(path); err != nil {
				_ = os.RemoveAll(dir)
				return "", err
			}
		}
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	m.mu.last = id
	return id, nil
}

func (m *Manager) Last() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mu.last
}

// Prune removes all but the newest keep checkpoints and returns the number
// removed. A checkpoint is only an undo point; pruning never touches the
// workspace.
func (m *Manager) pruneLegacy(keep int) (int, error) {
	if keep < 0 {
		keep = 0
	}
	list, err := m.listLegacy()
	if err != nil {
		return 0, err
	}
	removed := 0
	for i, manifest := range list {
		if i < keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(m.root, manifest.ID)); err != nil {
			return removed, err
		}
		removed++
	}
	if removed > 0 {
		if err := m.gcLegacy(); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// gcLegacy removes objects no remaining legacy snapshot references.
func (m *Manager) gcLegacy() error {
	list, err := m.listLegacy()
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, manifest := range list {
		for _, file := range manifest.Files {
			if file.SHA256 != "" {
				keep[file.SHA256] = true
			}
		}
	}
	root := filepath.Join(m.root, "objects")
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !keep[entry.Name()] {
			_ = os.Remove(path)
		}
		return nil
	})
}

func (m *Manager) listLegacy() ([]Manifest, error) {
	entries, err := os.ReadDir(m.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Manifest
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(m.root, entry.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			continue
		}
		out = append(out, manifest)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

func inScope(rel string, scope []string) bool {
	for _, s := range scope {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	return false
}

func (m *Manager) restoreLegacy(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("checkpoint id is required")
	}
	dir := filepath.Join(m.root, filepath.Base(id))
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("load checkpoint %s: %w", id, err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("parse checkpoint %s: %w", id, err)
	}
	if filepath.Clean(manifest.Workspace) != filepath.Clean(m.workspace) {
		return fmt.Errorf("checkpoint %s belongs to %s", id, manifest.Workspace)
	}
	keep := map[string]bool{}
	for _, file := range manifest.Files {
		keep[file.Path] = true
	}
	for _, skipped := range manifest.Skipped {
		if i := strings.Index(skipped, " ("); i > 0 {
			keep[skipped[:i]] = true
		}
	}
	var remove []string
	err = filepath.WalkDir(m.workspace, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			// A directory we cannot list (a protected trash) is left alone.
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
		// A partial snapshot only knows about the files it captured, so it
		// must not delete anything else.
		if manifest.Partial {
			return nil
		}
		if !keep[rel] && (manifest.Full || inScope(rel, manifest.Scope)) {
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
	for _, file := range manifest.Files {
		target := filepath.Join(m.workspace, filepath.FromSlash(file.Path))
		if !file.Exists {
			if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		src := filepath.Join(dir, "files", filepath.FromSlash(file.Path))
		mode := file.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := copyFile(src, target, mode); err != nil {
			return err
		}
	}
	return nil
}

func skipDir(name string) bool {
	switch name {
	case ".git", ".wombat", "node_modules", "vendor", "dist", "build", "target", ".cache", ".venv", "__pycache__":
		return true
	// Directories the operating system hides from ordinary processes.
	// macOS protects the trash and the per-user Library with TCC: listing
	// them fails with "Operation not permitted" even though you own them,
	// and a snapshot of the home directory would die on them.
	case ".Trash", ".Trashes", "Library", "$RECYCLE.BIN", "System Volume Information":
		return true
	}
	return false
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

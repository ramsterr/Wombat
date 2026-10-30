package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ApplyPatch applies a multi-file patch atomically. It accepts the patch
// format coding agents commonly emit:
//
//	*** Begin Patch
//	*** Add File: path
//	+content
//	*** Update File: path
//	@@ context
//	 unchanged
//	-old
//	+new
//	*** Delete File: path
//	*** End Patch
//
// Every file is checked and updated in memory first. If any hunk or write
// fails, the files that were already written are restored, so a patch never
// leaves a half-applied change behind.
type ApplyPatch struct{}

type patchOpKind int

const (
	patchAdd patchOpKind = iota
	patchUpdate
	patchDelete
)

type patchOp struct {
	kind    patchOpKind
	path    string
	content string
	hunks   []patchHunk
}

type patchHunk struct {
	lines []string
}

type applyPatchArgs struct {
	Patch string `json:"patch"`
}

func (t *ApplyPatch) Name() string  { return "apply_patch" }
func (t *ApplyPatch) Class() string { return "edit" }
func (t *ApplyPatch) Description() string {
	return "Apply one atomic patch across several files. The patch uses *** Begin Patch / *** Add File / *** Update File / *** Delete File / *** End Patch. Every file is validated before anything is written; a failure rolls back the files already changed. The permission prompt shows the combined diff."
}
func (t *ApplyPatch) Schema() map[string]any {
	return obj(map[string]any{
		"patch": map[string]any{"type": "string", "description": "the full patch text, from *** Begin Patch to *** End Patch"},
	}, "patch")
}

func (t *ApplyPatch) Run(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[applyPatchArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	ops, err := parsePatch(a.Patch)
	if err != nil {
		return "", err
	}
	ops, err = mergePatchOps(ops)
	if err != nil {
		return "", err
	}
	for _, op := range ops {
		if err := env.checkPolicy("edit", op.path); err != nil {
			return "", err
		}
	}
	type change struct {
		op       patchOp
		path     string
		exists   bool
		original []byte
		mode     os.FileMode
		result   []byte
		remove   bool
	}
	changes := make([]change, 0, len(ops))
	seen := map[string]bool{}
	var preview strings.Builder
	for _, op := range ops {
		if strings.TrimSpace(op.path) == "" {
			return "", fmt.Errorf("patch file path is empty")
		}
		if op.kind == patchUpdate && len(op.hunks) == 0 {
			return "", fmt.Errorf("%s has no hunks", op.path)
		}
		path, err := env.ResolvePath(op.path)
		if err != nil {
			return "", err
		}
		if seen[path] {
			return "", fmt.Errorf("%s appears more than once in the patch; combine it into one Update File", op.path)
		}
		seen[path] = true
		st, statErr := os.Stat(path)
		exists := statErr == nil
		var original []byte
		if exists {
			if err := env.checkOldContent(path); err != nil {
				return "", err
			}
			original, err = os.ReadFile(path)
			if err != nil {
				return "", err
			}
		}
		ch := change{op: op, path: path, exists: exists, original: original}
		if exists {
			ch.mode = st.Mode().Perm()
		}
		switch op.kind {
		case patchAdd:
			if exists {
				return "", fmt.Errorf("%s already exists; use Update File", op.path)
			}
			ch.result = []byte(op.content)
		case patchDelete:
			if !exists {
				return "", fmt.Errorf("%s does not exist", op.path)
			}
			ch.remove = true
		case patchUpdate:
			if !exists {
				return "", fmt.Errorf("%s does not exist; use Add File", op.path)
			}
			content := string(original)
			for i, h := range op.hunks {
				old, new := hunkText(h)
				if old == "" {
					return "", fmt.Errorf("%s hunk %d has no context or removed lines to locate it", op.path, i+1)
				}
				next, _, _, err := replaceTextIn(ch.path, content, old, new, false)
				if err != nil {
					return "", fmt.Errorf("%s hunk %d: %w", op.path, i+1, err)
				}
				content = next
			}
			ch.result = []byte(content)
		}
		if !ch.remove {
			if ch.mode == 0 {
				ch.mode = 0o644
			}
			preview.WriteString(DiffPreview(op.path, string(ch.original), string(ch.result)))
			preview.WriteString("\n")
		} else {
			preview.WriteString(DiffPreview(op.path, string(ch.original), ""))
			preview.WriteString("\n")
		}
		changes = append(changes, ch)
	}
	paths := make([]string, 0, len(changes))
	for _, ch := range changes {
		paths = append(paths, ch.op.path)
	}
	if err := env.CheckPaths("edit", paths, strings.TrimSpace(preview.String())); err != nil {
		return "", err
	}
	if err := env.Snapshot("before apply patch", paths...); err != nil {
		return "", err
	}
	written := make([]change, 0, len(changes))
	rollback := func() {
		for i := len(written) - 1; i >= 0; i-- {
			ch := written[i]
			if ch.exists {
				_ = os.WriteFile(ch.path, ch.original, ch.mode)
			} else {
				_ = os.Remove(ch.path)
			}
		}
	}
	for _, ch := range changes {
		if ch.remove {
			if err := os.Remove(ch.path); err != nil {
				rollback()
				return "", fmt.Errorf("delete %s: %w", ch.op.path, err)
			}
			written = append(written, ch)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(ch.path), 0o755); err != nil {
			rollback()
			return "", err
		}
		existed := ch.exists
		if err := os.WriteFile(ch.path, ch.result, ch.mode); err != nil {
			rollback()
			return "", fmt.Errorf("write %s: %w", ch.op.path, err)
		}
		ch.exists = existed
		written = append(written, ch)
	}
	var actions []string
	for _, ch := range changes {
		switch {
		case ch.remove:
			actions = append(actions, "deleted "+ch.op.path)
		case ch.exists:
			actions = append(actions, "updated "+ch.op.path)
		default:
			actions = append(actions, "added "+ch.op.path)
		}
	}
	out := fmt.Sprintf("applied patch (%d file(s)): %s", len(changes), strings.Join(actions, ", "))
	for _, ch := range changes {
		if ch.remove {
			continue
		}
		if w := secretWarning(env, ch.path); w != "" {
			out = w + "\n" + out
		}
	}
	return out, nil
}

// mergePatchOps combines repeated Update File sections for the same path.
// Models often emit one section per edit; that is safe to apply in order and
// is better than failing the whole patch.
func mergePatchOps(ops []patchOp) ([]patchOp, error) {
	merged := make([]patchOp, 0, len(ops))
	index := map[string]int{}
	for _, op := range ops {
		key := filepath.Clean(op.path)
		if i, ok := index[key]; ok {
			if op.kind != patchUpdate || merged[i].kind != patchUpdate {
				return nil, fmt.Errorf("%s appears more than once in the patch; combine it into one Update File", op.path)
			}
			merged[i].hunks = append(merged[i].hunks, op.hunks...)
			continue
		}
		index[key] = len(merged)
		merged = append(merged, op)
	}
	return merged, nil
}

func hunkText(h patchHunk) (old, new string) {
	var oldLines, newLines []string
	for _, line := range h.lines {
		if line == "" {
			line = " "
		}
		switch line[0] {
		case ' ':
			oldLines = append(oldLines, line[1:])
			newLines = append(newLines, line[1:])
		case '-':
			oldLines = append(oldLines, line[1:])
		case '+':
			newLines = append(newLines, line[1:])
		}
	}
	return strings.Join(oldLines, "\n"), strings.Join(newLines, "\n")
}

func parsePatch(text string) ([]patchOp, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var ops []patchOp
	var current *patchOp
	var hunk *patchHunk
	begun := false
	finishHunk := func() {
		if current != nil && hunk != nil {
			current.hunks = append(current.hunks, *hunk)
			hunk = nil
		}
	}
	finishOp := func() {
		finishHunk()
		if current != nil {
			ops = append(ops, *current)
			current = nil
		}
	}
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "*** Begin Patch"):
			begun = true
		case strings.HasPrefix(line, "*** End Patch"):
			finishOp()
			if !begun {
				return nil, fmt.Errorf("patch is missing *** Begin Patch")
			}
			if len(ops) == 0 {
				return nil, fmt.Errorf("patch has no file operations")
			}
			return ops, nil
		case strings.HasPrefix(line, "*** Add File: "):
			finishOp()
			current = &patchOp{kind: patchAdd, path: strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))}
		case strings.HasPrefix(line, "*** Update File: "):
			finishOp()
			current = &patchOp{kind: patchUpdate, path: strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))}
		case strings.HasPrefix(line, "*** Delete File: "):
			finishOp()
			current = &patchOp{kind: patchDelete, path: strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))}
		case strings.HasPrefix(line, "*** Move to: "):
			finishHunk()
		case strings.HasPrefix(line, "*** "):
			return nil, fmt.Errorf("unknown patch directive: %s", line)
		case current == nil:
			if strings.TrimSpace(line) == "" {
				continue
			}
			return nil, fmt.Errorf("patch content outside a file operation: %s", line)
		case current.kind == patchAdd:
			if strings.HasPrefix(line, "+") {
				current.content += line[1:] + "\n"
			} else if strings.TrimSpace(line) == "" {
				current.content += "\n"
			} else {
				return nil, fmt.Errorf("added lines must start with +: %s", line)
			}
		case current.kind == patchDelete:
			if strings.TrimSpace(line) != "" {
				return nil, fmt.Errorf("delete operation cannot contain content: %s", line)
			}
		case current.kind == patchUpdate:
			if strings.HasPrefix(line, "@@") {
				finishHunk()
				hunk = &patchHunk{}
				continue
			}
			if hunk == nil {
				hunk = &patchHunk{}
			}
			if strings.HasPrefix(line, "\\") {
				continue
			}
			if line == "" {
				line = " "
			}
			if !strings.ContainsRune(" +-", rune(line[0])) {
				return nil, fmt.Errorf("update lines must start with space, +, or -: %s", line)
			}
			hunk.lines = append(hunk.lines, line)
		}
	}
	// Many models omit the closing marker at EOF. If a complete file
	// operation was parsed, accept it; atomic validation and hunk matching
	// still protect against a truncated body.
	if begun && current != nil {
		finishOp()
		if len(ops) > 0 {
			return ops, nil
		}
	}
	return nil, fmt.Errorf("patch is missing *** End Patch")
}

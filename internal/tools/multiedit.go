package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// MultiEdit applies several edits to one file in a single call.
//
// Claude Code has MultiEdit for the same reason: one round-trip instead of
// one per change, and the edits are checked before any of them is written.
type MultiEdit struct{}

type multiEditArgs struct {
	Path  string          `json:"path"`
	Edits []multiEditItem `json:"edits"`
}

type multiEditItem struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

func (t *MultiEdit) Name() string  { return "multi_edit" }
func (t *MultiEdit) Class() string { return "edit" }
func (t *MultiEdit) Description() string {
	return "Apply several edits to one file in a single call. Each edit replaces a unique string; matching tolerates CRLF and whitespace differences. All edits are checked before anything is written, so the file is never half-edited."
}
func (t *MultiEdit) Schema() map[string]any {
	return obj(map[string]any{
		"path": desc("the file to edit"),
		"edits": map[string]any{
			"type":        "array",
			"description": "the edits to apply, in order",
			"minItems":    1,
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"old_string":  map[string]any{"type": "string", "description": "the text to replace, with enough context to be unique"},
					"new_string":  map[string]any{"type": "string", "description": "the replacement text"},
					"replace_all": map[string]any{"type": "boolean", "description": "replace every occurrence instead of requiring a unique one"},
				},
				"required": []string{"old_string", "new_string"},
			},
		},
	}, "path", "edits")
}

func (t *MultiEdit) Run(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[multiEditArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if len(a.Edits) == 0 {
		return "", fmt.Errorf("multi_edit needs at least one edit")
	}
	if err := env.checkPolicy("edit", a.Path); err != nil {
		return "", err
	}
	path, err := env.ResolvePath(a.Path)
	if err != nil {
		return "", err
	}
	if err := env.checkOldContent(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	original := string(data)
	content := original
	fuzzy := false
	total := 0
	for i, e := range a.Edits {
		if e.OldString == "" {
			return "", fmt.Errorf("edit %d: old_string is required", i+1)
		}
		var count int
		var wasFuzzy bool
		content, count, wasFuzzy, err = replaceTextIn(path, content, e.OldString, e.NewString, e.ReplaceAll)
		if err != nil {
			return "", fmt.Errorf("edit %d: %w", i+1, err)
		}
		total += count
		fuzzy = fuzzy || wasFuzzy
	}
	if err := env.CheckPathPreview("edit", a.Path, DiffPreview(a.Path, original, content)); err != nil {
		return "", err
	}
	if err := env.Snapshot("before multi edit", a.Path); err != nil {
		return "", err
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), st.Mode().Perm()); err != nil {
		return "", err
	}
	out := fmt.Sprintf("edited %s (%d edits, %d replacements)", a.Path, len(a.Edits), total)
	if fuzzy {
		out += " [some matches ignored whitespace/line endings]"
	}
	if w := secretWarning(env, path); w != "" {
		out = w + "\n" + out
	}
	return out, nil
}

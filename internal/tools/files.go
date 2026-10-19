package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxFileBytes = 8 << 20

type Read struct{}

type readArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func (t *Read) Name() string  { return "read" }
func (t *Read) Class() string { return "read" }
func (t *Read) Description() string {
	return "Read a text file with line numbers. Use offset/limit for large files. Default reads up to 2000 lines."
}
func (t *Read) Schema() map[string]any {
	return obj(map[string]any{
		"path":   desc("file path, absolute or relative to the working directory"),
		"offset": map[string]any{"type": "integer", "description": "1-based starting line"},
		"limit":  map[string]any{"type": "integer", "description": "max lines to read (default 2000)"},
	}, "path")
}

func (t *Read) Run(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[readArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	path := resolve(env, a.Path)
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if st.Size() > maxFileBytes {
		return "", fmt.Errorf("file too large (%d bytes)", st.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	total := len(lines)
	offset := a.Offset
	if offset < 1 {
		offset = 1
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 2000
	}
	if limit > 5000 {
		limit = 5000
	}
	if offset > total {
		return fmt.Sprintf("%s: offset %d beyond end of file (%d lines)", a.Path, offset, total), nil
	}
	end := offset + limit - 1
	if end > total {
		end = total
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (lines %d-%d of %d)\n", a.Path, offset, end, total)
	width := len(fmt.Sprintf("%d", end))
	for i := offset; i <= end; i++ {
		fmt.Fprintf(&b, "%*d| %s\n", width, i, lines[i-1])
	}
	return Truncate(strings.TrimSuffix(b.String(), "\n"), 0, 0, 96*1024), nil
}

type Write struct{}

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *Write) Name() string  { return "write" }
func (t *Write) Class() string { return "write" }
func (t *Write) Description() string {
	return "Write a file, creating it and any parent directories. Overwrites existing content."
}
func (t *Write) Schema() map[string]any {
	return obj(map[string]any{
		"path":    desc("file path"),
		"content": desc("full file content"),
	}, "path", "content")
}

func (t *Write) Run(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[writeArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if !env.Allowed("write", a.Path) {
		return "", fmt.Errorf("permission denied by user")
	}
	path := resolve(env, a.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(a.Content), 0o644); err != nil {
		return "", err
	}
	n := 0
	if a.Content != "" {
		n = strings.Count(a.Content, "\n") + 1
	}
	return fmt.Sprintf("wrote %s (%d lines, %d bytes)", a.Path, n, len(a.Content)), nil
}

type Edit struct{}

type editArgs struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func (t *Edit) Name() string  { return "edit" }
func (t *Edit) Class() string { return "edit" }
func (t *Edit) Description() string {
	return "Replace an exact string in a file. old_string must match exactly and be unique unless replace_all is true. Include enough surrounding context to make it unique."
}
func (t *Edit) Schema() map[string]any {
	return obj(map[string]any{
		"path":        desc("file path"),
		"old_string":  desc("exact text to replace"),
		"new_string":  desc("replacement text"),
		"replace_all": map[string]any{"type": "boolean", "description": "replace every occurrence"},
	}, "path", "old_string", "new_string")
}

func (t *Edit) Run(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[editArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if a.OldString == a.NewString {
		return "", fmt.Errorf("old_string and new_string are identical")
	}
	if !env.Allowed("edit", a.Path) {
		return "", fmt.Errorf("permission denied by user")
	}
	path := resolve(env, a.Path)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := string(data)
	count := strings.Count(content, a.OldString)
	switch {
	case count == 0:
		return "", fmt.Errorf("old_string not found in %s", a.Path)
	case count > 1 && !a.ReplaceAll:
		return "", fmt.Errorf("old_string matches %d times in %s; add more context or set replace_all", count, a.Path)
	}
	var updated string
	if a.ReplaceAll {
		updated = strings.ReplaceAll(content, a.OldString, a.NewString)
	} else {
		updated = strings.Replace(content, a.OldString, a.NewString, 1)
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(updated), st.Mode().Perm()); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s (%d replacement(s))", a.Path, count), nil
}

func resolve(env *Env, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(env.Dir, path)
}

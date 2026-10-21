package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const maxFileBytes = 8 << 20

type Read struct{}

// maxImageBytes caps an image the agent may look at before it is downscaled.
const maxImageBytes = 24 << 20

// imageMIME reports whether a path is an image the model can look at.
func imageMIME(path string) (string, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	case ".bmp":
		return "image/bmp", true
	}
	return "", false
}

// readImage hands an image to the model so it can look at it, instead of
// returning the bytes as text. Large images are downscaled first: a camera
// photo should not cost a quarter of the context window.
func (t *Read) readImage(env *Env, path, mime string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if st.Size() > maxImageBytes {
		return "", fmt.Errorf("image too large (%d bytes, max %d)", st.Size(), maxImageBytes)
	}
	prepared, note, err := prepareImage(env, path, mime)
	if err != nil {
		return "", err
	}
	env.AddMedia(Media{Path: prepared, MIME: mimeOf(prepared, mime), Name: filepath.Base(path), Note: note})
	extra := ""
	if note != "" {
		extra = " " + note + "."
	}
	return fmt.Sprintf("image: %s (%s, %d bytes). It is attached to this message for you to look at.%s", path, mime, st.Size(), extra), nil
}

type readArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func (t *Read) Name() string  { return "read" }
func (t *Read) Class() string { return "read" }
func (t *Read) Description() string {
	return "Read a text file with line numbers. Use offset/limit for large files. Default reads up to 2000 lines. Images are attached for you to look at; binary files are refused instead of flooding the context."
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
	if err := env.CheckPath("read", a.Path); err != nil {
		return "", err
	}
	path, err := env.ResolvePath(a.Path)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", fmt.Errorf("%s is a directory; use glob or grep", a.Path)
	}
	if st.Size() > maxFileBytes {
		return "", fmt.Errorf("file too large (%d bytes)", st.Size())
	}
	if mime, ok := imageMIME(path); ok {
		return t.readImage(env, path, mime)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if isBinaryData(data) {
		return "", fmt.Errorf("%s looks like a binary file (%d bytes, not UTF-8 text); read does not dump it into the context. Use bash with file, xxd, or strings if you need details", a.Path, len(data))
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
	return Truncate(strings.TrimSuffix(b.String(), "\n"), 0, 0, 400*1024), nil
}

// isBinaryData reports whether the bytes look like a binary file. NUL bytes
// and a high share of invalid UTF-8 are the signals; ordinary source files,
// including ones with unicode, pass.
func isBinaryData(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	if utf8.Valid(head) {
		return false
	}
	invalid := 0
	control := 0
	for _, b := range head {
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' && b != '\f' && b != '\b' {
			control++
		}
	}
	for len(head) > 0 {
		_, size := utf8.DecodeRune(head)
		if size == 1 && head[0] >= 0x80 {
			invalid++
		}
		head = head[size:]
	}
	total := len(data)
	if total > 8192 {
		total = 8192
	}
	return control*100/total > 10 || invalid*100/total > 30
}

type Write struct{}

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *Write) Name() string  { return "write" }
func (t *Write) Class() string { return "write" }
func (t *Write) Description() string {
	return "Write a file, creating it and any parent directories. Overwrites existing content. The permission prompt shows the diff before anything is written."
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
	path, err := env.ResolvePath(a.Path)
	if err != nil {
		return "", err
	}
	var old string
	if err := env.checkPolicy("write", a.Path); err != nil {
		return "", err
	}
	if err := env.checkOldContent(path); err != nil {
		return "", err
	}
	if data, err := os.ReadFile(path); err == nil {
		old = string(data)
	}
	preview := DiffPreview(a.Path, old, a.Content)
	if err := env.CheckPathPreview("write", a.Path, preview); err != nil {
		return "", err
	}
	if err := env.Snapshot("before write", a.Path); err != nil {
		return "", err
	}
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
	out := fmt.Sprintf("wrote %s (%d lines, %d bytes)", a.Path, n, len(a.Content))
	if w := secretWarning(env, path); w != "" {
		out = w + "\n" + out
	}
	return out, nil
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
	return "Replace text in a file. old_string must be unique unless replace_all is true. Exact matching is tried first, then a line-based match that tolerates CRLF and whitespace differences. The permission prompt shows the diff."
}
func (t *Edit) Schema() map[string]any {
	return obj(map[string]any{
		"path":        desc("file path"),
		"old_string":  desc("text to replace, with enough context to be unique"),
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
	updated, count, fuzzy, err := replaceTextIn(path, string(data), a.OldString, a.NewString, a.ReplaceAll)
	if err != nil {
		return "", fmt.Errorf("%s: %w", a.Path, err)
	}
	if err := env.CheckPathPreview("edit", a.Path, DiffPreview(a.Path, string(data), updated)); err != nil {
		return "", err
	}
	if err := env.Snapshot("before edit", a.Path); err != nil {
		return "", err
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(updated), st.Mode().Perm()); err != nil {
		return "", err
	}
	out := fmt.Sprintf("edited %s (%d replacement(s))", a.Path, count)
	if fuzzy {
		out += " [matched ignoring whitespace/line endings]"
	}
	if w := secretWarning(env, path); w != "" {
		out = w + "\n" + out
	}
	return out, nil
}

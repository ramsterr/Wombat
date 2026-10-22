package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Replace performs the same literal or regular-expression replacement across
// one or more files in a single call. It exists so bulk and regex edits do
// not turn into throwaway Python/Perl/sed scripts.
type Replace struct{}

type replaceArgs struct {
	Paths       []string `json:"paths"`
	Pattern     string   `json:"pattern"`
	Replacement string   `json:"replacement"`
	Regex       bool     `json:"regex"`
	IgnoreCase  bool     `json:"ignore_case"`
	Count       int      `json:"count"`
	MaxFiles    int      `json:"max_files"`
}

type replaceChange struct {
	rel      string
	path     string
	mode     os.FileMode
	original string
	updated  string
	count    int
}

func (t *Replace) Name() string  { return "replace" }
func (t *Replace) Class() string { return "edit" }
func (t *Replace) Description() string {
	return "Replace a literal string or regular expression across several files in one call. Paths may be files, directories, or glob patterns. Regex replacements support $1 capture groups. Nothing is written unless every file resolves; a write failure restores the files already changed. Use this instead of bash with python, perl, or sed for bulk edits."
}
func (t *Replace) Schema() map[string]any {
	return obj(map[string]any{
		"paths":       map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}, "description": "files, directories, or glob patterns to edit"},
		"pattern":     desc("the literal text or regular expression to find"),
		"replacement": desc("the replacement text; regex replacements may use $1, $2, and so on"),
		"regex":       map[string]any{"type": "boolean", "description": "treat pattern as a Go regular expression"},
		"ignore_case": map[string]any{"type": "boolean", "description": "match case-insensitively"},
		"count":       map[string]any{"type": "integer", "description": "maximum replacements per file (default: every match)"},
		"max_files":   map[string]any{"type": "integer", "description": "safety limit on files (default 500)"},
	}, "paths", "pattern", "replacement")
}

func (t *Replace) Run(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[replaceArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if strings.TrimSpace(a.Pattern) == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if len(a.Paths) == 0 {
		return "", fmt.Errorf("paths is required")
	}
	if a.MaxFiles <= 0 {
		a.MaxFiles = 500
	}
	files, err := expandReplacePaths(env, a.Paths, a.MaxFiles)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no files matched %s", strings.Join(a.Paths, ", "))
	}
	pattern := a.Pattern
	if !a.Regex {
		pattern = regexp.QuoteMeta(pattern)
	}
	if a.IgnoreCase && !strings.HasPrefix(pattern, "(?i)") {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("bad pattern: %w", err)
	}

	var changes []replaceChange
	var preview strings.Builder
	totalFiles := 0
	for _, file := range files {
		rel := file
		if r, err := filepath.Rel(env.Dir, file); err == nil {
			rel = filepath.ToSlash(r)
		}
		if err := env.checkPolicy("edit", rel); err != nil {
			return "", err
		}
		st, err := os.Stat(file)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		if st.Size() > maxFileBytes {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		if isBinaryData(data) {
			continue
		}
		original := string(data)
		updated, count := replaceWithRegex(re, original, a.Replacement, a.Count, a.Regex)
		if count == 0 {
			continue
		}
		changes = append(changes, replaceChange{rel: rel, path: file, mode: st.Mode().Perm(), original: original, updated: updated, count: count})
		preview.WriteString(DiffPreview(rel, original, updated))
		preview.WriteString("\n")
		totalFiles++
	}
	if len(changes) == 0 {
		return fmt.Sprintf("no matches for %q in %d file(s)", a.Pattern, len(files)), nil
	}
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		paths = append(paths, change.rel)
	}
	if err := env.CheckPaths("edit", paths, preview.String()); err != nil {
		return "", err
	}
	if err := env.Snapshot("before replace", paths...); err != nil {
		return "", err
	}
	var written []replaceChange
	for _, change := range changes {
		if err := os.WriteFile(change.path, []byte(change.updated), change.mode); err != nil {
			_ = os.WriteFile(change.path, []byte(change.original), change.mode)
			for _, previous := range written {
				_ = os.WriteFile(previous.path, []byte(previous.original), previous.mode)
			}
			return "", fmt.Errorf("write %s: %w (already-written files were restored)", change.rel, err)
		}
		written = append(written, change)
	}
	var warnings []string
	for _, change := range changes {
		if warning := secretWarning(env, change.path); warning != "" {
			warnings = append(warnings, warning)
		}
	}
	sort.Strings(paths)
	out := fmt.Sprintf("replaced %d occurrence(s) in %d file(s): %s", replacementTotal(changes), totalFiles, strings.Join(paths, ", "))
	if len(warnings) > 0 {
		out = strings.Join(warnings, "\n") + "\n" + out
	}
	return out, nil
}

func replacementTotal(changes []replaceChange) int {
	total := 0
	for _, change := range changes {
		total += change.count
	}
	return total
}

func replaceWithRegex(re *regexp.Regexp, source, replacement string, limit int, expand bool) (string, int) {
	if limit == 0 {
		limit = -1
	}
	matches := re.FindAllStringSubmatchIndex(source, limit)
	if len(matches) == 0 {
		return source, 0
	}
	var b strings.Builder
	last := 0
	for _, match := range matches {
		if match[0] < last {
			continue
		}
		b.WriteString(source[last:match[0]])
		if expand {
			b.Write(re.ExpandString(nil, replacement, source, match))
		} else {
			b.WriteString(replacement)
		}
		last = match[1]
	}
	b.WriteString(source[last:])
	return b.String(), len(matches)
}

func expandReplacePaths(env *Env, patterns []string, max int) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	add := func(file string) {
		abs, err := filepath.Abs(file)
		if err != nil || seen[abs] {
			return
		}
		seen[abs] = true
		files = append(files, abs)
	}
	for _, raw := range patterns {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		target := raw
		if !filepath.IsAbs(target) {
			target = filepath.Join(env.Dir, target)
		}
		if strings.ContainsAny(raw, "*?[") {
			matches, err := filepath.Glob(target)
			if err != nil {
				return nil, fmt.Errorf("bad glob %q: %w", raw, err)
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("no files matched %q", raw)
			}
			for _, match := range matches {
				if err := collectReplaceFile(match, add, max, seen); err != nil {
					return nil, err
				}
				if len(files) >= max {
					return files, nil
				}
			}
			continue
		}
		if err := collectReplaceFile(target, add, max, seen); err != nil {
			return nil, err
		}
		if len(files) >= max {
			break
		}
	}
	if len(files) > max {
		files = files[:max]
	}
	return files, nil
}

func collectReplaceFile(target string, add func(string), max int, seen map[string]bool) error {
	st, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		add(target)
		return nil
	}
	return filepath.WalkDir(target, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", ".wombat", "vendor", "dist", "build", ".next", "coverage", ".cache":
				return filepath.SkipDir
			}
			return nil
		}
		if len(seen) >= max {
			return filepath.SkipAll
		}
		add(current)
		return nil
	})
}

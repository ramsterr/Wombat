package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".venv":        true,
	"venv":         true,
	".wombat":      true,
	".ssh":         true,
	".aws":         true,
	".gnupg":       true,
}

func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	i := 0
	for i < len(pattern) {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 3
					continue
				}
				b.WriteString(".*")
				i += 2
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
		i++
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func matchPath(re *regexp.Regexp, pattern, rel string) bool {
	rel = filepath.ToSlash(rel)
	if !strings.Contains(pattern, "/") {
		return re.MatchString(filepath.Base(rel))
	}
	return re.MatchString(rel)
}

func walk(root string, fn func(path string, d fs.DirEntry) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && path != root && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		return fn(path, d)
	})
}

type Glob struct{}

type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

func (t *Glob) Name() string  { return "glob" }
func (t *Glob) Class() string { return "read" }
func (t *Glob) Description() string {
	return "Find files by glob pattern (supports ** and *). Matches basenames when the pattern has no slash, otherwise relative paths."
}
func (t *Glob) Schema() map[string]any {
	return obj(map[string]any{
		"pattern": desc("glob pattern, e.g. **/*.go or *_test.go"),
		"path":    desc("directory to search (default: working directory)"),
	}, "pattern")
}

func (t *Glob) Run(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[globArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	re, err := globToRegexp(a.Pattern)
	if err != nil {
		return "", fmt.Errorf("bad pattern: %w", err)
	}
	root := env.Dir
	if a.Path != "" {
		if err := env.CheckPath("read", a.Path); err != nil {
			return "", err
		}
		root, err = env.ResolvePath(a.Path)
		if err != nil {
			return "", err
		}
	}
	var matches []string
	_ = walk(root, func(path string, d fs.DirEntry) error {
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if matchPath(re, a.Pattern, rel) {
			matches = append(matches, filepath.ToSlash(rel))
		}
		if len(matches) >= 250 {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(matches)
	if len(matches) == 0 {
		return "no files matched " + a.Pattern, nil
	}
	out := fmt.Sprintf("%d files:\n%s", len(matches), strings.Join(matches, "\n"))
	return Truncate(out, 800, 0.95, 200*1024), nil
}

type Grep struct{}

type grepArgs struct {
	Pattern string `json:"pattern"`
	Include string `json:"include"`
	Path    string `json:"path"`
}

func (t *Grep) Name() string  { return "grep" }
func (t *Grep) Class() string { return "read" }
func (t *Grep) Description() string {
	return "Search file contents with a regular expression. Optional include filters filenames (e.g. *.go). Returns path:line: text matches."
}
func (t *Grep) Schema() map[string]any {
	return obj(map[string]any{
		"pattern": desc("regular expression to search for"),
		"include": desc("only search files matching this glob, e.g. *.go"),
		"path":    desc("directory to search (default: working directory)"),
	}, "pattern")
}

func (t *Grep) Run(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[grepArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return "", fmt.Errorf("bad regexp: %w", err)
	}
	var includeRe *regexp.Regexp
	if a.Include != "" {
		includeRe, err = globToRegexp(a.Include)
		if err != nil {
			return "", fmt.Errorf("bad include: %w", err)
		}
	}
	root := env.Dir
	if a.Path != "" {
		if err := env.CheckPath("read", a.Path); err != nil {
			return "", err
		}
		root, err = env.ResolvePath(a.Path)
		if err != nil {
			return "", err
		}
	}
	const maxMatches = 200
	const maxFiles = 5000
	scanned := 0
	matchedFiles := map[string]bool{}
	var out []string
	truncated := false
	_ = walk(root, func(path string, d fs.DirEntry) error {
		if truncated {
			return filepath.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if includeRe != nil && !matchPath(includeRe, a.Include, rel) {
			return nil
		}
		if isProtectedPath(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 2<<20 {
			return nil
		}
		scanned++
		if scanned > maxFiles {
			truncated = true
			return filepath.SkipAll
		}
		data, err := os.ReadFile(path)
		if err != nil || bytes.IndexByte(data[:min(len(data), 1024)], 0) >= 0 {
			return nil
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		lineNo := 0
		for sc.Scan() {
			lineNo++
			line := strings.TrimRight(sc.Text(), "\r")
			if !re.MatchString(line) {
				continue
			}
			text := strings.TrimSpace(line)
			if len(text) > 240 {
				text = text[:240] + "…"
			}
			out = append(out, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), lineNo, text))
			matchedFiles[filepath.ToSlash(rel)] = true
			if len(out) >= maxMatches {
				truncated = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	if len(out) == 0 {
		return "no matches for " + a.Pattern, nil
	}
	header := fmt.Sprintf("%d matches in %d files:\n", len(out), len(matchedFiles))
	if truncated {
		header = fmt.Sprintf("%d+ matches (capped) in %d+ files:\n", maxMatches, len(matchedFiles))
	}
	res := header + strings.Join(out, "\n")
	return RedactSecrets(Truncate(res, 800, 0.9, 200*1024)), nil
}

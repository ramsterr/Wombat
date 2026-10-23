package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const gitTimeout = 60 * time.Second

// gitOutput runs a read-only git command in the environment's workspace.
// Commands are executed directly, never through a shell, so revision and
// path arguments cannot inject extra commands.
func gitOutput(ctx context.Context, env *Env, args ...string) (string, error) {
	if env == nil || strings.TrimSpace(env.Dir) == "" {
		return "", errors.New("git: no working directory")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return "", errors.New("git is not installed or not on PATH")
	}
	runCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", append([]string{"--no-pager"}, args...)...)
	cmd.Dir = env.Dir
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = append(os.Environ(),
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_PAGER=cat",
		"GIT_TERMINAL_PROMPT=0",
	)
	prepareCommand(cmd)
	stdout := &cappedBuffer{max: 2 * 1024 * 1024}
	stderr := &cappedBuffer{max: 64 * 1024}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("git %s timed out after %s", args[0], gitTimeout)
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return stdout.String(), nil
}

func gitPathspec(env *Env, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if env == nil {
		return "", errors.New("git: no working directory")
	}
	if filepath.IsAbs(path) {
		rel, err := filepath.Rel(env.Dir, path)
		if err != nil {
			return "", fmt.Errorf("path is outside the repository: %s", path)
		}
		path = rel
	}
	clean := filepath.Clean(path)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path is outside the repository: %s", path)
	}
	return filepath.ToSlash(clean), nil
}

func validateGitRev(rev string) (string, error) {
	rev = strings.TrimSpace(rev)
	if rev == "" {
		return "", nil
	}
	if strings.HasPrefix(rev, "-") {
		return "", fmt.Errorf("invalid revision %q", rev)
	}
	if strings.ContainsAny(rev, " \t\r\n\x00") {
		return "", fmt.Errorf("invalid revision %q", rev)
	}
	return rev, nil
}

func jsonToolResult(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// GitStatus reports branch state and changed files as structured data.
type GitStatus struct{}

type gitStatusFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	From   string `json:"from,omitempty"`
}

type gitStatusResult struct {
	Branch     string          `json:"branch,omitempty"`
	Upstream   string          `json:"upstream,omitempty"`
	Ahead      int             `json:"ahead,omitempty"`
	Behind     int             `json:"behind,omitempty"`
	Clean      bool            `json:"clean"`
	Staged     []gitStatusFile `json:"staged,omitempty"`
	Unstaged   []gitStatusFile `json:"unstaged,omitempty"`
	Untracked  []string        `json:"untracked,omitempty"`
	Conflicted []gitStatusFile `json:"conflicted,omitempty"`
}

func (t *GitStatus) Name() string  { return "git_status" }
func (t *GitStatus) Class() string { return "read" }
func (t *GitStatus) Description() string {
	return "Show the Git branch, upstream divergence, and staged, unstaged, untracked, and conflicted files as compact structured data. Prefer this over bash git status."
}
func (t *GitStatus) Schema() map[string]any { return obj(map[string]any{}) }
func (t *GitStatus) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	if _, err := decode[struct{}](raw); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	out, err := gitOutput(ctx, env, "status", "--porcelain=v1", "--branch", "-z")
	if err != nil {
		return "", err
	}
	return jsonToolResult(parseGitStatus(out))
}

func parseGitStatus(out string) gitStatusResult {
	res := gitStatusResult{Clean: true}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		rec := fields[i]
		if rec == "" {
			continue
		}
		if strings.HasPrefix(rec, "## ") {
			res.Branch, res.Upstream, res.Ahead, res.Behind = parseGitBranch(strings.TrimPrefix(rec, "## "))
			continue
		}
		if len(rec) < 4 {
			continue
		}
		x, y := rec[0], rec[1]
		path := rec[3:]
		var from string
		if x == 'R' || x == 'C' {
			if i+1 < len(fields) {
				from = fields[i+1]
				i++
			}
		}
		switch string([]byte{x, y}) {
		case "DD", "AU", "UD", "UA", "DU", "AA", "UU":
			res.Conflicted = append(res.Conflicted, gitStatusFile{Path: path, Status: string([]byte{x, y}), From: from})
		case "??":
			res.Untracked = append(res.Untracked, path)
		default:
			if x != ' ' && x != '?' {
				res.Staged = append(res.Staged, gitStatusFile{Path: path, Status: string(x), From: from})
			}
			if y != ' ' {
				res.Unstaged = append(res.Unstaged, gitStatusFile{Path: path, Status: string(y)})
			}
		}
	}
	res.Clean = len(res.Staged) == 0 && len(res.Unstaged) == 0 && len(res.Untracked) == 0 && len(res.Conflicted) == 0
	return res
}

func parseGitBranch(s string) (branch, upstream string, ahead, behind int) {
	if i := strings.Index(s, " ["); i >= 0 {
		info := strings.TrimSuffix(s[i+2:], "]")
		s = s[:i]
		for _, part := range strings.Split(info, ", ") {
			f := strings.Fields(part)
			if len(f) != 2 {
				continue
			}
			n, err := strconv.Atoi(f[1])
			if err != nil {
				continue
			}
			switch f[0] {
			case "ahead":
				ahead = n
			case "behind":
				behind = n
			}
		}
	}
	switch {
	case strings.HasPrefix(s, "No commits yet on "):
		return strings.TrimPrefix(s, "No commits yet on "), "", 0, 0
	case strings.HasPrefix(s, "Initial commit on "):
		return strings.TrimPrefix(s, "Initial commit on "), "", 0, 0
	case s == "HEAD (no branch)":
		return "HEAD (detached)", "", 0, 0
	}
	if i := strings.Index(s, "..."); i >= 0 {
		return s[:i], s[i+3:], ahead, behind
	}
	return s, "", ahead, behind
}

// GitDiff returns a unified diff without shelling out.
type GitDiff struct{}

type gitDiffArgs struct {
	Path         string `json:"path"`
	Rev          string `json:"rev"`
	Staged       bool   `json:"staged"`
	Stat         bool   `json:"stat"`
	ContextLines int    `json:"context_lines"`
}

func (t *GitDiff) Name() string  { return "git_diff" }
func (t *GitDiff) Class() string { return "read" }
func (t *GitDiff) Description() string {
	return "Show a unified diff of the working tree, the index with staged=true, or a revision/range with rev. Optionally limit to a path or show a stat summary. Read-only; use instead of bash git diff."
}
func (t *GitDiff) Schema() map[string]any {
	return obj(map[string]any{
		"path":          desc("optional file or directory to limit the diff to, relative to the repository"),
		"rev":           desc("optional revision or range, such as HEAD~1 or main...HEAD"),
		"staged":        map[string]any{"type": "boolean", "description": "diff the index instead of the working tree"},
		"stat":          map[string]any{"type": "boolean", "description": "show a diffstat summary instead of the patch"},
		"context_lines": map[string]any{"type": "integer", "description": "lines of context around each change (1-50, default 3)"},
	})
}
func (t *GitDiff) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[gitDiffArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	rev, err := validateGitRev(a.Rev)
	if err != nil {
		return "", err
	}
	path, err := gitPathspec(env, a.Path)
	if err != nil {
		return "", err
	}
	args := []string{"diff", "--no-color", "--no-ext-diff"}
	if a.Staged {
		args = append(args, "--staged")
	}
	if a.Stat {
		args = append(args, "--stat")
	}
	if a.ContextLines > 0 {
		if a.ContextLines > 50 {
			a.ContextLines = 50
		}
		args = append(args, fmt.Sprintf("-U%d", a.ContextLines))
	}
	if rev != "" {
		args = append(args, rev)
	}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := gitOutput(ctx, env, args...)
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "(no changes)", nil
	}
	return RedactSecrets(Truncate(out, 800, 0.5, 200*1024)), nil
}

// GitLog returns recent commits as compact structured data.
type GitLog struct{}

type gitLogArgs struct {
	Path  string `json:"path"`
	Rev   string `json:"rev"`
	Limit int    `json:"limit"`
}

type gitLogEntry struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Subject string `json:"subject"`
}

type gitLogResult struct {
	Entries   []gitLogEntry `json:"entries"`
	Truncated bool          `json:"truncated,omitempty"`
}

func (t *GitLog) Name() string  { return "git_log" }
func (t *GitLog) Class() string { return "read" }
func (t *GitLog) Description() string {
	return "Show recent commits as structured entries with hash, author, date, and subject. Optionally filter by path or revision. Read-only; use instead of bash git log."
}
func (t *GitLog) Schema() map[string]any {
	return obj(map[string]any{
		"path":  desc("optional file or directory to show history for, relative to the repository"),
		"rev":   desc("optional revision or range, such as a branch or HEAD~5..HEAD (default HEAD)"),
		"limit": map[string]any{"type": "integer", "description": "maximum commits to return (1-100, default 20)"},
	})
}
func (t *GitLog) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[gitLogArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	rev, err := validateGitRev(a.Rev)
	if err != nil {
		return "", err
	}
	path, err := gitPathspec(env, a.Path)
	if err != nil {
		return "", err
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	args := []string{"log", "--no-color", "--format=%H%x00%h%x00%an%x00%aI%x00%s%x1e", "-n", strconv.Itoa(limit + 1)}
	if rev != "" {
		args = append(args, rev)
	}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := gitOutput(ctx, env, args...)
	if err != nil {
		return "", err
	}
	res := gitLogResult{Entries: []gitLogEntry{}}
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.Trim(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x00")
		if len(f) < 5 {
			continue
		}
		res.Entries = append(res.Entries, gitLogEntry{Hash: f[0], Short: f[1], Author: f[2], Date: f[3], Subject: f[4]})
	}
	if len(res.Entries) > limit {
		res.Entries = res.Entries[:limit]
		res.Truncated = true
	}
	return jsonToolResult(res)
}

// GitShow returns one commit with its patch.
type GitShow struct{}

type gitShowArgs struct {
	Rev  string `json:"rev"`
	Path string `json:"path"`
	Stat bool   `json:"stat"`
}

func (t *GitShow) Name() string  { return "git_show" }
func (t *GitShow) Class() string { return "read" }
func (t *GitShow) Description() string {
	return "Show one commit: metadata plus its patch, optionally limited to a path or summarized with stat. Read-only; use instead of bash git show."
}
func (t *GitShow) Schema() map[string]any {
	return obj(map[string]any{
		"rev":  desc("revision to show (default HEAD); a single revision, not a range"),
		"path": desc("optional file or directory to limit the patch to, relative to the repository"),
		"stat": map[string]any{"type": "boolean", "description": "show a diffstat summary instead of the patch"},
	})
}
func (t *GitShow) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[gitShowArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	rev, err := validateGitRev(a.Rev)
	if err != nil {
		return "", err
	}
	if rev == "" {
		rev = "HEAD"
	}
	if strings.Contains(rev, "..") {
		return "", fmt.Errorf("git_show takes a single revision; use git_diff for ranges")
	}
	path, err := gitPathspec(env, a.Path)
	if err != nil {
		return "", err
	}
	args := []string{"show", "--no-color", "--no-ext-diff", "--date=iso-strict", "--format=fuller"}
	if a.Stat {
		args = append(args, "--stat")
	} else {
		args = append(args, "--patch")
	}
	args = append(args, rev)
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := gitOutput(ctx, env, args...)
	if err != nil {
		return "", err
	}
	return RedactSecrets(Truncate(strings.TrimSpace(out), 800, 0.5, 200*1024)), nil
}

// GitBlame attributes each line of a file to a commit.
type GitBlame struct{}

type gitBlameArgs struct {
	Path      string `json:"path"`
	Rev       string `json:"rev"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type gitBlameLine struct {
	Line    int    `json:"line"`
	Hash    string `json:"hash"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Summary string `json:"summary,omitempty"`
	Content string `json:"content"`
}

type gitBlameResult struct {
	Path      string         `json:"path"`
	Lines     []gitBlameLine `json:"lines"`
	Truncated bool           `json:"truncated,omitempty"`
}

const gitBlameMaxLines = 400

func (t *GitBlame) Name() string  { return "git_blame" }
func (t *GitBlame) Class() string { return "read" }
func (t *GitBlame) Description() string {
	return "Attribute each line of a file to the commit that last changed it, with author, date, summary, and content. Read-only; use instead of bash git blame."
}
func (t *GitBlame) Schema() map[string]any {
	return obj(map[string]any{
		"path":       desc("file to blame, relative to the repository"),
		"rev":        desc("optional revision to blame at (default: working tree)"),
		"start_line": map[string]any{"type": "integer", "description": "optional first line of the range (1-based)"},
		"end_line":   map[string]any{"type": "integer", "description": "optional last line of the range; defaults to start_line"},
	}, "path")
}
func (t *GitBlame) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[gitBlameArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	path, err := gitPathspec(env, strings.TrimSpace(a.Path))
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", errors.New("path is required")
	}
	rev, err := validateGitRev(a.Rev)
	if err != nil {
		return "", err
	}
	args := []string{"blame", "--line-porcelain"}
	if a.StartLine > 0 {
		end := a.EndLine
		if end <= 0 {
			end = a.StartLine
		}
		if end < a.StartLine {
			return "", fmt.Errorf("end_line %d is before start_line %d", end, a.StartLine)
		}
		if end-a.StartLine+1 > gitBlameMaxLines {
			end = a.StartLine + gitBlameMaxLines - 1
		}
		args = append(args, fmt.Sprintf("-L%d,%d", a.StartLine, end))
	}
	if rev != "" {
		args = append(args, rev)
	}
	args = append(args, "--", path)
	out, err := gitOutput(ctx, env, args...)
	if err != nil {
		return "", err
	}
	res := parseGitBlame(out)
	res.Path = path
	return jsonToolResult(res)
}

func parseGitBlame(out string) gitBlameResult {
	res := gitBlameResult{Lines: []gitBlameLine{}}
	var cur gitBlameLine
	var authorTime, authorTZ string
	flush := func() {
		if cur.Line == 0 {
			return
		}
		cur.Date = gitDate(authorTime, authorTZ)
		if len(cur.Hash) > 8 {
			cur.Hash = cur.Hash[:8]
		}
		res.Lines = append(res.Lines, cur)
		cur = gitBlameLine{}
		authorTime, authorTZ = "", ""
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "\t"):
			cur.Content = line[1:]
			flush()
		case strings.HasPrefix(line, "author "):
			cur.Author = strings.TrimPrefix(line, "author ")
		case strings.HasPrefix(line, "author-time "):
			authorTime = strings.TrimPrefix(line, "author-time ")
		case strings.HasPrefix(line, "author-tz "):
			authorTZ = strings.TrimPrefix(line, "author-tz ")
		case strings.HasPrefix(line, "summary "):
			cur.Summary = strings.TrimPrefix(line, "summary ")
		default:
			f := strings.Fields(line)
			if len(f) >= 3 && len(f[0]) >= 8 {
				if n, err := strconv.Atoi(f[2]); err == nil {
					cur.Line = n
					cur.Hash = f[0]
				}
			}
		}
	}
	if len(res.Lines) > gitBlameMaxLines {
		res.Lines = res.Lines[:gitBlameMaxLines]
		res.Truncated = true
	}
	return res
}

func gitDate(epoch, tz string) string {
	sec, err := strconv.ParseInt(strings.TrimSpace(epoch), 10, 64)
	if err != nil {
		return ""
	}
	loc := time.UTC
	tz = strings.TrimSpace(tz)
	if len(tz) == 5 && (tz[0] == '+' || tz[0] == '-') {
		sign := 1
		if tz[0] == '-' {
			sign = -1
		}
		hh, errH := strconv.Atoi(tz[1:3])
		mm, errM := strconv.Atoi(tz[3:5])
		if errH == nil && errM == nil {
			loc = time.FixedZone("", sign*(hh*3600+mm*60))
		}
	}
	return time.Unix(sec, 0).In(loc).Format("2006-01-02")
}

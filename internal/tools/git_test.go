package tools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test User"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestCmd(t, dir, "add", "a.txt")
	gitTestCmd(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func gitTestCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestGitStatusReportsWorktreeState(t *testing.T) {
	dir := gitRepo(t)
	env := testEnv(dir)

	out := run(t, &GitStatus{}, env, `{}`)
	var clean gitStatusResult
	if err := json.Unmarshal([]byte(out), &clean); err != nil {
		t.Fatalf("status json: %v\n%s", err, out)
	}
	if !clean.Clean {
		t.Errorf("fresh repo should be clean: %+v", clean)
	}
	if clean.Branch != "main" {
		t.Errorf("branch = %q, want main", clean.Branch)
	}

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestCmd(t, dir, "add", "b.txt")

	out = run(t, &GitStatus{}, env, `{}`)
	var st gitStatusResult
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("status json: %v\n%s", err, out)
	}
	if st.Clean {
		t.Fatal("dirty repo reported clean")
	}
	if len(st.Staged) != 1 || st.Staged[0].Path != "b.txt" || st.Staged[0].Status != "A" {
		t.Errorf("staged = %+v", st.Staged)
	}
	if len(st.Unstaged) != 1 || st.Unstaged[0].Path != "a.txt" || st.Unstaged[0].Status != "M" {
		t.Errorf("unstaged = %+v", st.Unstaged)
	}
	if len(st.Untracked) != 1 || st.Untracked[0] != "c.txt" {
		t.Errorf("untracked = %+v", st.Untracked)
	}
}

func TestGitStatusParsesRenames(t *testing.T) {
	dir := gitRepo(t)
	env := testEnv(dir)
	gitTestCmd(t, dir, "mv", "a.txt", "d.txt")

	out := run(t, &GitStatus{}, env, `{}`)
	var st gitStatusResult
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("status json: %v\n%s", err, out)
	}
	if len(st.Staged) != 1 {
		t.Fatalf("staged = %+v", st.Staged)
	}
	if got := st.Staged[0]; got.Path != "d.txt" || got.From != "a.txt" || got.Status != "R" {
		t.Errorf("rename = %+v", got)
	}
}

func TestGitDiffWorkingTreeAndStaged(t *testing.T) {
	dir := gitRepo(t)
	env := testEnv(dir)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := run(t, &GitDiff{}, env, `{}`)
	if !strings.Contains(out, "-two") || !strings.Contains(out, "+changed") {
		t.Errorf("working tree diff = %q", out)
	}
	gitTestCmd(t, dir, "add", "a.txt")
	out = run(t, &GitDiff{}, env, `{"staged":true}`)
	if !strings.Contains(out, "-two") || !strings.Contains(out, "+changed") {
		t.Errorf("staged diff = %q", out)
	}
	out = run(t, &GitDiff{}, env, `{"staged":true,"stat":true}`)
	if !strings.Contains(out, "a.txt") {
		t.Errorf("stat diff = %q", out)
	}
	if out := run(t, &GitDiff{}, env, `{"path":"missing.txt"}`); out != "(no changes)" {
		t.Errorf("empty diff = %q", out)
	}
	if err := runErr(t, &GitDiff{}, env, `{"rev":"-p"}`); !strings.Contains(err.Error(), "invalid revision") {
		t.Errorf("option revision error = %v", err)
	}
	if err := runErr(t, &GitDiff{}, env, `{"path":"../outside"}`); !strings.Contains(err.Error(), "outside") {
		t.Errorf("outside path error = %v", err)
	}
}

func TestGitLogReturnsStructuredCommits(t *testing.T) {
	dir := gitRepo(t)
	env := testEnv(dir)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestCmd(t, dir, "commit", "-q", "-am", "second")

	out := run(t, &GitLog{}, env, `{"limit":1}`)
	var res gitLogResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("log json: %v\n%s", err, out)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(res.Entries))
	}
	if !res.Truncated {
		t.Error("log should report truncation")
	}
	if got := res.Entries[0]; got.Subject != "second" || got.Short == "" || got.Author != "Test User" || got.Date == "" {
		t.Errorf("entry = %+v", got)
	}

	out = run(t, &GitLog{}, env, `{"path":"a.txt","limit":5}`)
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("log json: %v\n%s", err, out)
	}
	if len(res.Entries) != 2 {
		t.Errorf("path log entries = %d, want 2", len(res.Entries))
	}
}

func TestGitShowCommitAndPath(t *testing.T) {
	dir := gitRepo(t)
	env := testEnv(dir)

	out := run(t, &GitShow{}, env, `{}`)
	for _, want := range []string{"first", "a.txt", "+one"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}
	out = run(t, &GitShow{}, env, `{"stat":true}`)
	if !strings.Contains(out, "a.txt") {
		t.Errorf("show stat = %q", out)
	}
	if err := runErr(t, &GitShow{}, env, `{"rev":"HEAD~1..HEAD"}`); !strings.Contains(err.Error(), "single revision") {
		t.Errorf("range error = %v", err)
	}
}

func TestGitBlameAttributesLines(t *testing.T) {
	dir := gitRepo(t)
	env := testEnv(dir)

	out := run(t, &GitBlame{}, env, `{"path":"a.txt"}`)
	var res gitBlameResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("blame json: %v\n%s", err, out)
	}
	if len(res.Lines) != 2 {
		t.Fatalf("blame lines = %d, want 2", len(res.Lines))
	}
	first := res.Lines[0]
	if first.Line != 1 || first.Content != "one" || first.Author != "Test User" || len(first.Hash) != 8 || first.Date == "" {
		t.Errorf("first blame line = %+v", first)
	}
	if res.Lines[1].Content != "two" {
		t.Errorf("second blame line = %+v", res.Lines[1])
	}

	out = run(t, &GitBlame{}, env, `{"path":"a.txt","start_line":2,"end_line":2}`)
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("blame json: %v\n%s", err, out)
	}
	if len(res.Lines) != 1 || res.Lines[0].Line != 2 {
		t.Errorf("range blame = %+v", res.Lines)
	}

	if err := runErr(t, &GitBlame{}, env, `{"path":"a.txt","start_line":3,"end_line":2}`); !strings.Contains(err.Error(), "before") {
		t.Errorf("bad range error = %v", err)
	}
}

func TestGitToolsOutsideRepository(t *testing.T) {
	env := testEnv(t.TempDir())
	err := runErr(t, &GitStatus{}, env, `{}`)
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("outside repo error = %v", err)
	}
}

func TestGitToolsAreRegisteredReadOnly(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range Registry() {
		names[tool.Name()] = true
	}
	for _, name := range []string{"git_status", "git_diff", "git_log", "git_show", "git_blame"} {
		if !names[name] {
			t.Errorf("%s is not registered", name)
		}
	}
	for _, tool := range []Tool{&GitStatus{}, &GitDiff{}, &GitLog{}, &GitShow{}, &GitBlame{}} {
		if tool.Class() != "read" {
			t.Errorf("%s class = %q, want read", tool.Name(), tool.Class())
		}
	}
}

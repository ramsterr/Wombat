package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testEnv(dir string) *Env {
	return &Env{Dir: dir, Permit: func(string, string) bool { return true }}
}

func run(t *testing.T, tool Tool, env *Env, args string) string {
	t.Helper()
	out, err := tool.Run(context.Background(), json.RawMessage(args), env)
	if err != nil {
		t.Fatalf("%s failed: %v", tool.Name(), err)
	}
	return out
}

func runErr(t *testing.T, tool Tool, env *Env, args string) error {
	t.Helper()
	_, err := tool.Run(context.Background(), json.RawMessage(args), env)
	if err == nil {
		t.Fatalf("%s should have failed", tool.Name())
	}
	return err
}

func TestWriteReadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	env := testEnv(dir)
	out := run(t, &Write{}, env, `{"path":"sub/a.txt","content":"alpha\nbeta"}`)
	if !strings.Contains(out, "a.txt") {
		t.Errorf("write output = %q", out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "sub", "a.txt"))
	if err != nil || string(data) != "alpha\nbeta" {
		t.Fatalf("file = %q, %v", data, err)
	}
	read := run(t, &Read{}, env, `{"path":"sub/a.txt"}`)
	if !strings.Contains(read, "1| alpha") || !strings.Contains(read, "2| beta") {
		t.Errorf("read output = %q", read)
	}
	if !strings.Contains(read, "lines 1-2 of 2") {
		t.Errorf("read header missing: %q", read)
	}
}

func TestReadOffsetLimit(t *testing.T) {
	dir := t.TempDir()
	env := testEnv(dir)
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, "line")
	}
	_ = os.WriteFile(filepath.Join(dir, "f.txt"), []byte(strings.Join(lines, "\n")), 0o644)
	out := run(t, &Read{}, env, `{"path":"f.txt","offset":10,"limit":5}`)
	if !strings.Contains(out, "lines 10-14 of 50") {
		t.Errorf("offset read output = %q", out)
	}
}

func TestEditUniqueAndAmbiguous(t *testing.T) {
	dir := t.TempDir()
	env := testEnv(dir)
	_ = os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one two one"), 0o644)

	err := runErr(t, &Edit{}, env, `{"path":"f.txt","old_string":"one","new_string":"1"}`)
	if !strings.Contains(err.Error(), "matches 2 times") {
		t.Errorf("ambiguous edit error = %v", err)
	}
	run(t, &Edit{}, env, `{"path":"f.txt","old_string":"one two","new_string":"ONE TWO"}`)
	data, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if string(data) != "ONE TWO one" {
		t.Fatalf("after unique edit = %q", data)
	}
	run(t, &Edit{}, env, `{"path":"f.txt","old_string":"one","new_string":"1","replace_all":true}`)
	data, _ = os.ReadFile(filepath.Join(dir, "f.txt"))
	if string(data) != "ONE TWO 1" {
		t.Fatalf("after replace_all = %q", data)
	}
	err = runErr(t, &Edit{}, env, `{"path":"f.txt","old_string":"missing","new_string":"x"}`)
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing old_string error = %v", err)
	}
}

func TestWritePermissionDenied(t *testing.T) {
	dir := t.TempDir()
	env := &Env{Dir: dir, Permit: func(string, string) bool { return false }}
	err := runErr(t, &Write{}, env, `{"path":"x.txt","content":"y"}`)
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "x.txt")); !os.IsNotExist(statErr) {
		t.Errorf("file should not be written")
	}
}

func TestEditIdenticalStrings(t *testing.T) {
	dir := t.TempDir()
	env := testEnv(dir)
	_ = os.WriteFile(filepath.Join(dir, "f.txt"), []byte("abc"), 0o644)
	err := runErr(t, &Edit{}, env, `{"path":"f.txt","old_string":"abc","new_string":"abc"}`)
	if !strings.Contains(err.Error(), "identical") {
		t.Errorf("error = %v", err)
	}
}

func TestTruncateLines(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "x")
	}
	out := Truncate(strings.Join(lines, "\n"), 10, 0.5, 0)
	if !strings.Contains(out, "[90 lines omitted]") {
		t.Errorf("truncate output = %q", out)
	}
	if got := strings.Count(out, "\n") + 1; got != 11 {
		t.Errorf("truncated line count = %d, want 11", got)
	}
	if short := Truncate("a\nb", 10, 0.5, 0); short != "a\nb" {
		t.Errorf("short input should pass through, got %q", short)
	}
}

func TestGlobAndGrep(t *testing.T) {
	dir := t.TempDir()
	env := testEnv(dir)
	_ = os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n// TODO fix\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# hi\n"), 0o644)

	globOut := run(t, &Glob{}, env, `{"pattern":"**/*.go"}`)
	if !strings.Contains(globOut, "src/main.go") {
		t.Errorf("glob output = %q", globOut)
	}
	grepOut := run(t, &Grep{}, env, `{"pattern":"TODO"}`)
	if !strings.Contains(grepOut, "main.go") || !strings.Contains(grepOut, "TODO fix") {
		t.Errorf("grep output = %q", grepOut)
	}
}

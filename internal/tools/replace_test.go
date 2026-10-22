package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceAcrossFilesAndGlobs(t *testing.T) {
	dir := t.TempDir()
	env := testEnv(dir)
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "color: red\ncolor: red\n")
	write("nested/b.txt", "color: red\n")
	write("nested/c.md", "no match\n")
	out := run(t, &Replace{}, env, `{"paths":["*.txt","nested"],"pattern":"color: red","replacement":"color: blue"}`)
	if !strings.Contains(out, "3 occurrence(s) in 2 file(s)") {
		t.Fatalf("output = %q", out)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.txt")); strings.Count(string(got), "blue") != 2 {
		t.Fatalf("a.txt = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "nested", "b.txt")); !strings.Contains(string(got), "blue") {
		t.Fatalf("b.txt = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "nested", "c.md")); !strings.Contains(string(got), "no match") {
		t.Fatalf("c.md = %q", got)
	}
}

func TestReplaceRegexCaptureAndLimit(t *testing.T) {
	dir := t.TempDir()
	env := testEnv(dir)
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("name: alpha\nname: beta\nname: gamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := run(t, &Replace{}, env, `{"paths":["main.go"],"pattern":"name: (\\w+)","replacement":"value: $1","regex":true,"count":2}`)
	if !strings.Contains(out, "2 occurrence(s)") {
		t.Fatalf("output = %q", out)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "value: alpha\nvalue: beta\nname: gamma\n" {
		t.Fatalf("main.go = %q", got)
	}
	run(t, &Replace{}, env, `{"paths":["main.go"],"pattern":"value: ALPHA","replacement":"literal $1","ignore_case":true}`)
	got, _ = os.ReadFile(path)
	if !strings.HasPrefix(string(got), "literal $1\n") {
		t.Fatalf("literal replacement = %q", got)
	}
}

func TestReplaceReportsNoMatches(t *testing.T) {
	dir := t.TempDir()
	env := testEnv(dir)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := run(t, &Replace{}, env, `{"paths":["a.txt"],"pattern":"absent","replacement":"x"}`)
	if !strings.Contains(out, "no matches") {
		t.Fatalf("output = %q", out)
	}
}

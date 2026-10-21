package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSymbolsGoDefsAndRefs(t *testing.T) {
	dir := t.TempDir()
	src := `package demo

type Thing struct{}

func (t *Thing) Run() {}

func Helper(x int) int { return x }
`
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	env := testEnv(dir)
	out := run(t, &Symbols{}, env, `{"action":"defs","name":"Helper","path":"."}`)
	if !strings.Contains(out, "a.go:7") || !strings.Contains(out, "func") || !strings.Contains(out, "Helper") {
		t.Fatalf("defs = %q", out)
	}
	out = run(t, &Symbols{}, env, `{"action":"refs","name":"Thing","path":"."}`)
	if !strings.Contains(out, "references") {
		t.Fatalf("refs = %q", out)
	}
	out = run(t, &Symbols{}, env, `{"action":"outline","path":"a.go"}`)
	if !strings.Contains(out, "Run") || !strings.Contains(out, "Thing") {
		t.Fatalf("outline = %q", out)
	}
}

func TestSymbolsJSTypeScript(t *testing.T) {
	dir := t.TempDir()
	src := "export function load() {}\nclass Store {}\nconst handler = async () => {}\n"
	if err := os.WriteFile(filepath.Join(dir, "app.ts"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	env := testEnv(dir)
	out := run(t, &Symbols{}, env, `{"action":"defs","path":"."}`)
	for _, marker := range []string{"load", "Store", "handler"} {
		if !strings.Contains(out, marker) {
			t.Errorf("missing %s in %q", marker, out)
		}
	}
}

func TestVerifyDetectsAndRuns(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte("package x\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &Env{Dir: dir, Permit: func(string, string) Decision { return Decision{Allow: true} }}
	raw, _ := json.Marshal(verifyArgs{Kind: "test"})
	out, err := (&Verify{}).Run(context.Background(), raw, env)
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	if !strings.Contains(out, "project: go") || !strings.Contains(out, "go test ./...") || !strings.Contains(out, "0 failed") {
		t.Fatalf("output = %q", out)
	}
}

func TestVerifyReportsFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte("package x\n\nfunc Bad() { this is not go }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &Env{Dir: dir, Permit: func(string, string) Decision { return Decision{Allow: true} }}
	raw, _ := json.Marshal(verifyArgs{Kind: "test"})
	out, err := (&Verify{}).Run(context.Background(), raw, env)
	if err == nil {
		t.Fatalf("expected failure, got %q", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Fatalf("output = %q", out)
	}
}

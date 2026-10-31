package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

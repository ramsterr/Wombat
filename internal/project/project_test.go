package project

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectGo(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/x\n")
	p := Detect(dir)
	if p.Kind != "go" {
		t.Fatalf("kind = %q", p.Kind)
	}
	if p.Commands.Test != "go test ./..." || p.Commands.Build != "go build ./..." {
		t.Fatalf("commands = %+v", p.Commands)
	}
	if p.Commands.Typecheck != "go vet ./..." {
		t.Fatalf("typecheck = %q", p.Commands.Typecheck)
	}
}

func TestDetectNodeScripts(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"scripts":{"test":"vitest","build":"vite build","lint":"eslint ."}}`)
	write(t, dir, "pnpm-lock.yaml", "")
	p := Detect(dir)
	if p.Kind != "node" || p.Manager != "pnpm" {
		t.Fatalf("project = %+v", p)
	}
	if p.Commands.Test != "pnpm run test" || p.Commands.Lint != "pnpm run lint" {
		t.Fatalf("commands = %+v", p.Commands)
	}
}

func TestDetectNodeTypecheckFallback(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"scripts":{}}`)
	write(t, dir, "tsconfig.json", "{}")
	write(t, dir, "yarn.lock", "")
	p := Detect(dir)
	if p.Commands.Typecheck != "yarn tsc --noEmit" {
		t.Fatalf("typecheck = %q", p.Commands.Typecheck)
	}
}

func TestDetectRust(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Cargo.toml", "[package]\nname = \"x\"\n")
	p := Detect(dir)
	if p.Kind != "rust" || p.Commands.Lint == "" || p.Commands.Format == "" {
		t.Fatalf("project = %+v", p)
	}
}

func TestDetectMakeTargetsFillGaps(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Makefile", "test:\n\tgo test ./...\nlint:\n\tgolangci-lint run\n")
	p := Detect(dir)
	if p.Kind != "make" || p.Commands.Test != "make test" || p.Commands.Lint != "make lint" {
		t.Fatalf("project = %+v", p)
	}
}

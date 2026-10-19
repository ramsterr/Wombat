package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeViaTool(t *testing.T, env *Env, path, content string) string {
	t.Helper()
	raw, err := json.Marshal(writeArgs{Path: path, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	out, err := (&Write{}).Run(context.Background(), raw, env)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSecretFileWarning(t *testing.T) {
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	env := &Env{Dir: dir, Permit: func(string, string) Decision { return Decision{Allow: true} }}

	out := writeViaTool(t, env, ".env", "API_KEY=secret")
	if !strings.Contains(out, "not ignored by git") {
		t.Fatalf("expected gitignore warning, got %q", out)
	}

	writeViaTool(t, env, ".gitignore", ".env\n.env.*\n!.env.example\n")
	out = writeViaTool(t, env, ".env", "API_KEY=secret")
	if strings.Contains(out, "not ignored by git") {
		t.Fatalf("unexpected warning after ignore rule: %q", out)
	}

	out = writeViaTool(t, env, ".env.example", "API_KEY=<your-key>")
	if strings.Contains(out, "not ignored by git") {
		t.Fatalf("example file should never warn: %q", out)
	}
}

func TestSecretWarningOutsideGit(t *testing.T) {
	dir := t.TempDir()
	env := &Env{Dir: dir, Permit: func(string, string) Decision { return Decision{Allow: true} }}
	out := writeViaTool(t, env, ".env", "API_KEY=secret")
	if strings.Contains(out, "not ignored by git") {
		t.Fatalf("no warning expected outside a git repository: %q", out)
	}
}

func TestIsSecretFile(t *testing.T) {
	yes := []string{".env", ".env.local", ".env.production", "id_rsa", "server.pem", "tls.key", "credentials.json", "secrets.yaml"}
	for _, p := range yes {
		if !isSecretFile(p) {
			t.Errorf("%s should be treated as a secret", p)
		}
	}
	no := []string{".env.example", ".env.sample", "main.go", "README.md", "secretary.go"}
	for _, p := range no {
		if isSecretFile(p) {
			t.Errorf("%s should not be treated as a secret", p)
		}
	}
	if _, err := os.Stat(filepath.Join(t.TempDir(), ".env")); err == nil {
		t.Fatal("unexpected file")
	}
}

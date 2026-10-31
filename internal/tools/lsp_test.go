package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func hasGopls() bool {
	if _, err := exec.LookPath("gopls"); err == nil {
		return true
	}
	paths := []string{filepath.Join(os.Getenv("GOBIN"), "gopls"), filepath.Join(os.Getenv("GOPATH"), "bin", "gopls")}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, "go", "bin", "gopls"))
	}
	for _, path := range paths {
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

func goplsWorkspace(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/demo\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package demo

func Add(a, b int) int { return a + b }

func Use() int {
	return Add(1, 2)
}
`
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, src
}

func TestLSPDefinitionReferencesAndOutline(t *testing.T) {
	if !hasGopls() {
		t.Skip("gopls is not installed")
	}
	dir, _ := goplsWorkspace(t)
	env := testEnv(dir)
	defer env.CloseLSP()

	def := run(t, &LSP{}, env, `{"action":"definition","path":"a.go","line":6,"column":9}`)
	if !strings.Contains(def, "a.go:3:") || !strings.Contains(def, "func Add") {
		t.Fatalf("definition = %q", def)
	}
	refs := run(t, &LSP{}, env, `{"action":"references","path":"a.go","line":6,"column":9}`)
	if !strings.Contains(refs, "a.go:3:") || !strings.Contains(refs, "a.go:6:") {
		t.Fatalf("references = %q", refs)
	}
	outline := run(t, &LSP{}, env, `{"action":"outline","path":"a.go"}`)
	if !strings.Contains(outline, "Add") || !strings.Contains(outline, "Use") {
		t.Fatalf("outline = %q", outline)
	}
	symbols := run(t, &LSP{}, env, `{"action":"symbols","path":".","query":"Add"}`)
	if !strings.Contains(symbols, "Add") {
		t.Fatalf("symbols = %q", symbols)
	}
	diagnostics := run(t, &LSP{}, env, `{"action":"diagnostics","path":"a.go"}`)
	if !strings.Contains(diagnostics, "diagnostic") {
		t.Fatalf("diagnostics = %q", diagnostics)
	}
	completion := run(t, &LSP{}, env, `{"action":"completion","path":"a.go","line":6,"column":9}`)
	if !strings.Contains(completion, "completion(s)") {
		t.Fatalf("completion = %q", completion)
	}
}

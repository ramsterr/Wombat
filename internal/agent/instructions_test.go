package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestStartupContextReadsCompatFiles proves the instruction-file
// compatibility fix: repos carrying Claude Code or Cursor rules get them
// injected, after Wombat's native files.
func TestStartupContextReadsCompatFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "WOMBAT.md"), "wombat rules")
	write(t, filepath.Join(dir, "AGENTS.md"), "agents rules")
	write(t, filepath.Join(dir, "CLAUDE.md"), "claude rules")
	write(t, filepath.Join(dir, ".cursorrules"), "cursor legacy rules")
	write(t, filepath.Join(dir, ".cursor", "rules", "a.mdc"), "cursor rule a")
	write(t, filepath.Join(dir, ".cursor", "rules", "b.mdc"), "cursor rule b")
	write(t, filepath.Join(dir, ".cursor", "rules", "nested", "c.md"), "must be skipped")
	write(t, filepath.Join(dir, ".github", "copilot-instructions.md"), "copilot rules")

	ctx := startupContext(dir)
	for label, want := range map[string]string{
		"(WOMBAT.md)":                       "wombat rules",
		"(AGENTS.md)":                       "agents rules",
		"(CLAUDE.md)":                       "claude rules",
		"(.cursorrules)":                    "cursor legacy rules",
		"(.cursor/rules)":                   "cursor rule a",
		"(.github/copilot-instructions.md)": "copilot rules",
	} {
		if !strings.Contains(ctx, "Project instructions "+label) || !strings.Contains(ctx, want) {
			t.Errorf("startup context missing %s / %q", label, want)
		}
	}
	if !strings.Contains(ctx, "cursor rule b") {
		t.Error("directory rules must concatenate every file")
	}
	if strings.Contains(ctx, "must be skipped") {
		t.Error("nested directories inside a rules dir must be skipped")
	}
	// Native files come before the compat files.
	for _, pair := range [][2]string{{"(WOMBAT.md)", "(CLAUDE.md)"}, {"(CLAUDE.md)", "(.cursorrules)"}, {"(.cursorrules)", "(.cursor/rules)"}, {"(.cursor/rules)", "(.github/copilot-instructions.md)"}} {
		a, b := strings.Index(ctx, "Project instructions "+pair[0]), strings.Index(ctx, "Project instructions "+pair[1])
		if a < 0 || b < 0 || a > b {
			t.Errorf("precedence broken: %s should come before %s", pair[0], pair[1])
		}
	}
}

// TestStartupContextTruncatesCompatFiles keeps the 16KB cap per source.
func TestStartupContextTruncatesCompatFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "CLAUDE.md"), strings.Repeat("c", 20*1024))
	ctx := startupContext(dir)
	if !strings.Contains(ctx, "…[truncated]") {
		t.Error("oversized compat file must be truncated")
	}
	// 16KB content plus label and marker only.
	if len(ctx) > maxInstructionBytes+512 {
		t.Errorf("truncation ineffective: len=%d", len(ctx))
	}
}

// TestStartupContextCompatBudget caps all compat sources together, so
// importing several rule sets cannot inflate the startup context.
func TestStartupContextCompatBudget(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "CLAUDE.md"), strings.Repeat("c", maxInstructionBytes+10))
	write(t, filepath.Join(dir, ".cursorrules"), "cursor legacy rules")
	ctx := startupContext(dir)
	if !strings.Contains(ctx, "c") || !strings.Contains(ctx, "…[truncated]") {
		t.Error("first compat source should load up to the budget")
	}
	if strings.Contains(ctx, "cursor legacy rules") {
		t.Error("compat budget is exhausted; the second source must be skipped")
	}
}

// TestStartupContextNativeFilesUnbudgeted: the native files keep their
// per-file cap but do not share the compat budget.
func TestStartupContextNativeFilesUnbudgeted(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "WOMBAT.md"), strings.Repeat("w", maxInstructionBytes))
	write(t, filepath.Join(dir, "CLAUDE.md"), "claude rules")
	ctx := startupContext(dir)
	if !strings.Contains(ctx, "claude rules") {
		t.Error("native files must not eat the compat budget")
	}
}

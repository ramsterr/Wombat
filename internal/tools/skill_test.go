package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUseSkillListsAndLoads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	cwd := t.TempDir()
	dir := filepath.Join(home, "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: demo\ndescription: demo skill\n---\nRun <SKILL_DIR>/run.sh."
	if err := os.WriteFile(filepath.Join(dir, "demo.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &Env{Dir: cwd}
	tool := &UseSkill{}

	list, err := tool.Run(context.Background(), json.RawMessage(`{}`), env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "demo") || !strings.Contains(list, "demo skill") {
		t.Errorf("list = %q", list)
	}

	out, err := tool.Run(context.Background(), json.RawMessage(`{"name":"/demo"}`), env)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"skill: demo", "skill_dir: " + dir, filepath.Join(dir, "run.sh"), "Follow this skill"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"name":"missing"}`), env); err == nil {
		t.Error("missing skill should error")
	}
}

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/agents"
	"github.com/ramsterr/Wombat/internal/config"
)

func TestAgentNewCreatesScaffold(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	t.Chdir(t.TempDir())

	if err := cmdAgentNew([]string{"Math Research"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "agents", "math-research.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("scaffold not written: %v", err)
	}
	def, err := config.ParseAgentJSON(data, "math-research")
	if err != nil {
		t.Fatalf("scaffold does not parse: %v", err)
	}
	if def.Name != "math-research" {
		t.Fatalf("name = %q", def.Name)
	}
	// A second create must not overwrite.
	if err := cmdAgentNew([]string{"math-research"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v", err)
	}
	if err := cmdAgentList(nil); err != nil {
		t.Fatal(err)
	}
}

func TestAgentNewPrefersProjectDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	cwd := t.TempDir()
	t.Chdir(cwd)
	if err := os.MkdirAll(filepath.Join(cwd, ".wombat"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cmdAgentNew([]string{"design"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".wombat", "agents", "design.json")); err != nil {
		t.Fatalf("project agent not created: %v", err)
	}
	defs := agents.Load(&config.Config{}, home, cwd)
	found := false
	for _, def := range defs {
		if def.Def != nil && def.Def.Name == "design" {
			found = true
		}
	}
	if !found {
		t.Fatalf("loader does not see the new project agent: %+v", defs)
	}
}

func TestPrepareTeamFailsOnUnknownSelection(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	_, _, err := prepareTeam(&config.Config{}, t.TempDir(), nil, "", nil, []string{"does-not-exist"}, false)
	if err == nil || !strings.Contains(err.Error(), "could not prepare custom agent") {
		t.Fatalf("error = %v", err)
	}
	members, problems, err := prepareTeam(&config.Config{}, t.TempDir(), nil, "", nil, nil, false)
	if err != nil || members != nil || problems != nil {
		t.Fatalf("empty selection = %v, %v, %v", members, problems, err)
	}
}

func TestSplitAgentFlags(t *testing.T) {
	got := splitAgentFlags([]string{"math, design", "review", ""})
	want := []string{"math", "design", "review"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("splitAgentFlags = %v, want %v", got, want)
	}
}

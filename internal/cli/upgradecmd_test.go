package cli

import (
	"testing"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/session"
)

func TestUpgradeRefusesTaggedReleases(t *testing.T) {
	if err := cmdUpgrade([]string{"--version", "v0.2.6"}); err == nil {
		t.Fatal("a tagged-release upgrade must be refused so the old UI cannot return")
	}
	if err := cmdUpgrade([]string{"--method", "brew"}); err == nil {
		t.Fatal("method-based release upgrades must be refused")
	}
}

func TestBuildAgentUsesSandboxOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	cfg := &config.Config{
		Provider:  "openai",
		Model:     "test-model",
		Mode:      "create",
		Providers: map[string]*config.Provider{"openai": {Kind: "openai", BaseURL: "http://127.0.0.1:1/v1"}},
	}
	sess, err := session.CreateAt(home, t.TempDir(), "openai", "test-model", "test")
	if err != nil {
		t.Fatal(err)
	}
	opts := runOpts{}
	if _, err := buildAgent(cfg, opts, sess, newPrompter(false, false, cfg)); err != nil {
		t.Fatal(err)
	}
}

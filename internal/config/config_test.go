package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	t.Setenv("WOMBAT_CONFIG", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "openai" {
		t.Fatalf("defaults = %+v", cfg)
	}
	if !cfg.Orchestrator || !cfg.ParallelAgents || !cfg.Checkpoints || !cfg.Writers {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.Mode != "create" {
		t.Fatalf("mode = %q", cfg.Mode)
	}
	if !cfg.Mouse {
		t.Fatal("mouse wheel scrolling should be on by default")
	}
	if cfg.CheckpointKeep != DefaultCheckpointKeep {
		t.Fatalf("checkpoint_keep = %d, want %d", cfg.CheckpointKeep, DefaultCheckpointKeep)
	}
	if _, ok := cfg.Providers["ollama"]; !ok {
		t.Fatalf("ollama preset missing")
	}
	for _, kind := range []string{"bash", "write", "edit", "network", "outside"} {
		if got := cfg.Permission(kind); got != "allow" {
			t.Errorf("default permission %s = %q, want allow", kind, got)
		}
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	t.Setenv("WOMBAT_CONFIG", "")
	cfg, _ := Load()
	cfg.Provider = "ollama"
	cfg.Model = "llama3"
	cfg.Providers["ollama"].NumCtx = 16384
	cfg.Permissions["bash"] = "deny"
	cfg.Orchestrator = false
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("config perms = %v, want 0600", fi.Mode().Perm())
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Provider != "ollama" || reloaded.Model != "llama3" {
		t.Fatalf("reloaded = %+v", reloaded)
	}
	if reloaded.Providers["ollama"].NumCtx != 16384 {
		t.Errorf("num_ctx not persisted")
	}
	if reloaded.Permission("bash") != "deny" {
		t.Errorf("permission not persisted")
	}
	if reloaded.Orchestrator {
		t.Errorf("orchestrator config not persisted: %+v", reloaded)
	}
}

func TestAPIKeyResolution(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	t.Setenv("WOMBAT_CONFIG", "")
	cfg, _ := Load()

	p := cfg.Providers["openai"]
	p.APIKey = "sk-config"
	if got := cfg.APIKey("openai", p); got != "sk-config" {
		t.Errorf("config key should win, got %q", got)
	}
	p.APIKey = ""
	t.Setenv("OPENAI_API_KEY", "sk-env")
	if got := cfg.APIKey("openai", p); got != "sk-env" {
		t.Errorf("env key should be used, got %q", got)
	}
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "sk-or")
	if got := cfg.APIKey("openrouter", cfg.Providers["openrouter"]); got != "sk-or" {
		t.Errorf("preset env key should be used, got %q", got)
	}
}

func TestLoadMigratesLegacyAskDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	t.Setenv("WOMBAT_CONFIG", "")
	legacy := `{"version":1,"provider":"openai","permissions":{"bash":"ask","write":"ask","edit":"ask"}}`
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"bash", "write", "edit", "network", "outside"} {
		if got := cfg.Permission(kind); got != "allow" {
			t.Errorf("%s = %q, want allow", kind, got)
		}
	}
	if cfg.Version != configVersion {
		t.Errorf("version = %d, want %d", cfg.Version, configVersion)
	}
}

func TestLoadKeepsExplicitDenyDuringMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	t.Setenv("WOMBAT_CONFIG", "")
	legacy := `{"version":1,"provider":"openai","permissions":{"bash":"ask","write":"deny","edit":"ask"}}`
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Permission("write") != "deny" {
		t.Errorf("explicit deny was overwritten: %q", cfg.Permission("write"))
	}
}

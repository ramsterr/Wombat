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
	if !cfg.Orchestrator {
		t.Fatalf("orchestrator defaults = %+v", cfg)
	}
	if cfg.MaxParallelScouts != DefaultMaxParallelScouts {
		t.Fatalf("worker defaults = %+v", cfg)
	}
	if !cfg.Checkpoints || cfg.RequireVerification {
		t.Fatalf("safety defaults = %+v", cfg)
	}
	if cfg.Permissions["bash"] != "allow" || cfg.Permissions["write"] != "allow" {
		t.Fatalf("permissions should default to allow: %+v", cfg.Permissions)
	}
	if cfg.MaxParallelWriters != DefaultMaxParallelWriters {
		t.Fatalf("writer defaults = %+v", cfg)
	}
	if _, ok := cfg.Providers["ollama"]; !ok {
		t.Fatalf("ollama preset missing")
	}
	if cfg.Permission("bash") != "allow" || cfg.Permission("write") != "allow" {
		t.Fatalf("default permissions wrong: %+v", cfg.Permissions)
	}
	if cfg.Mouse {
		t.Fatal("mouse reporting must be off by default so text selection works")
	}
	if cfg.Version != 3 {
		t.Fatalf("config version = %d, want 3", cfg.Version)
	}
}

func TestOldConfigTurnsOffMouse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	t.Setenv("WOMBAT_CONFIG", "")
	raw := `{"version":2,"provider":"openai","model":"gpt-5","mouse":true}`
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mouse {
		t.Error("an old config with mouse:true should be migrated to selection-friendly mouse:false")
	}
	if cfg.Version != 3 {
		t.Errorf("migrated version = %d, want 3", cfg.Version)
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
	cfg.MaxParallelScouts = 2
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
	if reloaded.Orchestrator || reloaded.MaxParallelScouts != 2 {
		t.Errorf("orchestrator config not persisted: %+v", reloaded)
	}
	if reloaded.MaxParallelScouts != 2 {
		t.Errorf("worker config not persisted: %+v", reloaded)
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

func TestOldConfigGetsSimplePermissions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	t.Setenv("WOMBAT_CONFIG", "")
	path := Path()
	old := `{"version":1,"provider":"openai","model":"m","permissions":{"bash":"ask","write":"ask","edit":"ask"}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Permissions["bash"] != "allow" || cfg.Permissions["write"] != "allow" {
		t.Errorf("an old config should get the simple permissions: %+v", cfg.Permissions)
	}
	if cfg.Model != "m" {
		t.Errorf("the migration must not touch the model: %q", cfg.Model)
	}
}

func TestFullWorkersDefaultOn(t *testing.T) {
	if !Defaults().FullWorkers() {
		t.Fatal("full worker access should default on")
	}
	off := false
	cfg := Defaults()
	cfg.WorkerFullAccess = &off
	if cfg.FullWorkers() {
		t.Fatal("worker_full_access=false should disable full access")
	}
}

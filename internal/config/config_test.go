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
	if cfg.Provider != "openai" || cfg.MaxSteps != 64 {
		t.Fatalf("defaults = %+v", cfg)
	}
	if !cfg.Orchestrator || cfg.MaxDelegations != DefaultMaxDelegations || cfg.WorkerMaxSteps != DefaultWorkerMaxSteps {
		t.Fatalf("orchestrator defaults = %+v", cfg)
	}
	if cfg.MaxParallelScouts != DefaultMaxParallelScouts || cfg.WorkerTokenBudget != DefaultWorkerTokenBudget || cfg.TurnWorkerTokenBudget != DefaultTurnWorkerTokenBudget {
		t.Fatalf("worker defaults = %+v", cfg)
	}
	if _, ok := cfg.Providers["ollama"]; !ok {
		t.Fatalf("ollama preset missing")
	}
	if cfg.Permission("bash") != "ask" || cfg.Permission("read") != "ask" {
		t.Fatalf("default permissions wrong")
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
	cfg.MaxDelegations = 7
	cfg.MaxParallelScouts = 2
	cfg.WorkerMaxSteps = 11
	cfg.WorkerTokenBudget = 50000
	cfg.TurnWorkerTokenBudget = 90000
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
	if reloaded.Orchestrator || reloaded.MaxDelegations != 7 || reloaded.WorkerMaxSteps != 11 {
		t.Errorf("orchestrator config not persisted: %+v", reloaded)
	}
	if reloaded.MaxParallelScouts != 2 || reloaded.WorkerTokenBudget != 50000 || reloaded.TurnWorkerTokenBudget != 90000 {
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

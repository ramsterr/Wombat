package cli

import (
	"bufio"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/config"
)

func TestSaveSetupProviderPersistsImmediately(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	t.Setenv("WOMBAT_CONFIG", filepath.Join(home, "config.json"))

	cfg := config.Defaults()
	pc, err := saveSetupProvider(cfg, "groq", "openai", "https://api.groq.com/openai/v1", "gsk-test")
	if err != nil {
		t.Fatal(err)
	}
	if pc == nil || pc.APIKey != "gsk-test" {
		t.Fatalf("provider = %+v", pc)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Provider != "groq" {
		t.Fatalf("active provider = %q", reloaded.Provider)
	}
	saved := reloaded.Providers["groq"]
	if saved == nil || saved.APIKey != "gsk-test" || saved.BaseURL != "https://api.groq.com/openai/v1" {
		t.Fatalf("persisted provider = %+v", saved)
	}
}

func TestSaveSetupProviderKeepsExistingKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	t.Setenv("WOMBAT_CONFIG", filepath.Join(home, "config.json"))

	cfg := config.Defaults()
	cfg.Providers["custom"] = &config.Provider{Kind: "openai", BaseURL: "https://old.example/v1", APIKey: "saved-key"}
	if _, err := saveSetupProvider(cfg, "custom", "openai", "https://new.example/v1", ""); err != nil {
		t.Fatal(err)
	}
	pc := cfg.Providers["custom"]
	if pc.APIKey != "saved-key" {
		t.Fatalf("empty key replaced the saved key: %q", pc.APIKey)
	}
	if pc.BaseURL != "https://new.example/v1" {
		t.Fatalf("base URL = %q", pc.BaseURL)
	}
}

func TestSelectSetupKeyKeepsSavedKey(t *testing.T) {
	cfg := config.Defaults()
	cfg.Providers["saved"] = &config.Provider{APIKey: "saved-key-123456"}
	preset := config.Preset{Name: "saved", Kind: "openai"}
	in := bufio.NewReader(strings.NewReader("\n"))
	if got := selectSetupKey(cfg, "saved", preset, in); got != "" {
		t.Fatalf("keeping the saved key returned %q, want an empty override", got)
	}
}

func TestSelectSetupKeyCanReplaceSavedKey(t *testing.T) {
	cfg := config.Defaults()
	cfg.Providers["saved"] = &config.Provider{APIKey: "saved-key-123456"}
	preset := config.Preset{Name: "saved", Kind: "openai"}
	in := bufio.NewReader(strings.NewReader("n\nnew-key\n"))
	if got := selectSetupKey(cfg, "saved", preset, in); got != "new-key" {
		t.Fatalf("replacement key = %q", got)
	}
}

func TestSelectSetupKeySavesEnvironmentKey(t *testing.T) {
	t.Setenv("TEST_PROVIDER_API_KEY", "env-key")
	cfg := config.Defaults()
	preset := config.Preset{Name: "test-provider", Kind: "openai", KeyEnv: "TEST_PROVIDER_API_KEY"}
	in := bufio.NewReader(strings.NewReader("\n"))
	if got := selectSetupKey(cfg, "test-provider", preset, in); got != "env-key" {
		t.Fatalf("environment key = %q", got)
	}
}

func TestSaveSetupProviderClearsModelWhenProviderChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	t.Setenv("WOMBAT_CONFIG", filepath.Join(home, "config.json"))

	cfg := config.Defaults()
	cfg.Provider = "openai"
	cfg.Model = "gpt-5"
	if _, err := saveSetupProvider(cfg, "groq", "openai", "https://api.groq.com/openai/v1", "gsk-test"); err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "" {
		t.Fatalf("model = %q, want it cleared for the new provider", cfg.Model)
	}
}

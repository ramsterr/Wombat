package cli

import (
	"testing"

	"github.com/ramsterr/Wombat/internal/config"
)

func TestApplyProviderFormKeepsSavedKey(t *testing.T) {
	cfg := &config.Config{
		Providers: map[string]*config.Provider{
			"a": {Kind: "openai", BaseURL: "https://a.example/v1", APIKey: "saved-key"},
		},
	}

	applyProviderForm(cfg, "a", "", "")
	if cfg.Provider != "a" {
		t.Fatalf("active provider = %q", cfg.Provider)
	}
	if got := cfg.Providers["a"].APIKey; got != "saved-key" {
		t.Fatalf("an empty key field must keep the saved key, got %q", got)
	}
	if got := cfg.Providers["a"].BaseURL; got != "https://a.example/v1" {
		t.Fatalf("an empty base URL must keep the saved URL, got %q", got)
	}

	applyProviderForm(cfg, "a", "", "new-key")
	if got := cfg.Providers["a"].APIKey; got != "new-key" {
		t.Fatalf("a new key should replace the saved one, got %q", got)
	}
}

func TestProviderConfigured(t *testing.T) {
	t.Setenv("PROVIDER_A_API_KEY", "")
	t.Setenv("PROVIDER_B_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	cfg := &config.Config{
		Providers: map[string]*config.Provider{
			"provider-a": {Kind: "openai", BaseURL: "https://a.example/v1", APIKey: "saved"},
			"provider-b": {Kind: "openai", BaseURL: "https://b.example/v1"},
			"local":      {Kind: "openai", BaseURL: "http://127.0.0.1:1234/v1"},
		},
	}

	if !providerConfigured(cfg, "provider-a", cfg.Providers["provider-a"]) {
		t.Fatal("a saved key should mark the provider configured")
	}
	if providerConfigured(cfg, "provider-b", cfg.Providers["provider-b"]) {
		t.Fatal("a provider without a key must not be marked configured")
	}
	if !providerConfigured(cfg, "local", cfg.Providers["local"]) {
		t.Fatal("a local provider should be configured without a key")
	}
}

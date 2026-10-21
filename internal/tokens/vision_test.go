package tokens

import "testing"

func TestSupportsImages(t *testing.T) {
	withVision := []string{
		"gpt-5", "gpt-5-mini", "gpt-4o", "gpt-4.1", "o3", "o4-mini",
		"claude-sonnet-4-5", "claude-3-7-sonnet", "gemini-2.5-pro",
		"grok-4", "pixtral-large", "llama-3.2-vision", "qwen2.5-vl",
		"deepseek-v4.1-flash",
	}
	for _, m := range withVision {
		if !SupportsImages(m) {
			t.Errorf("%s should support images", m)
		}
	}
	textOnly := []string{
		"deepseek-chat", "deepseek-reasoner", "gemma-3-27b", "grok-2-1212",
		"llama-3.1-70b", "qwen3-235b", "mistral-large-latest", "kimi-k2",
		"", "unknown-model",
	}
	for _, m := range textOnly {
		if SupportsImages(m) {
			t.Errorf("%s should not be treated as vision capable", m)
		}
	}
}

// Without a catalogue entry for the provider, the built-in families still
// answer, so capability detection keeps working offline.
func TestSupportsImagesForFallsBackToModelName(t *testing.T) {
	if !SupportsImagesFor("unknown-provider", "gpt-4.1") {
		t.Fatal("gpt-4.1 should be vision capable through the fallback")
	}
	if SupportsImagesFor("unknown-provider", "deepseek-chat") {
		t.Fatal("deepseek-chat should stay text-only through the fallback")
	}
	if SupportsImagesFor("", "gpt-4.1") != SupportsImages("gpt-4.1") {
		t.Fatal("an empty provider must fall back to the model name")
	}
}

func TestDiscoverCapabilities(t *testing.T) {
	c := Discover("claude-sonnet-4-5")
	if c.Context != 200000 || !c.Vision || c.Source != "model-name" {
		t.Fatalf("unexpected capabilities: %+v", c)
	}
	c = Discover("deepseek-v4.1-flash")
	if c.Context != 128000 || !c.Vision {
		t.Fatalf("unexpected capabilities: %+v", c)
	}
	c = Discover("")
	if c.Context != DefaultWindow || c.Vision || c.Source != "default" {
		t.Fatalf("unexpected capabilities: %+v", c)
	}
}

// Without a catalogue entry the provider-aware helpers keep the built-in
// model families, so context and vision detection still work offline.
func TestDiscoverForFallsBackToModelName(t *testing.T) {
	c := DiscoverFor("unknown-provider", "claude-sonnet-4-5")
	if c.Context != 200000 || !c.Vision || c.Source != "model-name" {
		t.Fatalf("unexpected capabilities: %+v", c)
	}
	if got := WindowFor("unknown-provider", "claude-sonnet-4-5"); got != 200000 {
		t.Fatalf("WindowFor = %d, want 200000", got)
	}
	if got := WindowFor("", "unknown-model"); got != DefaultWindow {
		t.Fatalf("WindowFor = %d, want default", got)
	}
}

package tokens

import "testing"

func TestSupportsImages(t *testing.T) {
	withVision := []string{
		"gpt-5", "gpt-5-mini", "gpt-4o", "gpt-4.1", "o3", "o4-mini",
		"claude-sonnet-4-5", "claude-3-7-sonnet", "gemini-2.5-pro",
		"grok-4", "pixtral-large", "llama-3.2-vision", "qwen2.5-vl",
	}
	for _, m := range withVision {
		if !SupportsImages(m) {
			t.Errorf("%s should support images", m)
		}
	}
	textOnly := []string{
		"deepseek-v4.1-flash", "deepseek-chat", "gemma-3-27b", "grok-2-1212",
		"llama-3.1-70b", "qwen3-235b", "mistral-large-latest", "kimi-k2",
		"", "unknown-model",
	}
	for _, m := range textOnly {
		if SupportsImages(m) {
			t.Errorf("%s should not be treated as vision capable", m)
		}
	}
}

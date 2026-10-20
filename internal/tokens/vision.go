package tokens

import "strings"

// visionModels are the model families that accept an image in the request.
// A model that is not listed here is treated as text-only: sending it an
// image makes the provider reject the whole request.
var visionModels = []string{
	"gpt-4o",
	"gpt-4.1",
	"gpt-4-turbo",
	"gpt-5",
	"gpt-5.",
	"o3",
	"o4",
	"claude-3",
	"claude-4",
	"claude-sonnet",
	"claude-opus",
	"claude-haiku",
	"gemini",
	"grok-2-vision",
	"grok-4",
	"pixtral",
	"llama-3.2-vision",
	"llama-4",
	"qwen-vl",
	"qwen2-vl",
	"qwen2.5-vl",
	"kimi-vl",
	"internvl",
	"glm-4v",
	"minimax-vl",
	"nova-lite",
	"nova-pro",
}

// textOnlyModels are families that are known to be text-only, even when the
// name would otherwise look like a vision model.
var textOnlyModels = []string{
	"deepseek",
	"grok-2-",
	"gemma-3",
}

// SupportsImages reports whether a model can look at an image.
func SupportsImages(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return false
	}
	for _, blocked := range textOnlyModels {
		if strings.Contains(m, blocked) {
			return false
		}
	}
	for _, ok := range visionModels {
		if strings.Contains(m, ok) {
			return true
		}
	}
	return false
}

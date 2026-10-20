package llm

import "strings"

// reasoningEffort maps a level to the OpenAI reasoning_effort value. An empty
// level means "use the provider default", which sends nothing.
func reasoningEffort(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "off", "none", "minimal":
		return "minimal"
	case "low":
		return "low"
	case "medium", "default":
		return "medium"
	case "high", "max":
		return "high"
	}
	return ""
}

// thinkingBudget maps a level to an Anthropic thinking budget. Off disables
// thinking entirely; the others scale the budget with the output limit.
func thinkingBudget(level string, maxTokens int) (int, bool) {
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "off", "none":
		return 0, true
	case "low":
		return maxTokens / 2, true
	case "medium", "default":
		return maxTokens, true
	case "high", "max":
		return maxTokens * 2, true
	}
	return 0, false
}

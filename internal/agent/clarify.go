package agent

import "strings"

// IsVaguePrompt is a cheap heuristic for requests that usually need one
// clarifying question. It deliberately does not try to classify every task;
// the model still decides whether the answer changes the plan.
func IsVaguePrompt(prompt string) bool {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return false
	}
	if len([]rune(trimmed)) <= 18 || len(strings.Fields(trimmed)) <= 3 {
		return true
	}
	lower := strings.ToLower(trimmed)
	for _, phrase := range []string{
		"make it better", "improve this", "fix this", "fix it",
		"build an app", "build a website", "create a website",
		"write some code", "do something", "help me with this",
		"make a plan", "make a ui", "build a ui", "make a game",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// ClarifyNote tells the model when to ask one question and when not to ask at
// all. Headless runs never ask; they state assumptions instead.
func ClarifyNote(prompt string, interactive bool) string {
	if !interactive {
		return "No user is available to answer questions. Do not ask; choose the most reasonable interpretation and state your assumptions."
	}
	if !IsVaguePrompt(prompt) {
		return ""
	}
	return "This request is underspecified. Ask one concise question with 2-3 concrete options before starting, but only if the answer changes the plan. Do not ask about things you can discover from the workspace. Do not ask more than one round; if the answer is still ambiguous, choose the most reasonable interpretation and state it."
}

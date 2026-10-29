package orchestrator

import (
	"strings"

	"github.com/ramsterr/Wombat/internal/llm"
)

// inputNote puts the planner directive in front of a request that clearly has
// independent parts. The system prompt already prefers planning; this makes
// the first action concrete on the turn where it matters most, without
// persisting a synthetic user message in the session.
func (o *Orchestrator) inputNote(input string) string {
	expected := looksMultiStep(input)
	o.mu.Lock()
	o.planExpected = expected
	o.steered = false
	active := false
	if o.ledger != nil {
		for _, step := range o.ledger.Steps {
			if step.Status == StepPending || step.Status == StepInProgress {
				active = true
				break
			}
		}
	}
	maxParallel := o.cfg.MaxParallelScouts
	o.mu.Unlock()
	if !expected {
		return ""
	}
	if active {
		return "## Planner directive\nThis request extends an active ledger. Update the ledger with update_plan when the scope changes, then run_plan. Keep todo_write current."
	}
	var b strings.Builder
	b.WriteString("## Planner directive\n")
	b.WriteString("This request has independent parts. Your first tool call must be update_plan: 2-6 concrete steps, explicit depends_on, and a deliverable per step. ")
	if maxParallel > 1 {
		b.WriteString("Then call run_plan so ready steps run in one parallel wave. ")
	}
	b.WriteString("Do not open with a sequence of bash/read calls. For a pure research fleet with no ledger, delegate_many with 2-5 self-contained briefs is acceptable. Keep todo_write current, and synthesize every worker report before answering.")
	return b.String()
}

// stepNote is the runtime counterpart to inputNote. A prompt nudge only
// helps before the first tool call; this one catches a turn that has already
// drifted into a serial bash/read loop or kept exploring without acting.
func (o *Orchestrator) stepNote(step int, calls []llm.ToolCall) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.steered || len(calls) == 0 {
		return ""
	}
	if o.planExpected && len(calls) >= 3 {
		if hasPlanningCall(calls) {
			return ""
		}
		o.steered = true
		return "## Steering\nThis multi-step request has used several tool calls without a plan. Stop the serial read/bash loop now: call update_plan with 2-6 concrete steps, then run_plan. For independent research angles, delegate_many is the alternative. Keep todo_write current."
	}
	if len(calls) >= 10 && !hasMutationCall(calls) {
		o.steered = true
		return "## Steering\nYou have made 10 or more calls without changing anything. Stop exploring: use the evidence you have, make the smallest complete change now, then verify the result."
	}
	return ""
}

func hasPlanningCall(calls []llm.ToolCall) bool {
	for _, call := range calls {
		switch call.Name {
		case "update_plan", "run_plan", "delegate", "delegate_many":
			return true
		}
	}
	return false
}

func hasMutationCall(calls []llm.ToolCall) bool {
	for _, call := range calls {
		switch call.Name {
		case "write", "edit", "multi_edit", "apply_patch", "replace", "rename", "refactor", "imagegen", "image_edit":
			return true
		}
	}
	return false
}

// looksMultiStep is deliberately conservative. It fires when a request has
// several action clauses, not merely because it is long.
func looksMultiStep(input string) bool {
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "" {
		return false
	}
	// A direct question with no imperative is not a plan.
	first := firstWord(text)
	if isQuestionWord(first) && !hasVerb(text, imperativeVerbs) {
		return false
	}
	markers := 0
	for _, marker := range []string{
		" and ", " then ", ";", "\n- ", "\n* ", "\n1.", "\n2.", "\n3.",
		", then", " plus ", " also ", " separately", " each ",
	} {
		if strings.Contains(text, marker) {
			markers++
		}
	}
	if markers == 0 {
		return false
	}
	// Only trivial reads do not deserve a plan.
	nonTrivial := hasVerb(text, imperativeVerbs) && !onlyTrivial(text)
	return nonTrivial
}

var imperativeVerbs = []string{
	"build", "create", "make", "write", "design", "implement", "add", "fix",
	"refactor", "research", "analy", "compare", "test", "verify", "render",
	"record", "edit", "generate", "install", "deploy", "migrate", "audit",
	"review", "draft", "plan", "scaffold", "port", "convert", "publish",
	"optimize", "rewrite", "replace", "remove", "delete", "update", "change",
	"investigate", "inspect", "benchmark", "profile", "debug", "ship",
}

var trivialVerbs = []string{
	"read", "show", "tell", "explain", "summarize", "summarise", "list",
	"find", "search", "check", "run", "open", "print", "grep", "look",
	"describe", "answer",
}

func hasVerb(text string, verbs []string) bool {
	for _, verb := range verbs {
		if containsWord(text, verb) {
			return true
		}
	}
	return false
}

func onlyTrivial(text string) bool {
	if !hasVerb(text, trivialVerbs) {
		return false
	}
	for _, verb := range imperativeVerbs {
		if containsWord(text, verb) {
			return false
		}
	}
	return true
}

func containsWord(text, word string) bool {
	for _, field := range strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if field == word {
			return true
		}
	}
	return false
}

func firstWord(text string) string {
	if i := strings.IndexAny(text, " \t\n"); i >= 0 {
		return text[:i]
	}
	return text
}

func isQuestionWord(word string) bool {
	switch word {
	case "what", "why", "how", "who", "where", "when", "which", "is", "are",
		"does", "do", "can", "could", "should", "would", "will":
		return true
	}
	return false
}

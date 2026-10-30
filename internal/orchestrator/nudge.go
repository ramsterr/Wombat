package orchestrator

import (
	"encoding/json"
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
	o.screenshotSteered = false
	o.routingSteered = false
	o.noProgressSteered = false
	o.patchSteered = false
	o.perfSteered = false
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
		return "## Planner directive\nThis request extends an active ledger. For every independent read-only angle (research, file survey, comparison, audit), dispatch one delegate_many fleet with read_only=true instead of reading serially. If the new work changes the plan, update_plan then run_plan. Keep todo_write current."
	}
	var b strings.Builder
	b.WriteString("## Planner directive\n")
	b.WriteString("This request has independent parts. Your first tool call must be update_plan: 2-6 concrete steps, explicit depends_on, and a deliverable per step. ")
	if maxParallel > 1 {
		b.WriteString("Then call run_plan so ready steps run in one parallel wave. ")
	}
	b.WriteString("Do not open with a sequence of bash/read calls. For independent read-only angles, dispatch delegate_many with read_only=true in one wave; keep writes and edits sequential unless files are disjoint. Keep todo_write current, and synthesize every worker report before answering.")
	return b.String()
}

// stepNote is the runtime counterpart to inputNote. A prompt nudge only
// helps before the first tool call; this one catches a turn that has already
// drifted into a serial bash/read loop or kept exploring without acting.
func (o *Orchestrator) stepNote(step int, calls []llm.ToolCall) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(calls) == 0 {
		return ""
	}
	if !o.screenshotSteered {
		if note := screenshotBatchNudge(calls); note != "" {
			o.screenshotSteered = true
			return note
		}
	}
	if o.steered {
		return ""
	}
	if o.planExpected && len(calls) >= 3 {
		if hasPlanningCall(calls) {
			return ""
		}
		o.steered = true
		return "## Steering\nThis multi-step request has used several tool calls without a plan. Stop the serial read/bash loop now: call update_plan with 2-6 concrete steps, then run_plan. For independent read-only angles, delegate_many with read_only=true is the alternative. Keep todo_write current."
	}
	if !o.routingSteered && bashReadCount(calls) >= 3 {
		o.routingSteered = true
		o.steered = true
		return "## Tool routing\nYou have used bash for several simple file reads or searches. Use read, grep, glob, or batch instead: they return structured results, cost fewer tokens, and respect path permissions. Keep bash for execution, builds, and multi-step shell workflows."
	}
	if !o.noProgressSteered && callsSinceProgress(calls) >= 8 {
		o.noProgressSteered = true
		o.steered = true
		return "## No progress\nYou have made 8 or more calls without changing files, todos, or the plan. Stop investigating: state the blocking fact, make the smallest useful change, or finish with a partial report and what remains."
	}
	if !o.patchSteered && o.lead != nil && o.lead.Env != nil && o.lead.Env.PatchFailures() >= 2 {
		o.patchSteered = true
		o.steered = true
		return "## Patch failures\napply_patch has failed twice this turn. Stop retrying the same patch: use multi_edit for several edits in one file, replace for a literal or regex, or rewrite the whole file with write. Read the current file first if its contents may have changed."
	}
	if !o.perfSteered && perfProbeCount(calls) >= 3 {
		o.perfSteered = true
		o.steered = true
		return "## Performance loop\nYou have measured rendering performance several times. Stop micro-benchmarking: make one structural change (lower canvas resolution, reduce particles, cache static layers), verify once, and finish. If the target still misses, report the measured limit instead of continuing to optimize."
	}
	if len(calls) >= 10 && !hasMutationCall(calls) {
		o.steered = true
		return "## Steering\nYou have made 10 or more calls without changing anything. Stop exploring: use the evidence you have, make the smallest complete change now, then verify the result."
	}
	return ""
}

// screenshotBatchNudge catches the expensive screenshot -> one fix ->
// screenshot loop. A single small edit between two screenshots is not a
// review pass; the model should collect every visible defect and fix them
// together.
func screenshotBatchNudge(calls []llm.ToolCall) string {
	shots := 0
	mutations := 0
	for _, call := range calls {
		if isScreenshotCall(call) {
			if shots > 0 && mutations < 2 {
				return "## Screenshot batching\nYou took another screenshot after only one small edit. Stop the screenshot → fix → screenshot loop: list every visible defect from the latest image, fix them together with apply_patch or multi_edit, then take one verification screenshot."
			}
			shots++
			mutations = 0
			continue
		}
		mutations += mutationWeight(call.Name)
	}
	if shots >= 3 {
		return "## Screenshot batching\nYou have taken three screenshots this turn. Consolidate the remaining defects into one batch of edits, then verify once; do not keep screenshotting after each fix."
	}
	return ""
}

func isScreenshotCall(call llm.ToolCall) bool {
	if call.Name != "browser" {
		return false
	}
	var args struct {
		Action string `json:"action"`
		Steps  []struct {
			Action string `json:"action"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return false
	}
	if strings.EqualFold(args.Action, "screenshot") {
		return true
	}
	for _, step := range args.Steps {
		if strings.EqualFold(step.Action, "screenshot") {
			return true
		}
	}
	return false
}

func mutationWeight(name string) int {
	switch name {
	case "apply_patch", "multi_edit", "replace":
		return 2
	case "write", "edit", "rename", "refactor", "imagegen", "image_edit":
		return 1
	}
	return 0
}

func callsSinceProgress(calls []llm.ToolCall) int {
	n := 0
	for i := len(calls) - 1; i >= 0; i-- {
		if isProgressCall(calls[i].Name) {
			break
		}
		n++
	}
	return n
}

func isProgressCall(name string) bool {
	switch name {
	case "write", "edit", "multi_edit", "apply_patch", "replace", "rename", "refactor",
		"todo_write", "update_plan", "run_plan", "run_checks":
		return true
	}
	return false
}

func perfProbeCount(calls []llm.ToolCall) int {
	n := 0
	for _, call := range calls {
		if call.Name != "browser" {
			continue
		}
		lower := strings.ToLower(call.Arguments)
		for _, marker := range []string{"performance.now", "framerate", "requestanimationframe", "rafpersec", "avgdrawms", "drawms"} {
			if strings.Contains(lower, marker) {
				n++
				break
			}
		}
	}
	return n
}

func bashReadCount(calls []llm.ToolCall) int {
	count := 0
	for _, call := range calls {
		if call.Name != "bash" {
			continue
		}
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			continue
		}
		cmd := strings.TrimSpace(args.Command)
		if cmd == "" || strings.ContainsAny(cmd, "|;&><`") {
			continue
		}
		fields := strings.Fields(cmd)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "cat", "head", "tail", "grep", "rg", "find", "ls":
			count++
		}
	}
	return count
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

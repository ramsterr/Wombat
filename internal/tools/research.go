package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type ResearchPolicy struct {
	Mode          string
	SoftSearches  int
	SoftFetches   int
	HardSearches  int
	HardFetches   int
	MaxSeconds    int
	HardSeconds   int
	ExtendSeconds int
	MaxExtensions int
	Depth         string
}

// ResolveResearchPolicy picks a bounded research policy. Auto is deliberately
// conservative: only explicit research/comparison/verification language gets
// the standard budget; exhaustive language gets deep.
func ResolveResearchPolicy(mode, prompt string) ResearchPolicy {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" || mode == "auto" {
		mode = autoResearchMode(prompt)
	}
	switch mode {
	case "off":
		return ResearchPolicy{Mode: "off", Depth: "fast"}
	case "quick":
		return ResearchPolicy{Mode: "quick", SoftSearches: 2, SoftFetches: 1, HardSearches: 4, HardFetches: 2, MaxSeconds: 90, HardSeconds: 210, ExtendSeconds: 60, MaxExtensions: 2, Depth: "fast"}
	case "standard":
		return ResearchPolicy{Mode: "standard", SoftSearches: 5, SoftFetches: 3, HardSearches: 8, HardFetches: 4, MaxSeconds: 180, HardSeconds: 360, ExtendSeconds: 60, MaxExtensions: 3, Depth: "fast"}
	case "deep":
		return ResearchPolicy{Mode: "deep", SoftSearches: 10, SoftFetches: 6, HardSearches: 15, HardFetches: 8, MaxSeconds: 360, HardSeconds: 600, ExtendSeconds: 60, MaxExtensions: 4, Depth: "deep"}
	default:
		return ResearchPolicy{Mode: "quick", SoftSearches: 2, SoftFetches: 1, HardSearches: 4, HardFetches: 2, MaxSeconds: 90, HardSeconds: 210, ExtendSeconds: 60, MaxExtensions: 2, Depth: "fast"}
	}
}

func autoResearchMode(prompt string) string {
	lower := strings.ToLower(prompt)
	for _, trigger := range []string{"exhaustive", "deep dive", "comprehensive", "verify every", "every source", "systematic review"} {
		if strings.Contains(lower, trigger) {
			return "deep"
		}
	}
	for _, trigger := range []string{"research", "compare", "current", "latest", "verify", "cite", "sources", "evidence"} {
		if strings.Contains(lower, trigger) {
			return "standard"
		}
	}
	return "quick"
}

// ResearchNote is the system-prompt guidance for the active policy.
func ResearchNote(p ResearchPolicy) string {
	if p.Mode == "off" {
		return "Research is off for this task: answer from knowledge, state assumptions, and do not call websearch or webfetch."
	}
	return fmt.Sprintf(
		"Research mode: %s. Search/fetch budget: %d/%d (hard %d/%d). Time: %ds (hard %ds, up to %d extensions). "+
			"Answer from knowledge first for recommendations and opinions; use web only for current facts or explicit verification. "+
			"Stop and synthesize when you have enough. Use request_more_research only when the soft counts or time are exhausted; "+
			"it adds %ds and one soft search/fetch, capped by the hard limits.",
		p.Mode, p.SoftSearches, p.SoftFetches, p.HardSearches, p.HardFetches,
		p.MaxSeconds, p.HardSeconds, p.MaxExtensions, p.ExtendSeconds,
	)
}

type ResearchBudget struct {
	mu         sync.Mutex
	policy     ResearchPolicy
	searches   int
	fetches    int
	extensions int
	started    time.Time
}

func NewResearchBudget(p ResearchPolicy) *ResearchBudget {
	return &ResearchBudget{policy: p}
}

func (b *ResearchBudget) Policy() ResearchPolicy {
	if b == nil {
		return ResearchPolicy{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.policy
}

func (b *ResearchBudget) AllowSearch() error {
	return b.allow("search")
}

func (b *ResearchBudget) AllowFetch() error {
	return b.allow("fetch")
}

func (b *ResearchBudget) allow(kind string) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.policy.Mode == "off" {
		return fmt.Errorf("research is off for this task; answer from knowledge")
	}
	if b.started.IsZero() {
		b.started = time.Now()
	}
	if b.policy.MaxSeconds > 0 && time.Since(b.started) > time.Duration(b.policy.MaxSeconds)*time.Second {
		elapsed := int(time.Since(b.started).Seconds())
		if b.policy.HardSeconds > 0 && elapsed >= b.policy.HardSeconds {
			return fmt.Errorf("research time hard ceiling reached (%ds/%ds); synthesize now", elapsed, b.policy.HardSeconds)
		}
		if b.policy.MaxExtensions > 0 && b.extensions >= b.policy.MaxExtensions {
			return fmt.Errorf("research time budget reached (%ds/%ds) and the %d extension limit is used; synthesize now", elapsed, b.policy.MaxSeconds, b.policy.MaxExtensions)
		}
		return fmt.Errorf("research time budget reached (%ds/%ds); synthesize now with what you have, or call request_more_research with a specific reason", elapsed, b.policy.MaxSeconds)
	}
	soft, hard, used := b.policy.SoftSearches, b.policy.HardSearches, b.searches
	if kind == "fetch" {
		soft, hard, used = b.policy.SoftFetches, b.policy.HardFetches, b.fetches
	}
	if used >= hard {
		return fmt.Errorf("research %s hard ceiling reached (%d/%d); synthesize now", kind, used, hard)
	}
	if used >= soft {
		return fmt.Errorf("research %s soft budget reached (%d/%d); synthesize now or call request_more_research with a specific reason", kind, used, soft)
	}
	if kind == "fetch" {
		b.fetches++
	} else {
		b.searches++
	}
	return nil
}

func (b *ResearchBudget) Extend(reason string) error {
	if b == nil {
		return fmt.Errorf("research budget is unavailable")
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("reason is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.policy.Mode == "off" {
		return fmt.Errorf("research is off for this task")
	}
	if b.policy.MaxExtensions > 0 && b.extensions >= b.policy.MaxExtensions {
		return fmt.Errorf("research extension limit reached (%d/%d); synthesize now", b.extensions, b.policy.MaxExtensions)
	}
	if !b.exhaustedLocked() {
		return fmt.Errorf("research budget is not exhausted yet; use the current search/fetch and time allowance first")
	}
	elapsed := 0
	if !b.started.IsZero() {
		elapsed = int(time.Since(b.started).Seconds())
	}
	if b.policy.HardSeconds > 0 && elapsed >= b.policy.HardSeconds {
		return fmt.Errorf("research time hard ceiling reached (%ds/%ds); synthesize now", elapsed, b.policy.HardSeconds)
	}
	if b.searches >= b.policy.HardSearches && b.fetches >= b.policy.HardFetches {
		return fmt.Errorf("research search/fetch hard ceiling already reached; synthesize now")
	}
	if b.policy.SoftSearches < b.policy.HardSearches {
		b.policy.SoftSearches++
	}
	if b.policy.SoftFetches < b.policy.HardFetches {
		b.policy.SoftFetches++
	}
	if b.policy.ExtendSeconds > 0 && b.policy.MaxSeconds < b.policy.HardSeconds {
		b.policy.MaxSeconds += b.policy.ExtendSeconds
		if b.policy.MaxSeconds > b.policy.HardSeconds {
			b.policy.MaxSeconds = b.policy.HardSeconds
		}
	}
	b.extensions++
	return nil
}

// exhaustedLocked reports whether the current allowance is actually used up,
// so request_more_research cannot be called just to stockpile budget.
func (b *ResearchBudget) exhaustedLocked() bool {
	if b.searches >= b.policy.SoftSearches || b.fetches >= b.policy.SoftFetches {
		return true
	}
	if b.policy.MaxSeconds > 0 && !b.started.IsZero() {
		remaining := time.Duration(b.policy.MaxSeconds)*time.Second - time.Since(b.started)
		near := time.Duration(b.policy.ExtendSeconds) * time.Second / 2
		if near <= 0 {
			near = 15 * time.Second
		}
		return remaining <= near
	}
	return false
}

func (b *ResearchBudget) Status() string {
	if b == nil {
		return "research: unavailable"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	elapsed := 0
	if !b.started.IsZero() {
		elapsed = int(time.Since(b.started).Seconds())
	}
	return fmt.Sprintf("%s: %d/%d searches, %d/%d fetches (hard %d/%d), %ds/%ds (hard %ds), %d/%d extensions",
		b.policy.Mode, b.searches, b.policy.SoftSearches, b.fetches, b.policy.SoftFetches,
		b.policy.HardSearches, b.policy.HardFetches, elapsed, b.policy.MaxSeconds, b.policy.HardSeconds,
		b.extensions, b.policy.MaxExtensions)
}

func (e *Env) SetResearchBudget(p ResearchPolicy) {
	if e == nil {
		return
	}
	e.research = NewResearchBudget(p)
}

func (e *Env) AllowSearch() error {
	if e == nil || e.research == nil {
		return nil
	}
	return e.research.AllowSearch()
}

func (e *Env) AllowFetch() error {
	if e == nil || e.research == nil {
		return nil
	}
	return e.research.AllowFetch()
}

func (e *Env) ExtendResearch(reason string) (string, error) {
	if e == nil || e.research == nil {
		return "", fmt.Errorf("research budget is unavailable")
	}
	if err := e.research.Extend(reason); err != nil {
		return "", err
	}
	return e.research.Status(), nil
}

func (e *Env) ResearchStatus() string {
	if e == nil || e.research == nil {
		return "research: unavailable"
	}
	return e.research.Status()
}

func (e *Env) ResearchDepth() string {
	if e == nil || e.research == nil {
		return ""
	}
	return e.research.Policy().Depth
}

type questionState struct {
	mu    sync.Mutex
	asked int
	max   int
}

func (e *Env) SetQuestionBudget(max int) {
	if e == nil {
		return
	}
	e.questions = &questionState{max: max}
}

func (e *Env) AllowQuestion() error {
	if e == nil || e.questions == nil {
		return nil
	}
	e.questions.mu.Lock()
	defer e.questions.mu.Unlock()
	if e.questions.max <= 0 || e.questions.asked < e.questions.max {
		e.questions.asked++
		return nil
	}
	return fmt.Errorf("question budget reached (%d); make a reasonable assumption and proceed", e.questions.max)
}

type RequestMoreResearch struct{}

func (t *RequestMoreResearch) Name() string  { return "request_more_research" }
func (t *RequestMoreResearch) Class() string { return "read" }
func (t *RequestMoreResearch) Description() string {
	return "Ask for more research time and one more soft search/fetch when the current allowance is exhausted and you are genuinely blocked. Give a specific reason. The request is capped by the hard limits and a small extension count."
}
func (t *RequestMoreResearch) Schema() map[string]any {
	return obj(map[string]any{
		"reason": desc("why the current evidence is insufficient"),
	}, "reason")
}
func (t *RequestMoreResearch) Run(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
	var a struct {
		Reason string `json:"reason"`
	}
	if err := decodeInto(raw, &a); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	status, err := env.ExtendResearch(a.Reason)
	if err != nil {
		return "", err
	}
	return "Research budget extended. " + status + ". Stop when you have enough.", nil
}
func (t *RequestMoreResearch) AllowedInWorkers() bool { return false }

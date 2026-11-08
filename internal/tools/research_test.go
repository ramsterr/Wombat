package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestResolveResearchPolicy(t *testing.T) {
	cases := []struct {
		mode   string
		prompt string
		want   string
	}{
		{"auto", "make it better", "quick"},
		{"auto", "research the latest", "standard"},
		{"auto", "exhaustive comparison", "deep"},
		{"off", "", "off"},
	}
	for _, tc := range cases {
		got := ResolveResearchPolicy(tc.mode, tc.prompt)
		if got.Mode != tc.want {
			t.Errorf("policy(%q, %q) = %s, want %s", tc.mode, tc.prompt, got.Mode, tc.want)
		}
	}
}

func TestResearchTimerStartsOnFirstCall(t *testing.T) {
	b := NewResearchBudget(ResolveResearchPolicy("quick", ""))
	if !b.started.IsZero() {
		t.Fatal("research timer should not start until the first research call")
	}
}

func TestResearchBudget(t *testing.T) {
	env := &Env{Dir: t.TempDir()}
	env.SetResearchBudget(ResolveResearchPolicy("quick", ""))
	if err := env.AllowSearch(); err != nil {
		t.Fatal(err)
	}
	if err := env.AllowSearch(); err != nil {
		t.Fatal(err)
	}
	if err := env.AllowSearch(); err == nil {
		t.Fatal("third search should hit the soft budget")
	}
	if err := env.AllowFetch(); err != nil {
		t.Fatal(err)
	}
	if err := env.AllowFetch(); err == nil {
		t.Fatal("second fetch should hit the soft budget")
	}
	if _, err := env.ExtendResearch("need one more source"); err != nil {
		t.Fatal(err)
	}
	if err := env.AllowSearch(); err != nil {
		t.Fatalf("extended search failed: %v", err)
	}
	if !strings.Contains(env.ResearchStatus(), "quick") {
		t.Fatalf("status = %q", env.ResearchStatus())
	}
}

func TestResearchOff(t *testing.T) {
	env := &Env{Dir: t.TempDir()}
	env.SetResearchBudget(ResolveResearchPolicy("off", ""))
	if err := env.AllowSearch(); err == nil {
		t.Fatal("search should be blocked when research is off")
	}
	raw, _ := json.Marshal(map[string]string{"query": "x"})
	if _, err := (&WebSearch{}).Run(context.Background(), raw, env); err == nil {
		t.Fatal("websearch should be blocked when research is off")
	}
	raw, _ = json.Marshal(map[string]string{"url": "https://example.com"})
	if _, err := (&WebFetch{}).Run(context.Background(), raw, env); err == nil {
		t.Fatal("webfetch should be blocked when research is off")
	}
}

func TestResearchNote(t *testing.T) {
	note := ResearchNote(ResolveResearchPolicy("quick", ""))
	if !strings.Contains(note, "quick") || !strings.Contains(note, "Search/fetch budget") {
		t.Fatalf("note = %q", note)
	}
	if !strings.Contains(ResearchNote(ResolveResearchPolicy("off", "")), "Research is off") {
		t.Fatal("off note missing")
	}
}

func TestQuestionBudget(t *testing.T) {
	env := &Env{Dir: t.TempDir()}
	env.SetQuestionBudget(1)
	if err := env.AllowQuestion(); err != nil {
		t.Fatal(err)
	}
	if err := env.AllowQuestion(); err == nil {
		t.Fatal("second question should exceed the budget")
	}
	env.AskUser = func(Question) (string, error) { return "A", nil }
	raw := json.RawMessage(`{"questions":[{"question":"Which?","options":[{"label":"A"},{"label":"B"}]}]}`)
	if _, err := (&QuestionTool{}).Run(context.Background(), raw, env); err == nil {
		t.Fatal("question tool should enforce the budget")
	}
}

func TestResearchExtendAddsTime(t *testing.T) {
	policy := ResolveResearchPolicy("quick", "")
	b := NewResearchBudget(policy)
	if err := b.AllowSearch(); err != nil {
		t.Fatal(err)
	}
	if err := b.AllowSearch(); err != nil {
		t.Fatal(err)
	}
	if err := b.AllowFetch(); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.started = time.Now().Add(-time.Duration(policy.MaxSeconds+1) * time.Second)
	b.mu.Unlock()

	if err := b.AllowSearch(); err == nil || !strings.Contains(err.Error(), "time budget") {
		t.Fatalf("expected a time-budget error, got %v", err)
	}
	if err := b.Extend("the remaining fact needs one more source"); err != nil {
		t.Fatalf("extend: %v", err)
	}
	if got := b.Policy().MaxSeconds; got != policy.MaxSeconds+policy.ExtendSeconds {
		t.Fatalf("MaxSeconds = %d, want %d", got, policy.MaxSeconds+policy.ExtendSeconds)
	}
	if err := b.AllowSearch(); err != nil {
		t.Fatalf("search after manual time extension: %v", err)
	}
	if status := b.Status(); !strings.Contains(status, "150s") || !strings.Contains(status, "1/2 extensions") {
		t.Fatalf("status = %q", status)
	}
}

func TestResearchExtendRefusesStockpiling(t *testing.T) {
	b := NewResearchBudget(ResolveResearchPolicy("quick", ""))
	if err := b.AllowSearch(); err != nil {
		t.Fatal(err)
	}
	if err := b.Extend("just in case"); err == nil || !strings.Contains(err.Error(), "not exhausted") {
		t.Fatalf("extension before exhaustion should fail, got %v", err)
	}
	if got := b.Policy().MaxSeconds; got != ResolveResearchPolicy("quick", "").MaxSeconds {
		t.Fatalf("stockpiled extension changed time to %d", got)
	}
}

func TestResearchExtensionLimit(t *testing.T) {
	p := ResearchPolicy{
		Mode: "quick", SoftSearches: 0, SoftFetches: 0,
		HardSearches: 100, HardFetches: 100,
		MaxSeconds: 1, HardSeconds: 1000, ExtendSeconds: 60, MaxExtensions: 2,
	}
	b := NewResearchBudget(p)
	forceExhausted := func() {
		b.mu.Lock()
		b.started = time.Now().Add(-time.Duration(b.policy.MaxSeconds+1) * time.Second)
		b.mu.Unlock()
	}
	forceExhausted()
	if err := b.Extend("one"); err != nil {
		t.Fatal(err)
	}
	forceExhausted()
	if err := b.Extend("two"); err != nil {
		t.Fatal(err)
	}
	if err := b.Extend("three"); err == nil || !strings.Contains(err.Error(), "extension limit") {
		t.Fatalf("third extension should be capped, got %v", err)
	}
	if got := b.Policy().MaxSeconds; got != 121 {
		t.Fatalf("MaxSeconds = %d, want 121", got)
	}
}

func TestResearchNoteMentionsTimeAndExtensions(t *testing.T) {
	note := ResearchNote(ResolveResearchPolicy("quick", ""))
	for _, want := range []string{"Time:", "extensions", "request_more_research"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note missing %q: %s", want, note)
		}
	}
}

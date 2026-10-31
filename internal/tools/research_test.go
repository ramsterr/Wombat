package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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

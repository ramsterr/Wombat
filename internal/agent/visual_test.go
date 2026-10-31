package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

func TestDetectVisualArtifact(t *testing.T) {
	cases := []struct {
		name   string
		args   string
		result string
		ok     bool
		kind   string
	}{
		{"write", `{"path":"index.html","content":"<h1>hi</h1>"}`, "wrote index.html", true, "html"},
		{"edit", `{"path":"report.pdf"}`, "edited report.pdf", true, "pdf"},
		{"write", `{"path":"main.go"}`, "wrote main.go", false, ""},
		{"read", `{"path":"index.html"}`, "<h1>old</h1>", false, ""},
		{"bash", `{"command":"npm run dev"}`, "VITE ready in 200 ms\nLocal: http://localhost:5173/", true, "site"},
		{"bash", `{"command":"cat old.log"}`, "http://localhost:5173/", false, ""},
		{"browser", `{"action":"open","url":"http://127.0.0.1:8080/"}`, "opened http://127.0.0.1:8080/", true, "site"},
		{"write", `{"path":"main.go"}`, "error: permission denied", false, ""},
	}
	for _, tc := range cases {
		call := llm.ToolCall{Name: tc.name, Arguments: tc.args}
		art, ok := detectVisualArtifact(call, tc.result)
		if ok != tc.ok || art.Kind != tc.kind {
			t.Errorf("%s %s: got (%+v, %v), want kind %q ok %v", tc.name, tc.args, art, ok, tc.kind, tc.ok)
		}
	}
}

func newVisualAgent(t *testing.T, answers ...string) (*Agent, *session.Session) {
	t.Helper()
	t.Setenv("WOMBAT_HOME", t.TempDir())
	dir := t.TempDir()
	sess, err := session.Create(dir, "fake", "fake-model", "visual")
	if err != nil {
		t.Fatal(err)
	}
	i := 0
	a := &Agent{
		Sess: sess,
		Env: &tools.Env{
			Dir: dir,
			AskUser: func(tools.Question) (string, error) {
				if i >= len(answers) {
					return "", nil
				}
				ans := answers[i]
				i++
				return ans, nil
			},
		},
	}
	return a, sess
}

func TestVisualReviewStopsOnApproval(t *testing.T) {
	a, sess := newVisualAgent(t, "Looks good — ship it")
	calls := []llm.ToolCall{{Name: "write", Arguments: `{"path":"index.html"}`}}
	results := []toolResult{{content: "wrote index.html"}}
	stop, err := a.visualReview(context.Background(), calls, results)
	if err != nil {
		t.Fatal(err)
	}
	if !stop {
		t.Fatal("approval should stop the run")
	}
	last := sess.Messages[len(sess.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "approved") {
		t.Fatalf("approval was not recorded: %+v", last)
	}
}

func TestVisualReviewAsksForChanges(t *testing.T) {
	a, sess := newVisualAgent(t, "Needs changes — I'll describe", "make the heading larger")
	calls := []llm.ToolCall{{Name: "write", Arguments: `{"path":"index.html"}`}}
	results := []toolResult{{content: "wrote index.html"}}
	stop, err := a.visualReview(context.Background(), calls, results)
	if err != nil {
		t.Fatal(err)
	}
	if stop {
		t.Fatal("feedback should continue the run")
	}
	last := sess.Messages[len(sess.Messages)-1]
	if !strings.Contains(last.Content, "make the heading larger") {
		t.Fatalf("feedback was not recorded: %+v", last)
	}
}

func TestVisualReviewSkipsNonVisualTurn(t *testing.T) {
	a, _ := newVisualAgent(t, "Looks good")
	calls := []llm.ToolCall{{Name: "write", Arguments: `{"path":"main.go"}`}}
	results := []toolResult{{content: "wrote main.go"}}
	stop, err := a.visualReview(context.Background(), calls, results)
	if err != nil {
		t.Fatal(err)
	}
	if stop {
		t.Fatal("a normal source edit must not stop the run")
	}
}

type visualProvider struct {
	calls int
}

func (p *visualProvider) Name() string                                 { return "visual-fake" }
func (p *visualProvider) ListModels(context.Context) ([]string, error) { return nil, nil }
func (p *visualProvider) Chat(_ context.Context, _ llm.Request, _ func(string)) (*llm.Result, error) {
	p.calls++
	if p.calls == 1 {
		return &llm.Result{
			Content:   "built the page",
			ToolCalls: []llm.ToolCall{{ID: "w1", Name: "write", Arguments: `{"path":"index.html","content":"<h1>hi</h1>"}`}},
		}, nil
	}
	return &llm.Result{Content: "kept polishing"}, nil
}

func TestAgentStopsAtVisualHandoff(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	dir := t.TempDir()
	sess, err := session.Create(dir, "visual-fake", "model", "visual-loop")
	if err != nil {
		t.Fatal(err)
	}
	prov := &visualProvider{}
	a := &Agent{
		Provider: prov,
		Model:    "model",
		Tools:    tools.Registry(),
		Sess:     sess,
		MaxSteps: 6,
		Env: &tools.Env{
			Dir:    dir,
			Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} },
			AskUser: func(q tools.Question) (string, error) {
				if q.ID != "visual-review" {
					t.Fatalf("unexpected question %+v", q)
				}
				return "Looks good — ship it", nil
			},
		},
	}
	if err := a.Run(context.Background(), "build a page"); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 1 {
		t.Fatalf("provider was called %d times; approval should stop the run after the first visual turn", prov.calls)
	}
}

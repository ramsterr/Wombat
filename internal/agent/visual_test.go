package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/todo"
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

func rememberPreview(a *Agent, path string) {
	a.rememberVisualTurn(
		[]llm.ToolCall{{Name: "write", Arguments: `{"path":"` + path + `"}`}},
		[]toolResult{{content: "wrote " + path}},
	)
}

func TestVisualReviewStopsOnApproval(t *testing.T) {
	a, sess := newVisualAgent(t, "Looks good — ship it")
	a.Env.SetTodos([]todo.Item{{Content: "build the page", Status: todo.Completed}})
	rememberPreview(a, "index.html")
	stop, retry, err := a.finishVisualReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !stop || retry {
		t.Fatalf("finish = stop %v retry %v, want approval to stop", stop, retry)
	}
	last := sess.Messages[len(sess.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "approved") {
		t.Fatalf("approval was not recorded: %+v", last)
	}
}

func TestVisualReviewAsksForChanges(t *testing.T) {
	a, sess := newVisualAgent(t, "Needs changes — I'll describe", "make the heading larger")
	a.Env.SetTodos([]todo.Item{{Content: "build the page", Status: todo.Completed}})
	rememberPreview(a, "index.html")
	stop, retry, err := a.finishVisualReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stop || !retry {
		t.Fatalf("finish = stop %v retry %v, want feedback to continue", stop, retry)
	}
	last := sess.Messages[len(sess.Messages)-1]
	if !strings.Contains(last.Content, "make the heading larger") {
		t.Fatalf("feedback was not recorded: %+v", last)
	}
	if len(a.visualPending) == 0 {
		t.Fatal("the preview should stay pending so the next pass is reviewed again")
	}
	stop, retry, err = a.finishVisualReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !stop || retry {
		t.Fatalf("second finish = stop %v retry %v, want approval to stop", stop, retry)
	}
	if len(a.visualPending) != 0 {
		t.Fatal("approval should clear the pending preview")
	}
}

func TestVisualReviewWaitsForIncompleteTodos(t *testing.T) {
	a, sess := newVisualAgent(t, "Looks good — ship it")
	a.Env.SetTodos([]todo.Item{
		{Content: "build the page", Status: todo.InProgress},
		{Content: "check mobile", Status: todo.Pending},
	})
	rememberPreview(a, "index.html")
	stop, retry, err := a.finishVisualReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stop || !retry {
		t.Fatalf("finish = stop %v retry %v, want a nudge to finish the plan", stop, retry)
	}
	last := sess.Messages[len(sess.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "task plan still has pending") {
		t.Fatalf("nudge was not recorded: %+v", last)
	}

	a.Env.SetTodos([]todo.Item{
		{Content: "build the page", Status: todo.Completed},
		{Content: "check mobile", Status: todo.Completed},
	})
	stop, retry, err = a.finishVisualReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !stop || retry {
		t.Fatalf("finish = stop %v retry %v, want review after the plan is complete", stop, retry)
	}
	last = sess.Messages[len(sess.Messages)-1]
	if !strings.Contains(last.Content, "approved") {
		t.Fatalf("approval was not recorded: %+v", last)
	}
}

func TestVisualReviewWithoutPlanStillAsksAtTheEnd(t *testing.T) {
	a, _ := newVisualAgent(t, "Looks good — ship it")
	rememberPreview(a, "index.html")
	stop, retry, err := a.finishVisualReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !stop || retry {
		t.Fatalf("finish = stop %v retry %v, want the end-of-run review", stop, retry)
	}
}

func TestVisualReviewUsesPlanHook(t *testing.T) {
	a, _ := newVisualAgent(t, "Looks good — ship it")
	a.Plan = func() []todo.Item {
		return []todo.Item{{Content: "build the page", Status: todo.Completed}}
	}
	rememberPreview(a, "index.html")
	stop, retry, err := a.finishVisualReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !stop || retry {
		t.Fatalf("finish = stop %v retry %v, want a complete orchestrator plan to allow review", stop, retry)
	}
}

func TestVisualReviewIgnoresCancelledTodos(t *testing.T) {
	a, _ := newVisualAgent(t, "Looks good — ship it")
	a.Env.SetTodos([]todo.Item{{Content: "skip this", Status: todo.Cancelled}})
	rememberPreview(a, "index.html")
	stop, retry, err := a.finishVisualReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !stop || retry {
		t.Fatalf("finish = stop %v retry %v, want cancelled todos to allow review", stop, retry)
	}
}

func TestVisualReviewSkipsNonVisualTurn(t *testing.T) {
	a, _ := newVisualAgent(t, "Looks good")
	a.rememberVisualTurn(
		[]llm.ToolCall{{Name: "write", Arguments: `{"path":"main.go"}`}},
		[]toolResult{{content: "wrote main.go"}},
	)
	stop, retry, err := a.finishVisualReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stop || retry {
		t.Fatalf("finish = stop %v retry %v, want no review for a source edit", stop, retry)
	}
}

type todoVisualProvider struct {
	calls int
}

func (p *todoVisualProvider) Name() string                                 { return "todo-visual-fake" }
func (p *todoVisualProvider) ListModels(context.Context) ([]string, error) { return nil, nil }
func (p *todoVisualProvider) Chat(_ context.Context, _ llm.Request, _ func(string)) (*llm.Result, error) {
	p.calls++
	switch p.calls {
	case 1:
		return &llm.Result{
			Content: "building",
			ToolCalls: []llm.ToolCall{
				{ID: "t1", Name: "todowrite", Arguments: `{"todos":[{"content":"build the page","status":"in_progress"},{"content":"check mobile","status":"pending"}]}`},
				{ID: "w1", Name: "write", Arguments: `{"path":"index.html","content":"<h1>hi</h1>"}`},
			},
		}, nil
	case 2:
		return &llm.Result{
			Content: "finishing",
			ToolCalls: []llm.ToolCall{
				{ID: "t2", Name: "todowrite", Arguments: `{"todos":[{"content":"build the page","status":"completed"},{"content":"check mobile","status":"completed"}]}`},
			},
		}, nil
	default:
		return &llm.Result{Content: "extra work"}, nil
	}
}

func TestAgentWaitsForTodosBeforeVisualReview(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	dir := t.TempDir()
	sess, err := session.Create(dir, "todo-visual-fake", "model", "todo-visual")
	if err != nil {
		t.Fatal(err)
	}
	prov := &todoVisualProvider{}
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
	if prov.calls != 3 {
		t.Fatalf("provider was called %d times, want 3 (artifact, todo completion, final answer)", prov.calls)
	}
	if a.Env.HasIncompleteTodos() {
		t.Fatal("the todo list should be complete")
	}
	last := sess.Messages[len(sess.Messages)-1]
	if !strings.Contains(last.Content, "approved") {
		t.Fatalf("review was not approved: %+v", last)
	}
}

type multiPageProvider struct {
	calls int
}

func (p *multiPageProvider) Name() string                                 { return "multi-page-fake" }
func (p *multiPageProvider) ListModels(context.Context) ([]string, error) { return nil, nil }
func (p *multiPageProvider) Chat(_ context.Context, _ llm.Request, _ func(string)) (*llm.Result, error) {
	p.calls++
	pages := []string{"index.html", "features.html", "pricing.html"}
	if p.calls <= len(pages) {
		path := pages[p.calls-1]
		return &llm.Result{
			Content:   "building " + path,
			ToolCalls: []llm.ToolCall{{ID: "w" + path, Name: "write", Arguments: `{"path":"` + path + `","content":"<h1>` + path + `</h1>"}`}},
		}, nil
	}
	return &llm.Result{Content: "all pages built"}, nil
}

func TestAgentBuildsAllPagesBeforeReview(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	dir := t.TempDir()
	sess, err := session.Create(dir, "multi-page-fake", "model", "multi-page")
	if err != nil {
		t.Fatal(err)
	}
	prov := &multiPageProvider{}
	askedAt := 0
	a := &Agent{
		Provider: prov,
		Model:    "model",
		Tools:    tools.Registry(),
		Sess:     sess,
		MaxSteps: 8,
		Env: &tools.Env{
			Dir:    dir,
			Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} },
			AskUser: func(q tools.Question) (string, error) {
				askedAt = prov.calls
				if q.ID != "visual-review" {
					t.Fatalf("unexpected question %+v", q)
				}
				if !strings.Contains(q.Question, "3 previews") {
					t.Fatalf("review question = %q, want three previews", q.Question)
				}
				return "Looks good — ship it", nil
			},
		},
	}
	if err := a.Run(context.Background(), "build three pages"); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 4 {
		t.Fatalf("provider calls = %d, want 4 (three pages then final answer)", prov.calls)
	}
	if askedAt != 4 {
		t.Fatalf("review asked after provider call %d, want after the final call", askedAt)
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
	a.Env.SetTodos([]todo.Item{{Content: "build the page", Status: todo.Completed}})
	if err := a.Run(context.Background(), "build a page"); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 {
		t.Fatalf("provider was called %d times; the review should run after the model's final turn", prov.calls)
	}
}

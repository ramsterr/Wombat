package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

func boolPtr(v bool) *bool { return &v }

func TestSubmitReportTool(t *testing.T) {
	sink := &reportSink{}
	tool := &submitReportTool{sink: sink}
	out, err := tool.Run(context.Background(), json.RawMessage(`{"status":"completed","summary":"found it","findings":[{"claim":"x","path":"a.go"}]}`), &tools.Env{})
	if err != nil || out != "report recorded" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	r := sink.getReport()
	if r == nil || r.Status != "completed" || r.Summary != "found it" || len(r.Findings) != 1 {
		t.Fatalf("report = %+v", r)
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"status":"completed"}`), &tools.Env{}); err == nil {
		t.Fatal("empty summary should fail")
	}

	reviewSink := &reportSink{}
	review := &submitReviewTool{sink: reviewSink}
	if _, err := review.Run(context.Background(), json.RawMessage(`{"verdict":"approve","summary":"ok"}`), &tools.Env{}); err != nil {
		t.Fatal(err)
	}
	if got := reviewSink.getReview(); got == nil || got.Verdict != "approved" {
		t.Fatalf("review = %+v", got)
	}
}

func TestParseReportFindsEmbeddedJSON(t *testing.T) {
	cases := []struct {
		text   string
		status string
	}{
		{`prose before {"status":"completed","summary":"found the parser"} prose after`, "completed"},
		{"```json\n{\"status\":\"blocked\",\"summary\":\"need a token\"}\n```", "blocked"},
		{`first {not json} then {"status":"partial","summary":"use {braces} safely"}`, "partial"},
	}
	for _, tc := range cases {
		r, ok := parseReport(tc.text)
		if !ok {
			t.Fatalf("did not parse: %s", tc.text)
		}
		if r.Status != tc.status {
			t.Fatalf("status = %q, want %q", r.Status, tc.status)
		}
	}
	if _, ok := parseReport("just prose, no report"); ok {
		t.Fatal("prose should not parse as a report")
	}
}

func TestParseReview(t *testing.T) {
	r, ok := parseReview(`{"verdict":"REQUEST_CHANGES","summary":"missing test","findings":[{"severity":"major","issue":"no test"}]}`)
	if !ok || r.Verdict != "changes_requested" || len(r.Findings) != 1 {
		t.Fatalf("review = %+v ok=%v", r, ok)
	}
	if got := normalizeVerdict("lgtm"); got != "approved" {
		t.Fatalf("lgtm = %q", got)
	}
}

type reviewProvider struct {
	mu     sync.Mutex
	counts map[string]int
	fn     func(kind string, call int) (*llm.Result, error)
}

func (p *reviewProvider) Name() string { return "review-mock" }
func (p *reviewProvider) ListModels(context.Context) ([]string, error) {
	return []string{"review-mock"}, nil
}
func (p *reviewProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	kind := "lead"
	switch {
	case strings.Contains(req.System, "## Reviewer"):
		kind = "reviewer"
	case strings.Contains(req.System, "## Writer worker"):
		kind = "writer"
	case strings.Contains(req.System, "## Delegated worker"):
		kind = "scout"
	}
	p.mu.Lock()
	if p.counts == nil {
		p.counts = map[string]int{}
	}
	p.counts[kind]++
	call := p.counts[kind]
	p.mu.Unlock()
	return p.fn(kind, call)
}

func (p *reviewProvider) calls(kind string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[kind]
}

func toolCall(id, name string, args any) *llm.Result {
	raw, _ := json.Marshal(args)
	return &llm.Result{ToolCalls: []llm.ToolCall{{ID: id, Name: name, Arguments: string(raw)}}, Usage: llm.Usage{In: 2, Out: 1}}
}

func reviewRepo(t *testing.T, provider llm.Provider, cfg Config) (*Orchestrator, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "a.txt")
	runGit(t, repo, "commit", "-m", "initial")
	sess, err := session.CreateAt(home, repo, provider.Name(), "review-mock", "test")
	if err != nil {
		t.Fatal(err)
	}
	ag := &agent.Agent{
		Provider: provider,
		Model:    "review-mock",
		Tools:    tools.Registry(),
		Env:      &tools.Env{Dir: repo, Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} }},
		Sess:     sess,
		MaxSteps: 8,
	}
	cfg.WorktreeRoot = t.TempDir()
	cfg.EnableWriters = true
	o, err := Attach(ag, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return o, repo
}

func writerSteps(reviewVerdict string) func(kind string, call int) (*llm.Result, error) {
	return func(kind string, call int) (*llm.Result, error) {
		switch kind {
		case "writer":
			switch call {
			case 1:
				return toolCall("w1", "write", map[string]any{"path": "b.txt", "content": "two"}), nil
			case 2:
				return toolCall("w2", "submit_report", map[string]any{
					"status": "completed", "summary": "created b",
					"changes": []map[string]string{{"path": "b.txt", "summary": "created"}},
				}), nil
			default:
				return &llm.Result{Content: "done"}, nil
			}
		case "reviewer":
			if call == 1 {
				return toolCall("r1", "submit_review", map[string]any{
					"verdict": reviewVerdict, "summary": "review summary",
					"findings": []map[string]any{{"severity": "minor", "path": "b.txt", "issue": "style"}},
				}), nil
			}
			return &llm.Result{Content: "done"}, nil
		default:
			return nil, fmt.Errorf("unexpected %s call %d", kind, call)
		}
	}
}

func TestWriterReviewApprovesAndMerges(t *testing.T) {
	prov := &reviewProvider{fn: writerSteps("approved")}
	o, repo := reviewRepo(t, prov, Config{ReviewWriters: boolPtr(true)})
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "s", Title: "Create b", Kind: StepWrite, Files: []string{"b.txt"}, Status: StepInProgress,
	}}); err != nil {
		t.Fatal(err)
	}
	out, err := o.delegateWrite(context.Background(), delegateWriteArgs{Tasks: []writerTask{{
		StepID: "s", Title: "Create b", Objective: "create b", Files: []string{"b.txt"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"merged": true`) || !strings.Contains(out, `"verdict": "approved"`) {
		t.Fatalf("out = %s", out)
	}
	data, err := os.ReadFile(filepath.Join(repo, "b.txt"))
	if err != nil || string(data) != "two" {
		t.Fatalf("b.txt = %q err=%v", data, err)
	}
	if step := o.ledger.Find("s"); step == nil || step.Status != StepCompleted || step.Review == nil || step.Review.Verdict != "approved" {
		t.Fatalf("step = %+v", step)
	}
}

func TestWriterReviewRequestsChanges(t *testing.T) {
	prov := &reviewProvider{fn: writerSteps("changes_requested")}
	o, repo := reviewRepo(t, prov, Config{ReviewWriters: boolPtr(true)})
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "s", Title: "Create b", Kind: StepWrite, Files: []string{"b.txt"}, Status: StepInProgress,
	}}); err != nil {
		t.Fatal(err)
	}
	out, err := o.delegateWrite(context.Background(), delegateWriteArgs{Tasks: []writerTask{{
		StepID: "s", Title: "Create b", Objective: "create b", Files: []string{"b.txt"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"merged": false`) || !strings.Contains(out, "changes_requested") {
		t.Fatalf("out = %s", out)
	}
	if _, err := os.Stat(filepath.Join(repo, "b.txt")); err == nil {
		t.Fatal("rejected patch was merged")
	}
	if step := o.ledger.Find("s"); step == nil || step.Status == StepCompleted || step.Review == nil {
		t.Fatalf("step = %+v", step)
	}
}

func TestRunPlanSchedulesDependencyWaves(t *testing.T) {
	prov := &reviewProvider{fn: func(kind string, call int) (*llm.Result, error) {
		switch kind {
		case "scout":
			if call == 1 {
				return toolCall("s1", "submit_report", map[string]any{
					"status": "completed", "summary": "surveyed the parser",
				}), nil
			}
			return &llm.Result{Content: "done"}, nil
		default:
			return writerSteps("approved")(kind, call)
		}
	}}
	o, repo := reviewRepo(t, prov, Config{ReviewWriters: boolPtr(true)})
	if _, err := o.updatePlan("ship", nil, []Step{
		{ID: "research", Title: "Research", Kind: StepScout, Status: StepPending},
		{ID: "implement", Title: "Implement", Kind: StepWrite, Files: []string{"b.txt"}, Status: StepPending, DependsOn: []string{"research"}},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{})
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Waves []planWave `json:"waves"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Waves) != 2 {
		t.Fatalf("waves = %d, want 2:\n%s", len(parsed.Waves), out)
	}
	if len(parsed.Waves[0].Tasks) != 1 || parsed.Waves[0].Tasks[0].StepID != "research" {
		t.Fatalf("wave 1 = %+v", parsed.Waves[0])
	}
	if len(parsed.Waves[1].Tasks) != 1 || parsed.Waves[1].Tasks[0].StepID != "implement" || !parsed.Waves[1].Tasks[0].Merged {
		t.Fatalf("wave 2 = %+v", parsed.Waves[1])
	}
	if step := o.ledger.Find("implement"); step == nil || step.Status != StepCompleted {
		t.Fatalf("implement = %+v", step)
	}
	if data, err := os.ReadFile(filepath.Join(repo, "b.txt")); err != nil || string(data) != "two" {
		t.Fatalf("b.txt = %q err=%v", data, err)
	}
	if prov.calls("writer") == 0 || prov.calls("reviewer") == 0 {
		t.Fatalf("writer/reviewer were not used: writer=%d reviewer=%d", prov.calls("writer"), prov.calls("reviewer"))
	}
}

func TestRunPlanFailureBlocksDependents(t *testing.T) {
	prov := &reviewProvider{fn: func(kind string, call int) (*llm.Result, error) {
		if kind != "scout" {
			return nil, fmt.Errorf("dependent worker should not run")
		}
		if call == 1 {
			return toolCall("s1", "submit_report", map[string]any{"status": "failed", "summary": "could not inspect"}), nil
		}
		return &llm.Result{Content: "done"}, nil
	}}
	o, _ := reviewRepo(t, prov, Config{})
	if _, err := o.updatePlan("ship", nil, []Step{
		{ID: "research", Title: "Research", Kind: StepScout, Status: StepPending},
		{ID: "implement", Title: "Implement", Kind: StepWrite, Files: []string{"b.txt"}, Status: StepPending, DependsOn: []string{"research"}},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls("writer") != 0 || prov.calls("reviewer") != 0 {
		t.Fatalf("dependent writer ran: writer=%d reviewer=%d", prov.calls("writer"), prov.calls("reviewer"))
	}
	if step := o.ledger.Find("implement"); step == nil || step.Status != StepBlocked {
		t.Fatalf("implement = %+v, out=%s", step, out)
	}
}

func TestRunPlanRejectsWriteWithoutFiles(t *testing.T) {
	prov := &reviewProvider{fn: func(kind string, call int) (*llm.Result, error) {
		return nil, fmt.Errorf("no worker should run")
	}}
	o, _ := reviewRepo(t, prov, Config{})
	if _, err := o.updatePlan("ship", nil, []Step{
		{ID: "implement", Title: "Implement", Kind: StepWrite, Status: StepPending},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no owned files") {
		t.Fatalf("out = %s", out)
	}
	if step := o.ledger.Find("implement"); step == nil || step.Status != StepFailed {
		t.Fatalf("implement = %+v", step)
	}
	if prov.calls("writer") != 0 {
		t.Fatal("writer ran without owned files")
	}
}

func TestRunPlanUnknownStep(t *testing.T) {
	o := &Orchestrator{ledger: &Ledger{}, cfg: Config{}}
	if _, err := o.runPlan(context.Background(), runPlanArgs{Steps: []string{"nope"}}); err == nil || !strings.Contains(err.Error(), "unknown step") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdatePlanPreservesWriteMetadata(t *testing.T) {
	o := &Orchestrator{ledger: &Ledger{}}
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "s", Title: "Implement", Kind: StepWrite, Files: []string{"b.txt"}, Deliverable: "b exists", Status: StepInProgress,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "s", Title: "Implement", Status: StepCompleted,
	}}); err != nil {
		t.Fatal(err)
	}
	step := o.ledger.Find("s")
	if step == nil || step.Kind != StepWrite || len(step.Files) != 1 || step.Files[0] != "b.txt" || step.Deliverable != "b exists" {
		t.Fatalf("step metadata lost: %+v", step)
	}
}

func TestRunPlanNoRunnableSteps(t *testing.T) {
	o := &Orchestrator{ledger: &Ledger{Steps: []Step{{ID: "s", Title: "done", Status: StepCompleted}}}, cfg: Config{}}
	if _, err := o.runPlan(context.Background(), runPlanArgs{}); err == nil || !strings.Contains(err.Error(), "no runnable steps") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunPlanRunsIndependentScoutsInOneWave(t *testing.T) {
	prov := &reviewProvider{fn: func(kind string, call int) (*llm.Result, error) {
		if kind != "scout" {
			return nil, fmt.Errorf("unexpected %s", kind)
		}
		return &llm.Result{Content: `{"status":"completed","summary":"done"}`}, nil
	}}
	o, _ := reviewRepo(t, prov, Config{})
	if _, err := o.updatePlan("goal", nil, []Step{
		{ID: "a", Title: "A", Kind: StepScout, Status: StepPending},
		{ID: "b", Title: "B", Kind: StepScout, Status: StepPending},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{MaxParallel: 2})
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Waves []planWave `json:"waves"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Waves) != 1 || len(parsed.Waves[0].Tasks) != 2 {
		t.Fatalf("waves = %+v", parsed.Waves)
	}
	if step := o.ledger.Find("a"); step == nil || step.Status != StepCompleted {
		t.Fatalf("a = %+v", step)
	}
	if step := o.ledger.Find("b"); step == nil || step.Status != StepCompleted {
		t.Fatalf("b = %+v", step)
	}
}

func TestRunPlanRejectsOverlappingWriterOwnership(t *testing.T) {
	prov := &reviewProvider{fn: func(kind string, call int) (*llm.Result, error) {
		return nil, fmt.Errorf("no writer should run")
	}}
	o, _ := reviewRepo(t, prov, Config{})
	if _, err := o.updatePlan("goal", nil, []Step{
		{ID: "a", Title: "A", Kind: StepWrite, Files: []string{"same.txt"}, Status: StepPending},
		{ID: "b", Title: "B", Kind: StepWrite, Files: []string{"same.txt"}, Status: StepPending},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{MaxParallel: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "overlap") {
		t.Fatalf("out = %s", out)
	}
	if prov.calls("writer") != 0 {
		t.Fatal("overlapping writers ran")
	}
	for _, id := range []string{"a", "b"} {
		if step := o.ledger.Find(id); step == nil || step.Status != StepFailed {
			t.Fatalf("%s = %+v", id, step)
		}
	}
}

type waveWriterProvider struct {
	mu          sync.Mutex
	counts      map[string]int
	writerCalls map[string]int
}

func (p *waveWriterProvider) Name() string { return "wave-mock" }
func (p *waveWriterProvider) calls(kind string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[kind]
}
func (p *waveWriterProvider) ListModels(context.Context) ([]string, error) {
	return []string{"wave-mock"}, nil
}
func (p *waveWriterProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	kind := "lead"
	switch {
	case strings.Contains(req.System, "## Reviewer"):
		kind = "reviewer"
	case strings.Contains(req.System, "## Writer worker"):
		kind = "writer"
	}
	p.mu.Lock()
	if p.counts == nil {
		p.counts = map[string]int{}
	}
	if p.writerCalls == nil {
		p.writerCalls = map[string]int{}
	}
	p.counts[kind]++
	call := p.counts[kind]
	title := "A"
	if strings.Contains(req.System, "Write B") {
		title = "B"
	}
	if kind == "writer" {
		p.writerCalls[title]++
		call = p.writerCalls[title]
	}
	p.mu.Unlock()
	if kind == "reviewer" {
		if call == 1 {
			return toolCall("rev", "submit_review", map[string]any{"verdict": "approved", "summary": "ok"}), nil
		}
		return &llm.Result{Content: "done"}, nil
	}
	if kind != "writer" {
		return nil, fmt.Errorf("unexpected %s", kind)
	}
	path := "a.txt"
	if strings.Contains(req.System, "- b.txt") {
		path = "b.txt"
	}
	switch call {
	case 1:
		return toolCall("w", "write", map[string]any{"path": path, "content": strings.TrimSuffix(path, ".txt")}), nil
	case 2:
		return toolCall("r", "submit_report", map[string]any{"status": "completed", "summary": "wrote " + path, "changes": []map[string]string{{"path": path}}}), nil
	default:
		return &llm.Result{Content: "done"}, nil
	}
}

func TestRunPlanWriterWavesBuildOnMergedState(t *testing.T) {
	prov := &waveWriterProvider{}
	o, repo := reviewRepo(t, prov, Config{ReviewWriters: boolPtr(true)})
	if _, err := o.updatePlan("goal", nil, []Step{
		{ID: "a", Title: "Write A", Kind: StepWrite, Files: []string{"a.txt"}, Status: StepPending},
		{ID: "b", Title: "Write B", Kind: StepWrite, Files: []string{"b.txt"}, Status: StepPending, DependsOn: []string{"a"}},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if data, err := os.ReadFile(filepath.Join(repo, name)); err != nil || string(data) != strings.TrimSuffix(name, ".txt") {
			t.Fatalf("%s = %q err=%v\nout=%s", name, data, err, out)
		}
	}
	for _, id := range []string{"a", "b"} {
		if step := o.ledger.Find(id); step == nil || step.Status != StepCompleted {
			t.Fatalf("%s = %+v\nout=%s", id, step, out)
		}
	}
}

func TestReviewDefaultsAndModel(t *testing.T) {
	if !(Config{}).reviewsWriters() {
		t.Fatal("review should default on")
	}
	if (Config{ReviewWriters: boolPtr(false)}).reviewsWriters() {
		t.Fatal("review should be off when disabled")
	}
	if got := (Config{ReviewModel: "review-x", ScoutModel: "scout-x"}).reviewerModel("lead"); got != "review-x" {
		t.Fatalf("review model = %q", got)
	}
	if got := (Config{ScoutModel: "scout-x"}).reviewerModel("lead"); got != "scout-x" {
		t.Fatalf("scout fallback = %q", got)
	}
}

func TestDelegateWriteMergesTwoWriters(t *testing.T) {
	prov := &waveWriterProvider{}
	o, repo := reviewRepo(t, prov, Config{ReviewWriters: boolPtr(true)})
	if _, err := o.updatePlan("goal", nil, []Step{
		{ID: "a", Title: "Write A", Kind: StepWrite, Files: []string{"a.txt"}, Status: StepInProgress},
		{ID: "b", Title: "Write B", Kind: StepWrite, Files: []string{"b.txt"}, Status: StepInProgress},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := o.delegateWrite(context.Background(), delegateWriteArgs{Tasks: []writerTask{
		{StepID: "a", Title: "Write A", Objective: "write a", Files: []string{"a.txt"}},
		{StepID: "b", Title: "Write B", Objective: "write b", Files: []string{"b.txt"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, `"merged": true`) != 2 {
		t.Fatalf("expected two merges:\n%s", out)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if data, err := os.ReadFile(filepath.Join(repo, name)); err != nil || string(data) != strings.TrimSuffix(name, ".txt") {
			t.Fatalf("%s = %q err=%v", name, data, err)
		}
	}
}

func TestRunPlanScoutsDoNotUnstageUserChanges(t *testing.T) {
	prov := &reviewProvider{fn: func(kind string, call int) (*llm.Result, error) {
		return &llm.Result{Content: `{"status":"completed","summary":"ok"}`}, nil
	}}
	o, repo := reviewRepo(t, prov, Config{})
	if err := os.WriteFile(filepath.Join(repo, "staged.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "staged.txt")
	if _, err := o.updatePlan("goal", nil, []Step{{ID: "s", Title: "Scout", Kind: StepScout, Status: StepPending}}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.runPlan(context.Background(), runPlanArgs{}); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", repo, "diff", "--cached", "--name-only").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "staged.txt") {
		t.Fatalf("staged change was lost: %q", out)
	}
}

func TestRunPlanBlocksRejectedWriter(t *testing.T) {
	prov := &reviewProvider{fn: writerSteps("changes_requested")}
	o, repo := reviewRepo(t, prov, Config{ReviewWriters: boolPtr(true)})
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "w", Title: "Create b", Kind: StepWrite, Files: []string{"b.txt"}, Status: StepPending,
	}}); err != nil {
		t.Fatal(err)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "b.txt")); err == nil {
		t.Fatal("rejected writer patch was merged")
	}
	step := o.ledger.Find("w")
	if step == nil || step.Status != StepFailed || step.Review == nil || step.Review.Verdict != "changes_requested" {
		t.Fatalf("step = %+v\nout=%s", step, out)
	}
}

func TestRunPlanRunsScoutChecks(t *testing.T) {
	prov := &reviewProvider{fn: func(kind string, call int) (*llm.Result, error) {
		return &llm.Result{Content: `{"status":"completed","summary":"ok"}`}, nil
	}}
	o, _ := reviewRepo(t, prov, Config{})
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "s", Title: "Scout", Kind: StepScout, Status: StepPending,
		Checks: []Check{{ID: "c", Command: "true", Status: CheckPending}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.runPlan(context.Background(), runPlanArgs{}); err != nil {
		t.Fatal(err)
	}
	step := o.ledger.Find("s")
	if step == nil || step.Status != StepCompleted || len(step.Checks) != 1 || step.Checks[0].Status != CheckPassed {
		t.Fatalf("scout checks were not run: %+v", step)
	}
}

func TestRunPlanFailsScoutWithBadCheck(t *testing.T) {
	prov := &reviewProvider{fn: func(kind string, call int) (*llm.Result, error) {
		return &llm.Result{Content: `{"status":"completed","summary":"ok"}`}, nil
	}}
	o, _ := reviewRepo(t, prov, Config{})
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "s", Title: "Scout", Kind: StepScout, Status: StepPending,
		Checks: []Check{{ID: "c", Command: "false", Status: CheckPending}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.runPlan(context.Background(), runPlanArgs{}); err != nil {
		t.Fatal(err)
	}
	step := o.ledger.Find("s")
	if step == nil || step.Status != StepFailed || len(step.Checks) != 1 || step.Checks[0].Status != CheckFailed {
		t.Fatalf("bad scout check did not fail the step: %+v", step)
	}
}

func TestRunPlanRequiresChecksForWriters(t *testing.T) {
	prov := &reviewProvider{fn: func(kind string, call int) (*llm.Result, error) {
		return nil, fmt.Errorf("writer should not run")
	}}
	o, _ := reviewRepo(t, prov, Config{RequireVerification: true})
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "w", Title: "Write", Kind: StepWrite, Files: []string{"b.txt"}, Status: StepPending,
	}}); err != nil {
		t.Fatal(err)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no checks") {
		t.Fatalf("out = %s", out)
	}
	if prov.calls("writer") != 0 {
		t.Fatal("writer ran without checks")
	}
}

func TestRunPlanWriteStepWithoutWritersFails(t *testing.T) {
	prov := &reviewProvider{fn: func(kind string, call int) (*llm.Result, error) {
		return nil, fmt.Errorf("no worker should run")
	}}
	o, _ := reviewRepo(t, prov, Config{})
	o.worktrees = nil
	o.writerWarn = "writers are not enabled"
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "w", Title: "Write", Kind: StepWrite, Files: []string{"b.txt"}, Status: StepPending,
	}}); err != nil {
		t.Fatal(err)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not enabled") {
		t.Fatalf("out = %s", out)
	}
	if step := o.ledger.Find("w"); step == nil || step.Status != StepFailed {
		t.Fatalf("step = %+v", step)
	}
}

func TestDelegateWriteRecordsFailedCheck(t *testing.T) {
	prov := &waveWriterProvider{}
	o, _ := reviewRepo(t, prov, Config{ReviewWriters: boolPtr(false)})
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "w", Title: "Write A", Kind: StepWrite, Files: []string{"a.txt"}, Status: StepInProgress,
		Checks: []Check{{ID: "missing", Command: "test -f never", Status: CheckPending}},
	}}); err != nil {
		t.Fatal(err)
	}
	out, err := o.delegateWrite(context.Background(), delegateWriteArgs{Tasks: []writerTask{{
		StepID: "w", Title: "Write A", Objective: "write a", Files: []string{"a.txt"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status": "failed"`) {
		t.Fatalf("out = %s", out)
	}
	step := o.ledger.Find("w")
	if step == nil || len(step.Checks) != 1 || step.Checks[0].Status != CheckFailed {
		t.Fatalf("failed check not recorded: %+v", step)
	}
}

func TestUpdatePlanInfersWriteFromFiles(t *testing.T) {
	o := &Orchestrator{ledger: &Ledger{}}
	if _, err := o.updatePlan("goal", nil, []Step{{ID: "w", Title: "Write", Files: []string{"b.txt"}, Status: StepPending}}); err != nil {
		t.Fatal(err)
	}
	if step := o.ledger.Find("w"); step == nil || step.Kind != StepWrite {
		t.Fatalf("kind = %+v", step)
	}
}

func TestParseReportNestedObject(t *testing.T) {
	r, ok := parseReport(`prefix {not valid {"status":"completed","summary":"nested report"} } suffix`)
	if !ok || r.Status != "completed" || r.Summary != "nested report" {
		t.Fatalf("r = %+v ok=%v", r, ok)
	}
}

func TestDelegateWriteSecondBatchBuildsOnFirst(t *testing.T) {
	prov := &waveWriterProvider{}
	o, repo := reviewRepo(t, prov, Config{ReviewWriters: boolPtr(true)})
	if _, err := o.updatePlan("goal", nil, []Step{
		{ID: "a", Title: "Write A", Kind: StepWrite, Files: []string{"a.txt"}, Status: StepInProgress},
		{ID: "b", Title: "Write B", Kind: StepWrite, Files: []string{"b.txt"}, Status: StepInProgress},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.delegateWrite(context.Background(), delegateWriteArgs{Tasks: []writerTask{{
		StepID: "a", Title: "Write A", Objective: "a", Files: []string{"a.txt"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.delegateWrite(context.Background(), delegateWriteArgs{Tasks: []writerTask{{
		StepID: "b", Title: "Write B", Objective: "b", Files: []string{"b.txt"},
	}}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if data, err := os.ReadFile(filepath.Join(repo, name)); err != nil || string(data) != strings.TrimSuffix(name, ".txt") {
			t.Fatalf("%s = %q err=%v", name, data, err)
		}
	}
}

func TestWriterReviewCanBeDisabled(t *testing.T) {
	prov := &reviewProvider{fn: writerSteps("approved")}
	o, _ := reviewRepo(t, prov, Config{ReviewWriters: boolPtr(false)})
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "s", Title: "Create b", Kind: StepWrite, Files: []string{"b.txt"}, Status: StepInProgress,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.delegateWrite(context.Background(), delegateWriteArgs{Tasks: []writerTask{{
		StepID: "s", Title: "Create b", Objective: "create b", Files: []string{"b.txt"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if prov.calls("reviewer") != 0 {
		t.Fatalf("reviewer ran even though review was disabled: %d", prov.calls("reviewer"))
	}
}

func TestWriterRejectsCheckThatNamesLeadWorkspace(t *testing.T) {
	prov := &waveWriterProvider{}
	o, repo := reviewRepo(t, prov, Config{ReviewWriters: boolPtr(false)})
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "w", Title: "Write A", Kind: StepWrite, Files: []string{"a.txt"}, Status: StepInProgress,
		Checks: []Check{{ID: "c", Command: "test -f " + filepath.Join(repo, "a.txt"), Status: CheckPending}},
	}}); err != nil {
		t.Fatal(err)
	}
	out, err := o.delegateWrite(context.Background(), delegateWriteArgs{Tasks: []writerTask{{
		StepID: "w", Title: "Write A", Objective: "write a", Files: []string{"a.txt"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "lead workspace path") {
		t.Fatalf("out = %s", out)
	}
	if prov.calls("writer") != 0 {
		t.Fatalf("writer ran despite an invalid check: %d calls", prov.calls("writer"))
	}
	data, err := os.ReadFile(filepath.Join(repo, "a.txt"))
	if err != nil || string(data) != "one\n" {
		t.Fatalf("lead workspace was modified: %q err=%v", data, err)
	}
}

func TestWriterCannotWriteIntoLeadWorkspaceByShell(t *testing.T) {
	var repo string
	prov := &reviewProvider{fn: func(kind string, call int) (*llm.Result, error) {
		if kind != "writer" {
			return nil, fmt.Errorf("unexpected %s", kind)
		}
		switch call {
		case 1:
			return toolCall("b1", "bash", map[string]string{"command": "printf escaped > " + filepath.Join(repo, "escaped.txt")}), nil
		case 2:
			return toolCall("r1", "submit_report", map[string]any{"status": "failed", "summary": "could not write outside the worktree"}), nil
		default:
			return &llm.Result{Content: "done"}, nil
		}
	}}
	o, r := reviewRepo(t, prov, Config{ReviewWriters: boolPtr(false)})
	repo = r
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "w", Title: "Write", Kind: StepWrite, Files: []string{"a.txt"}, Status: StepInProgress,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.delegateWrite(context.Background(), delegateWriteArgs{Tasks: []writerTask{{
		StepID: "w", Title: "Write", Objective: "write", Files: []string{"a.txt"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "escaped.txt")); err == nil {
		t.Fatal("writer escaped the worktree")
	}
}

func TestUpdatePlanPreservesReview(t *testing.T) {
	o := &Orchestrator{ledger: &Ledger{}}
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "w", Title: "Write", Kind: StepWrite, Files: []string{"b.txt"}, Status: StepInProgress,
	}}); err != nil {
		t.Fatal(err)
	}
	o.ledger.Steps[0].Review = &Review{Verdict: "approved", Summary: "looks good"}
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "w", Title: "Write", Kind: StepWrite, Files: []string{"b.txt"}, Status: StepCompleted,
	}}); err != nil {
		t.Fatal(err)
	}
	if step := o.ledger.Find("w"); step == nil || step.Review == nil || step.Review.Verdict != "approved" {
		t.Fatalf("review was lost: %+v", step)
	}
}

func TestRunPlanMergesNewFileWithRelativeCheck(t *testing.T) {
	prov := &reviewProvider{fn: writerSteps("approved")}
	o, repo := reviewRepo(t, prov, Config{ReviewWriters: boolPtr(true), RequireVerification: true})
	if _, err := o.updatePlan("goal", nil, []Step{{
		ID: "w", Title: "Create b", Kind: StepWrite, Files: []string{"b.txt"}, Status: StepPending,
		Checks: []Check{{ID: "exists", Command: "test -f b.txt", Status: CheckPending}},
	}}); err != nil {
		t.Fatal(err)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"merged": true`) {
		t.Fatalf("out = %s", out)
	}
	data, err := os.ReadFile(filepath.Join(repo, "b.txt"))
	if err != nil || string(data) != "two" {
		t.Fatalf("b.txt = %q err=%v", data, err)
	}
	step := o.ledger.Find("w")
	if step == nil || step.Status != StepCompleted || step.Review == nil || step.Review.Verdict != "approved" {
		t.Fatalf("step = %+v", step)
	}
}

func TestFullAccessWorkerCanWriteAndMerge(t *testing.T) {
	prov := &reviewProvider{fn: func(kind string, call int) (*llm.Result, error) {
		if kind != "scout" {
			return nil, fmt.Errorf("unexpected %s", kind)
		}
		switch call {
		case 1:
			return toolCall("w", "write", map[string]any{"path": "worker-note.txt", "content": "written by a delegate"}), nil
		case 2:
			return toolCall("r", "submit_report", map[string]any{
				"status": "completed", "summary": "wrote the note",
				"changes": []map[string]string{{"path": "worker-note.txt"}},
			}), nil
		default:
			return &llm.Result{Content: "done"}, nil
		}
	}}
	o, repo := reviewRepo(t, prov, Config{FullWorkers: true, ReviewWriters: boolPtr(false)})
	if _, err := o.updatePlan("goal", nil, []Step{{ID: "s", Title: "Research and write", Kind: StepScout, Status: StepInProgress}}); err != nil {
		t.Fatal(err)
	}
	out, err := o.delegate(context.Background(), delegateArgs{
		StepID: "s", Title: "Research and write", Objective: "write a note",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status": "completed"`) {
		t.Fatalf("out = %s", out)
	}
	data, err := os.ReadFile(filepath.Join(repo, "worker-note.txt"))
	if err != nil || string(data) != "written by a delegate" {
		t.Fatalf("worker-note.txt = %q err=%v\nout=%s", data, err, out)
	}
	if step := o.ledger.Find("s"); step == nil || step.Status != StepCompleted {
		t.Fatalf("step = %+v", step)
	}
}

func TestFullAccessWorkerWithoutChangesCompletes(t *testing.T) {
	prov := &reviewProvider{fn: func(kind string, call int) (*llm.Result, error) {
		return &llm.Result{Content: `{"status":"completed","summary":"read only this time"}`}, nil
	}}
	o, _ := reviewRepo(t, prov, Config{FullWorkers: true, ReviewWriters: boolPtr(false)})
	if _, err := o.updatePlan("goal", nil, []Step{{ID: "s", Title: "Research", Kind: StepScout, Status: StepInProgress}}); err != nil {
		t.Fatal(err)
	}
	out, err := o.delegate(context.Background(), delegateArgs{StepID: "s", Title: "Research", Objective: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status": "completed"`) {
		t.Fatalf("out = %s", out)
	}
	if step := o.ledger.Find("s"); step == nil || step.Status != StepCompleted {
		t.Fatalf("step = %+v", step)
	}
}

func TestFullAccessWorkerCanUseNetworkAndWrite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("downloaded asset"))
	}))
	defer srv.Close()
	prov := &reviewProvider{fn: func(kind string, call int) (*llm.Result, error) {
		if kind != "scout" {
			return nil, fmt.Errorf("unexpected %s", kind)
		}
		switch call {
		case 1:
			return toolCall("c", "bash", map[string]string{"command": "curl -s -o asset.txt " + srv.URL}), nil
		case 2:
			return toolCall("r", "submit_report", map[string]any{"status": "completed", "summary": "downloaded asset"}), nil
		default:
			return &llm.Result{Content: "done"}, nil
		}
	}}
	o, repo := reviewRepo(t, prov, Config{FullWorkers: true, ReviewWriters: boolPtr(false)})
	if _, err := o.updatePlan("goal", nil, []Step{{ID: "s", Title: "Fetch", Kind: StepScout, Status: StepInProgress}}); err != nil {
		t.Fatal(err)
	}
	out, err := o.delegate(context.Background(), delegateArgs{StepID: "s", Title: "Fetch", Objective: "download the asset"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "asset.txt"))
	if err != nil || string(data) != "downloaded asset" {
		t.Fatalf("asset.txt = %q err=%v\nout=%s", data, err, out)
	}
}

func TestRunPlanFullAccessScoutCanWrite(t *testing.T) {
	prov := &reviewProvider{fn: func(kind string, call int) (*llm.Result, error) {
		if kind != "scout" {
			return nil, fmt.Errorf("unexpected %s", kind)
		}
		switch call {
		case 1:
			return toolCall("w", "write", map[string]any{"path": "plan-note.txt", "content": "from a plan scout"}), nil
		case 2:
			return toolCall("r", "submit_report", map[string]any{"status": "completed", "summary": "wrote plan note"}), nil
		default:
			return &llm.Result{Content: "done"}, nil
		}
	}}
	o, repo := reviewRepo(t, prov, Config{FullWorkers: true, ReviewWriters: boolPtr(false)})
	if _, err := o.updatePlan("goal", nil, []Step{{ID: "s", Title: "Scout write", Kind: StepScout, Status: StepPending}}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.runPlan(context.Background(), runPlanArgs{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "plan-note.txt"))
	if err != nil || string(data) != "from a plan scout" {
		t.Fatalf("plan-note.txt = %q err=%v", data, err)
	}
	if step := o.ledger.Find("s"); step == nil || step.Status != StepCompleted {
		t.Fatalf("step = %+v", step)
	}
}

type fullParallelProvider struct {
	mu    sync.Mutex
	calls map[string]int
}

func (p *fullParallelProvider) Name() string { return "full-parallel" }
func (p *fullParallelProvider) ListModels(context.Context) ([]string, error) {
	return []string{"full-parallel"}, nil
}
func (p *fullParallelProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	kind := "lead"
	if strings.Contains(req.System, "## Delegated worker") {
		kind = "scout"
	}
	p.mu.Lock()
	if p.calls == nil {
		p.calls = map[string]int{}
	}
	key := kind
	if kind == "scout" {
		if strings.Contains(req.System, "[a]") {
			key = "a"
		} else if strings.Contains(req.System, "[b]") {
			key = "b"
		}
	}
	p.calls[key]++
	call := p.calls[key]
	p.mu.Unlock()
	if kind != "scout" {
		return nil, fmt.Errorf("unexpected %s", kind)
	}
	path := "a.txt"
	if key == "b" {
		path = "b.txt"
	}
	switch call {
	case 1:
		return toolCall("w", "write", map[string]any{"path": path, "content": key}), nil
	case 2:
		return toolCall("r", "submit_report", map[string]any{"status": "completed", "summary": "wrote " + path}), nil
	default:
		return &llm.Result{Content: "done"}, nil
	}
}

func TestFullAccessWorkersRunInParallel(t *testing.T) {
	prov := &fullParallelProvider{}
	o, repo := reviewRepo(t, prov, Config{FullWorkers: true, ReviewWriters: boolPtr(false)})
	if _, err := o.updatePlan("goal", nil, []Step{
		{ID: "a", Title: "A", Kind: StepScout, Status: StepInProgress},
		{ID: "b", Title: "B", Kind: StepScout, Status: StepInProgress},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.delegateMany(context.Background(), []delegateArgs{
		{StepID: "a", Title: "A", Objective: "write a"},
		{StepID: "b", Title: "B", Objective: "write b"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if data, err := os.ReadFile(filepath.Join(repo, name)); err != nil || string(data) != strings.TrimSuffix(name, ".txt") {
			t.Fatalf("%s = %q err=%v", name, data, err)
		}
	}
}

func TestFullAccessWorkerFallsBackToMainWorkspace(t *testing.T) {
	prov := &reviewProvider{fn: func(kind string, call int) (*llm.Result, error) {
		switch call {
		case 1:
			return toolCall("w", "write", map[string]any{"path": "main-note.txt", "content": "written"}), nil
		case 2:
			return toolCall("r", "submit_report", map[string]any{"status": "completed", "summary": "wrote"}), nil
		default:
			return &llm.Result{Content: "done"}, nil
		}
	}}
	o, ag, _ := newOrch(t, prov, Config{FullWorkers: true, ReviewWriters: boolPtr(false)})
	if o.worktrees != nil {
		t.Fatal("test expects no worktree in a non-git directory")
	}
	if _, err := o.updatePlan("goal", nil, []Step{{ID: "s", Title: "Write", Kind: StepScout, Status: StepInProgress}}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.delegate(context.Background(), delegateArgs{StepID: "s", Title: "Write", Objective: "write"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(ag.Sess.Meta.Cwd, "main-note.txt"))
	if err != nil || string(data) != "written" {
		t.Fatalf("main-note.txt = %q err=%v", data, err)
	}
}

package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

type scriptProvider struct {
	leadCalls   int
	workerCalls int
	fn          func(call int, req llm.Request) (*llm.Result, error)
}

func (p *scriptProvider) Name() string { return "script" }
func (p *scriptProvider) ListModels(context.Context) ([]string, error) {
	return []string{"script-model"}, nil
}
func (p *scriptProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	if strings.Contains(req.System, "## Delegated worker") {
		p.workerCalls++
		return p.fn(p.workerCalls, req)
	}
	p.leadCalls++
	return p.fn(p.leadCalls, req)
}

func newOrch(t *testing.T, provider llm.Provider, cfg Config) (*Orchestrator, *agent.Agent, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	sess, err := session.Create(t.TempDir(), provider.Name(), "script-model", "test")
	if err != nil {
		t.Fatal(err)
	}
	ag := &agent.Agent{
		Provider: provider,
		Model:    "script-model",
		Tools:    tools.Registry(),
		Env: &tools.Env{
			Dir:    sess.Meta.Cwd,
			Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} },
		},
		Sess:     sess,
		MaxSteps: 8,
	}
	o, err := Attach(ag, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return o, ag, home
}

func TestLedgerValidate(t *testing.T) {
	valid := &Ledger{Steps: []Step{
		{ID: "a", Title: "A", Status: StepCompleted},
		{ID: "b", Title: "B", Status: StepInProgress, DependsOn: []string{"a"}},
	}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid ledger rejected: %v", err)
	}
	cases := []struct {
		name string
		l    *Ledger
		want string
	}{
		{"duplicate", &Ledger{Steps: []Step{{ID: "a", Title: "A", Status: StepPending}, {ID: "a", Title: "B", Status: StepPending}}}, "duplicate"},
		{"unknown dep", &Ledger{Steps: []Step{{ID: "a", Title: "A", Status: StepPending, DependsOn: []string{"x"}}}}, "unknown step"},
		{"cycle", &Ledger{Steps: []Step{
			{ID: "a", Title: "A", Status: StepPending, DependsOn: []string{"b"}},
			{ID: "b", Title: "B", Status: StepPending, DependsOn: []string{"a"}},
		}}, "cycle"},
		{"two active", &Ledger{Steps: []Step{
			{ID: "a", Title: "A", Status: StepInProgress},
			{ID: "b", Title: "B", Status: StepInProgress},
		}}, "at most one"},
		{"missing title", &Ledger{Steps: []Step{{ID: "a", Status: StepPending}}}, "title"},
		{"bad status", &Ledger{Steps: []Step{{ID: "a", Title: "A", Status: "nope"}}}, "invalid status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.l.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLedgerPersistenceAndCorruptBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.ledger.json")
	l := &Ledger{Goal: "ship it", Facts: []string{"a fact"}, Steps: []Step{{ID: "s1", Title: "Step", Status: StepInProgress}}}
	if err := l.Save(path); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("ledger perms = %v, want 0600", fi.Mode().Perm())
	}
	loaded, warning, err := LoadLedger(path)
	if err != nil || warning != "" {
		t.Fatalf("load: %v warning=%q", err, warning)
	}
	if loaded.Goal != "ship it" || len(loaded.Steps) != 1 || loaded.Steps[0].Status != StepInProgress {
		t.Fatalf("loaded = %+v", loaded)
	}

	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty, warning, err := LoadLedger(path)
	if err == nil || warning == "" {
		t.Fatalf("corrupt ledger should warn, got err=%v warning=%q", err, warning)
	}
	if len(empty.Steps) != 0 {
		t.Fatalf("corrupt ledger should reset, got %+v", empty)
	}
	backups, _ := filepath.Glob(path + ".corrupt-*")
	if len(backups) != 1 {
		t.Fatalf("backups = %v", backups)
	}
}

func TestUpdatePlanPreservesReports(t *testing.T) {
	o := &Orchestrator{ledger: &Ledger{}}
	steps := []Step{{ID: "s1", Title: "Survey", Status: StepInProgress}, {ID: "s2", Title: "Fix", Status: StepPending, DependsOn: []string{"s1"}}}
	out, err := o.updatePlan("goal", []string{"fact"}, steps)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "s1") || !strings.Contains(out, "in_progress") {
		t.Fatalf("output = %q", out)
	}
	o.ledger.Steps[0].Report = &WorkerReport{Status: "completed", Summary: "found it"}

	steps[0].Status = StepCompleted
	if _, err := o.updatePlan("goal", nil, steps); err != nil {
		t.Fatal(err)
	}
	if o.ledger.Steps[0].Report == nil || o.ledger.Steps[0].Report.Summary != "found it" {
		t.Fatalf("report not preserved: %+v", o.ledger.Steps[0])
	}
	if !strings.Contains(o.systemPrompt(), "found it") {
		t.Fatalf("prompt missing report: %s", o.systemPrompt())
	}

	bad := []Step{{ID: "s1", Title: "Survey", Status: StepPending, DependsOn: []string{"nope"}}}
	if _, err := o.updatePlan("goal", nil, bad); err == nil {
		t.Fatal("expected validation error")
	}
	if o.ledger.Steps[0].ID != "s1" || len(o.ledger.Steps) != 2 {
		t.Fatalf("failed update mutated ledger: %+v", o.ledger)
	}
}

func TestAttachToolsPromptAndTurnReset(t *testing.T) {
	p := &scriptProvider{fn: func(int, llm.Request) (*llm.Result, error) {
		return &llm.Result{Content: "ok"}, nil
	}}
	o, ag, _ := newOrch(t, p, Config{MaxDelegations: 2})
	var names []string
	for _, tool := range ag.Tools {
		names = append(names, tool.Name())
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "update_plan") || !strings.Contains(joined, "delegate") {
		t.Fatalf("tools = %v", names)
	}
	if !strings.Contains(ag.SystemExtra(), "## Orchestration") {
		t.Fatalf("missing orchestration prompt: %s", ag.SystemExtra())
	}
	o.mu.Lock()
	o.delegations = 2
	o.mu.Unlock()
	if err := ag.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	o.mu.Lock()
	got := o.delegations
	o.mu.Unlock()
	if got != 0 {
		t.Fatalf("delegations after turn = %d, want 0", got)
	}
	if _, err := Attach(ag, Config{}); err == nil {
		t.Fatal("second Attach should fail")
	}
}

func TestDelegateRunsReadOnlyWorkerAndUpdatesLedger(t *testing.T) {
	p := &scriptProvider{}
	p.fn = func(call int, req llm.Request) (*llm.Result, error) {
		if strings.Contains(req.System, "## Delegated worker") {
			if call != 1 {
				return nil, fmt.Errorf("worker call %d", call)
			}
			return &llm.Result{
				Content: `{"status":"completed","summary":"found the parser entry point","findings":[{"claim":"parse lives in parser.go","evidence":"func Parse","path":"parser.go","lines":"10-20"}],"changes":[],"tests":["go test ./..."],"risks":[],"next_steps":[]}`,
				Usage:   llm.Usage{In: 7, Out: 3},
			}, nil
		}
		switch call {
		case 1:
			args, _ := json.Marshal(delegateArgs{StepID: "s1", Title: "Survey", Objective: "Find the parser entry point", Deliverable: "path:line evidence"})
			return &llm.Result{
				ToolCalls: []llm.ToolCall{{ID: "c1", Name: "delegate", Arguments: string(args)}},
				Usage:     llm.Usage{In: 10, Out: 5},
			}, nil
		case 2:
			return &llm.Result{Content: "done", Usage: llm.Usage{In: 20, Out: 2}}, nil
		default:
			return nil, fmt.Errorf("unexpected lead call %d", call)
		}
	}
	o, ag, home := newOrch(t, p, Config{MaxDelegations: 2, WorkerMaxSteps: 4})
	if _, err := o.updatePlan("ship", nil, []Step{{ID: "s1", Title: "Survey", Status: StepInProgress}}); err != nil {
		t.Fatal(err)
	}
	if err := ag.Run(context.Background(), "please investigate"); err != nil {
		t.Fatal(err)
	}
	stats := ag.Usage()
	if stats.Calls != 3 || stats.In != 37 || stats.Out != 10 {
		t.Fatalf("usage = %+v", stats)
	}
	if o.Delegations() != 1 {
		t.Fatalf("delegations = %d", o.Delegations())
	}
	ledger, _, err := LoadLedger(LedgerPath(ag.Sess.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Steps) != 1 || ledger.Steps[0].Status != StepCompleted || ledger.Steps[0].Report == nil {
		t.Fatalf("ledger = %+v", ledger.Steps)
	}
	if ledger.Steps[0].Report.Summary != "found the parser entry point" {
		t.Fatalf("report = %+v", ledger.Steps[0].Report)
	}
	found := false
	for _, m := range ag.Sess.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, `"worker_session"`) && strings.Contains(m.Content, `"completed"`) {
			found = true
		}
	}
	if !found {
		t.Fatal("delegate tool result missing structured report")
	}
	sessions, _ := filepath.Glob(filepath.Join(home, "workers", "sessions", "*", "*.jsonl"))
	if len(sessions) != 1 {
		t.Fatalf("worker sessions = %v", sessions)
	}
}

func TestDelegateBudgetAndUnknownStep(t *testing.T) {
	o := &Orchestrator{ledger: &Ledger{}, cfg: Config{MaxDelegations: 1}}
	o.delegations = 1
	_, err := o.delegate(context.Background(), delegateArgs{Title: "t", Objective: "o"})
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("budget error = %v", err)
	}
	o.delegations = 0
	_, err = o.delegate(context.Background(), delegateArgs{StepID: "missing", Title: "t", Objective: "o"})
	if err == nil || !strings.Contains(err.Error(), "unknown step") {
		t.Fatalf("unknown step error = %v", err)
	}
	if o.delegations != 0 || o.Delegations() != 0 {
		t.Fatalf("unknown step consumed budget: per-turn=%d total=%d", o.delegations, o.Delegations())
	}
	o.ledger = &Ledger{Steps: []Step{
		{ID: "a", Title: "A", Status: StepPending},
		{ID: "b", Title: "B", Status: StepPending, DependsOn: []string{"a"}},
	}}
	_, err = o.delegate(context.Background(), delegateArgs{StepID: "b", Title: "t", Objective: "o"})
	if err == nil || !strings.Contains(err.Error(), "depends on") {
		t.Fatalf("dependency error = %v", err)
	}
}

func TestParseReport(t *testing.T) {
	r, ok := parseReport("```json\n{\"status\":\"done\",\"summary\":\"all good\",\"findings\":[{\"claim\":\"x\",\"path\":\"a.go\",\"lines\":\"1-2\"}]}\n```")
	if !ok {
		t.Fatal("fenced report not parsed")
	}
	if r.Status != "completed" || r.Summary != "all good" || len(r.Findings) != 1 {
		t.Fatalf("report = %+v", r)
	}
	if _, ok := parseReport("just prose"); ok {
		t.Fatal("prose unexpectedly parsed")
	}
	f := fallbackReport("some partial findings")
	if f.Status != "unstructured" || f.Summary != "some partial findings" {
		t.Fatalf("fallback = %+v", f)
	}
	if got := normalizeReportStatus("BLOCKED"); got != "blocked" {
		t.Fatalf("status = %q", got)
	}
	if reportStatusToStep("partial") != StepPending {
		t.Fatal("partial should leave the step pending")
	}
}

func TestWorkerSystemPromptIsReadOnly(t *testing.T) {
	prompt := workerSystemPrompt(delegateArgs{Title: "x", Objective: "y", Deliverable: "z"}, &Step{ID: "s1", Title: "step"}, "goal", []string{"fact"})
	for _, want := range []string{"read-only", "JSON", "s1", "fact"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestWorkerHooks(t *testing.T) {
	var usageHistory int
	var toolName, toolDetail string
	parent := agent.Hooks{
		OnUsage: func(_ llm.Usage, history int) { usageHistory = history },
		OnTool:  func(name, detail string) { toolName, toolDetail = name, detail },
	}
	h := workerHooks(parent, "Survey")
	h.OnUsage(llm.Usage{In: 3}, 99)
	if usageHistory != -1 {
		t.Fatalf("worker usage history = %d, want -1", usageHistory)
	}
	h.OnTool("read", "a.go")
	if toolName != "worker:read" || !strings.Contains(toolDetail, "Survey") {
		t.Fatalf("tool hook = %q %q", toolName, toolDetail)
	}
}

func TestLedgerPath(t *testing.T) {
	if got := LedgerPath("/tmp/a/b.jsonl"); got != "/tmp/a/b.ledger.json" {
		t.Fatalf("path = %q", got)
	}
	if got := LedgerPath(""); got != "" {
		t.Fatalf("empty path = %q", got)
	}
}

func TestLedgerSaveNoPath(t *testing.T) {
	if err := (&Ledger{}).Save(""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadLedger(""); err != nil {
		t.Fatal(err)
	}
}

type blockedProvider struct{}

func (b *blockedProvider) Name() string { return "blocked" }
func (b *blockedProvider) ListModels(context.Context) ([]string, error) {
	return nil, nil
}
func (b *blockedProvider) Chat(ctx context.Context, _ llm.Request, _ func(string)) (*llm.Result, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestDelegationTimeout(t *testing.T) {
	o, _, _ := newOrch(t, &blockedProvider{}, Config{MaxDelegations: 1, WorkerTimeout: 20 * time.Millisecond})
	start := time.Now()
	_, usage, _, err := o.runWorker(context.Background(), delegateArgs{Title: "slow", Objective: "wait"}, nil, "", nil, 2)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout error = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout took too long")
	}
	if usage.Calls != 0 {
		t.Fatalf("usage = %+v", usage)
	}
}

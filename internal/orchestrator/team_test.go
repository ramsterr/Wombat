package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/agents"
	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

type scriptedProvider struct {
	mu      sync.Mutex
	calls   []llm.Request
	handler func(req llm.Request, call int) *llm.Result
}

func (p *scriptedProvider) Name() string { return "team-mock" }
func (p *scriptedProvider) ListModels(context.Context) ([]string, error) {
	return []string{"team-mock"}, nil
}

func (p *scriptedProvider) Chat(_ context.Context, req llm.Request, _ func(string)) (*llm.Result, error) {
	p.mu.Lock()
	p.calls = append(p.calls, req)
	call := len(p.calls)
	handler := p.handler
	p.mu.Unlock()
	if handler != nil {
		return handler(req, call), nil
	}
	return reportResult(`{"status":"completed","summary":"done"}`), nil
}

func (p *scriptedProvider) requests() []llm.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llm.Request(nil), p.calls...)
}

func reportResult(summary string) *llm.Result {
	return &llm.Result{Content: summary, Usage: llm.Usage{In: 10, Out: 5}}
}

func testMember(id string, opts ...func(*agents.Member)) *agents.Member {
	m := &agents.Member{
		ID:       id,
		Def:      &config.AgentDef{Name: id, Description: id + " specialist"},
		Persona:  "## Your role: " + id + "\nYou are the " + id + " specialist.",
		MaxSteps: 4,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func newTeamOrchestrator(t *testing.T, prov llm.Provider, team []*agents.Member, leadTools []tools.Tool) (*Orchestrator, *session.Session) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	sess, err := session.CreateAt(home, t.TempDir(), "team-mock", "lead-model", "t")
	if err != nil {
		t.Fatal(err)
	}
	ag := &agent.Agent{
		Provider: prov, Model: "lead-model", Tools: leadTools,
		Env:  &tools.Env{Dir: sess.Meta.Cwd, Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} }},
		Sess: sess, MaxSteps: 8,
	}
	o, err := Attach(ag, Config{
		MaxParallelScouts: 5, MaxParallelAgents: 5, WorkerMaxSteps: 4,
		WorkerTimeout: time.Minute, Team: team,
	})
	if err != nil {
		t.Fatal(err)
	}
	return o, sess
}

func stepIDFromSystem(system string) string {
	const marker = "Ledger step: ["
	i := strings.Index(system, marker)
	if i < 0 {
		return ""
	}
	rest := system[i+len(marker):]
	if j := strings.Index(rest, "]"); j >= 0 {
		return rest[:j]
	}
	return ""
}

func briefFromRequest(req llm.Request) string {
	var last string
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			last = msg.Content
		}
	}
	return last
}

func TestUpdatePlanRejectsUnknownCustomAgent(t *testing.T) {
	prov := &scriptedProvider{}
	team := []*agents.Member{testMember("math")}
	o, _ := newTeamOrchestrator(t, prov, team, tools.Registry())

	_, err := o.updatePlan("goal", nil, []Step{{ID: "s1", Title: "S", Status: StepPending, Agent: "designer"}})
	if err == nil || !strings.Contains(err.Error(), `unknown agent "designer"`) {
		t.Fatalf("error = %v, want an unknown agent error", err)
	}
	if _, err := o.updatePlan("goal", nil, []Step{{ID: "s1", Title: "S", Status: StepPending, Agent: "math"}}); err != nil {
		t.Fatalf("known agent rejected: %v", err)
	}
	// "lead" clears the assignment instead of failing.
	if _, err := o.updatePlan("goal", nil, []Step{{ID: "s1", Title: "S", Status: StepPending, Agent: "lead"}}); err != nil {
		t.Fatalf("lead alias rejected: %v", err)
	}
	if step := o.ledger.Find("s1"); step == nil || step.Agent != "" {
		t.Fatalf("lead alias should clear the agent, got %+v", step)
	}
}

func TestAttachRejectsDuplicateCustomAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	sess, err := session.CreateAt(home, t.TempDir(), "team-mock", "lead-model", "t")
	if err != nil {
		t.Fatal(err)
	}
	ag := &agent.Agent{Provider: &scriptedProvider{}, Model: "lead-model", Tools: tools.Registry(),
		Env: &tools.Env{Dir: sess.Meta.Cwd}, Sess: sess}
	_, err = Attach(ag, Config{Team: []*agents.Member{testMember("math"), testMember("Math")}})
	if err == nil || !strings.Contains(err.Error(), "duplicate custom agent") {
		t.Fatalf("error = %v, want a duplicate error", err)
	}
}

func TestPlanAssignsStepsToCustomAgents(t *testing.T) {
	prov := &scriptedProvider{}
	math := testMember("math", func(m *agents.Member) {
		m.Model = "math-model"
		m.ReadOnly = true
		m.Persona = "MATH-PERSONA verifies proofs"
	})
	o, _ := newTeamOrchestrator(t, prov, []*agents.Member{math}, tools.Registry())
	if _, err := o.updatePlan("research math", nil, []Step{
		{ID: "research", Title: "Research", Status: StepPending, Agent: "math",
			Deliverable: "a verified proof summary"},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{})
	if err != nil {
		t.Fatal(err)
	}
	var result planRunResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("parse result: %v (%s)", err, out)
	}
	if result.Status != "completed" {
		t.Fatalf("status = %q (%s)", result.Status, out)
	}
	var sawPersona bool
	for _, req := range prov.requests() {
		if req.Model != "math-model" {
			t.Errorf("model = %q, want math-model", req.Model)
		}
		if strings.Contains(req.System, "MATH-PERSONA") {
			sawPersona = true
		}
	}
	if !sawPersona {
		t.Error("the custom agent persona was not injected into the worker system prompt")
	}
	step := o.ledger.Find("research")
	if step == nil || step.Status != StepCompleted {
		t.Fatalf("step = %+v", step)
	}
	if step.Report == nil || step.Report.Summary != "done" {
		t.Fatalf("report = %+v", step.Report)
	}
}

// barrier proves that two workers were in flight at the same time.
type barrier struct {
	mu     sync.Mutex
	n      int
	target int
	done   chan struct{}
	failed atomic.Bool
}

func newBarrier(target int) *barrier {
	return &barrier{target: target, done: make(chan struct{})}
}

func (b *barrier) wait(timeout time.Duration) {
	b.mu.Lock()
	b.n++
	if b.n == b.target {
		close(b.done)
	}
	b.mu.Unlock()
	select {
	case <-b.done:
	case <-time.After(timeout):
		b.failed.Store(true)
	}
}

func TestPlanRunsIndependentCustomAgentsInParallel(t *testing.T) {
	b := newBarrier(2)
	prov := &scriptedProvider{handler: func(req llm.Request, call int) *llm.Result {
		stepID := stepIDFromSystem(req.System)
		b.wait(5 * time.Second)
		return reportResult(fmt.Sprintf(`{"status":"completed","summary":"result %s"}`, stepID))
	}}
	team := []*agents.Member{testMember("math"), testMember("design")}
	o, _ := newTeamOrchestrator(t, prov, team, tools.Registry())
	if _, err := o.updatePlan("research and design", nil, []Step{
		{ID: "math", Title: "Math", Status: StepPending, Agent: "math"},
		{ID: "design", Title: "Design", Status: StepPending, Agent: "design"},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if b.failed.Load() {
		t.Fatalf("the custom agents did not run in parallel: %s", out)
	}
	var result planRunResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" {
		t.Fatalf("status = %q (%s)", result.Status, out)
	}
	for _, id := range []string{"math", "design"} {
		step := o.ledger.Find(id)
		if step == nil || step.Status != StepCompleted {
			t.Errorf("step %s = %+v", id, step)
		}
	}
}

func TestDependentCustomAgentReceivesUpstreamReport(t *testing.T) {
	var mu sync.Mutex
	var designBrief string
	prov := &scriptedProvider{handler: func(req llm.Request, call int) *llm.Result {
		stepID := stepIDFromSystem(req.System)
		switch stepID {
		case "research":
			return reportResult(`{"status":"completed","summary":"the key result is 42","findings":[{"claim":"42 is the answer","evidence":"chapter 3"}]}`)
		case "design":
			mu.Lock()
			designBrief = briefFromRequest(req)
			mu.Unlock()
			return reportResult(`{"status":"completed","summary":"designed the deck"}`)
		default:
			return reportResult(`{"status":"completed","summary":"ok"}`)
		}
	}}
	team := []*agents.Member{testMember("math"), testMember("design")}
	o, _ := newTeamOrchestrator(t, prov, team, tools.Registry())
	if _, err := o.updatePlan("research then design", nil, []Step{
		{ID: "research", Title: "Research", Status: StepPending, Agent: "math"},
		{ID: "design", Title: "Design", Status: StepPending, Agent: "design", DependsOn: []string{"research"}},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{})
	if err != nil {
		t.Fatal(err)
	}
	var result planRunResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" {
		t.Fatalf("status = %q (%s)", result.Status, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(designBrief, "## Upstream results") || !strings.Contains(designBrief, "the key result is 42") {
		t.Fatalf("design brief did not receive the research report:\n%s", designBrief)
	}
}

func TestCustomAgentsWithOverlappingFilesRunSerially(t *testing.T) {
	var mu sync.Mutex
	var events []string
	prov := &scriptedProvider{handler: func(req llm.Request, call int) *llm.Result {
		stepID := stepIDFromSystem(req.System)
		mu.Lock()
		events = append(events, "start:"+stepID)
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		events = append(events, "finish:"+stepID)
		mu.Unlock()
		return reportResult(`{"status":"completed","summary":"wrote the file"}`)
	}}
	team := []*agents.Member{testMember("design")}
	o, _ := newTeamOrchestrator(t, prov, team, tools.Registry())
	if _, err := o.updatePlan("design", nil, []Step{
		{ID: "alpha", Title: "Alpha", Status: StepPending, Agent: "design", Files: []string{"deck.md"}},
		{ID: "beta", Title: "Beta", Status: StepPending, Agent: "design", Files: []string{"deck.md"}},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := o.runPlan(context.Background(), runPlanArgs{})
	if err != nil {
		t.Fatal(err)
	}
	var result planRunResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" {
		t.Fatalf("status = %q (%s)", result.Status, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 4 {
		t.Fatalf("events = %v", events)
	}
	if events[0] != "start:alpha" || events[1] != "finish:alpha" ||
		events[2] != "start:beta" || events[3] != "finish:beta" {
		t.Fatalf("overlapping files were not serialized: %v", events)
	}
}

// probeTool records the environment a custom agent actually gets.
type probeTool struct {
	mu          sync.Mutex
	readOnly    bool
	writeDenied bool
	ran         bool
}

func (p *probeTool) Name() string           { return "probe" }
func (p *probeTool) Class() string          { return "read" }
func (p *probeTool) Description() string    { return "probe" }
func (p *probeTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (p *probeTool) Run(_ context.Context, _ json.RawMessage, env *tools.Env) (string, error) {
	err := env.Check("write", "x.txt")
	p.mu.Lock()
	p.ran = true
	p.readOnly = env.ReadOnly
	p.writeDenied = err != nil
	p.mu.Unlock()
	return "probe result", nil
}

func TestCustomAgentEnvEnforcesReadOnlyAndPermissions(t *testing.T) {
	probe := &probeTool{}
	prov := &scriptedProvider{handler: func(req llm.Request, call int) *llm.Result {
		for _, msg := range req.Messages {
			if msg.Role == "tool" && msg.Name == "probe" {
				return reportResult(`{"status":"completed","summary":"probed"}`)
			}
		}
		return &llm.Result{Content: `{"status":"partial","summary":"checking"}`,
			ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "probe", Arguments: "{}"}}}
	}}
	member := testMember("math", func(m *agents.Member) {
		m.ReadOnly = true
		m.Tools = []tools.Tool{probe}
		m.PermRules = map[string]string{"write": "deny"}
		m.Persona = "MATH"
	})
	o, _ := newTeamOrchestrator(t, prov, []*agents.Member{member}, tools.Registry())
	report, _, _, err := o.runTeamWorker(context.Background(), delegateArgs{
		Title: "probe env", Objective: "probe", Deliverable: "result",
	}, member, nil, "goal", nil, 4, 0)
	if err != nil {
		t.Fatalf("runTeamWorker: %v", err)
	}
	if report == nil || report.Status != "completed" {
		t.Fatalf("report = %+v", report)
	}
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if !probe.ran {
		t.Fatal("the probe tool never ran")
	}
	if !probe.readOnly {
		t.Error("ReadOnly was not applied to the custom agent environment")
	}
	if !probe.writeDenied {
		t.Error("the write deny rule was not enforced")
	}
}

func TestDelegateRoutesToCustomAgent(t *testing.T) {
	prov := &scriptedProvider{}
	member := testMember("design", func(m *agents.Member) {
		m.Model = "design-model"
		m.Persona = "DESIGN-PERSONA"
	})
	o, _ := newTeamOrchestrator(t, prov, []*agents.Member{member}, tools.Registry())
	out, err := o.delegate(context.Background(), delegateArgs{
		Title: "design a deck", Objective: "design it", Deliverable: "outline", Agent: "design",
	})
	if err != nil {
		t.Fatal(err)
	}
	var result delegateResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Report == nil || result.Report.Summary != "done" {
		t.Fatalf("report = %+v (%s)", result.Report, out)
	}
	requests := prov.requests()
	if len(requests) == 0 || requests[0].Model != "design-model" {
		t.Fatalf("requests = %+v", requests)
	}
	if !strings.Contains(requests[0].System, "DESIGN-PERSONA") {
		t.Error("the custom agent persona was not used")
	}
}

func TestUnknownAgentDelegationFails(t *testing.T) {
	o, _ := newTeamOrchestrator(t, &scriptedProvider{}, []*agents.Member{testMember("math")}, tools.Registry())
	_, err := o.delegate(context.Background(), delegateArgs{
		Title: "t", Objective: "o", Agent: "design",
	})
	if err == nil || !strings.Contains(err.Error(), `unknown agent "design"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestResumeCustomAgentAfterNeedsInput(t *testing.T) {
	prov := &scriptedProvider{handler: func(req llm.Request, call int) *llm.Result {
		brief := briefFromRequest(req)
		if strings.Contains(brief, "Follow-up from the lead") {
			return reportResult(`{"status":"completed","summary":"finished after the answer"}`)
		}
		return reportResult(`{"status":"needs_input","summary":"need a decision","questions":["which format?"]}`)
	}}
	member := testMember("math")
	o, _ := newTeamOrchestrator(t, prov, []*agents.Member{member}, tools.Registry())
	out, err := o.delegate(context.Background(), delegateArgs{
		Title: "research", Objective: "research it", Agent: "math",
	})
	if err != nil {
		t.Fatal(err)
	}
	var first delegateResult
	if err := json.Unmarshal([]byte(out), &first); err != nil {
		t.Fatal(err)
	}
	if first.Report == nil || first.Report.Status != "needs_input" {
		t.Fatalf("first report = %+v (%s)", first.Report, out)
	}
	if first.WorkerSession == "" {
		t.Fatal("needs_input outcome has no worker session")
	}
	resumed, err := o.resumeWorker(context.Background(), resumeWorkerArgs{
		WorkerSession: first.WorkerSession,
		Answer:        "use a proof sketch",
	})
	if err != nil {
		t.Fatal(err)
	}
	var second delegateResult
	if err := json.Unmarshal([]byte(resumed), &second); err != nil {
		t.Fatal(err)
	}
	if second.Report == nil || second.Report.Status != "completed" {
		t.Fatalf("second report = %+v (%s)", second.Report, resumed)
	}
	if !strings.Contains(second.Report.Summary, "finished after the answer") {
		t.Fatalf("summary = %q", second.Report.Summary)
	}
}

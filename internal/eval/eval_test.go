package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/tools"
)

type fixedProvider struct {
	calls int
	fix   bool
}

func (f *fixedProvider) Name() string { return "fake" }
func (f *fixedProvider) ListModels(context.Context) ([]string, error) {
	return []string{"fake-model"}, nil
}
func (f *fixedProvider) Chat(context.Context, llm.Request, func(string)) (*llm.Result, error) {
	f.calls++
	if f.fix && f.calls == 1 {
		return &llm.Result{
			ToolCalls: []llm.ToolCall{{
				ID:        "c1",
				Name:      "write",
				Arguments: `{"path":"fixed.txt","content":"ok"}`,
			}},
			Usage: llm.Usage{In: 10, Out: 2},
		}, nil
	}
	return &llm.Result{Content: "done", Usage: llm.Usage{In: 5, Out: 1, Cached: 1}}, nil
}

func testTask() Task {
	return Task{
		ID:     "write-fixed",
		Name:   "write the fixed file",
		Prompt: "Create fixed.txt with the content ok.",
		Files:  map[string]string{"input.txt": "bug\n"},
		Check:  []string{`test "$(cat fixed.txt)" = "ok"`},
	}
}

func testOptions(provider llm.Provider) Options {
	return Options{
		Provider:       provider,
		Model:          "fake-model",
		Tools:          tools.Registry(),
		Env:            tools.Env{Permit: func(string, string) tools.Decision { return tools.Decision{Allow: true} }},
		MaxSteps:       4,
		Timeout:        20 * time.Second,
		CommandTimeout: 10 * time.Second,
		Home:           tTempDir(),
	}
}

func tTempDir() string {
	dir, err := os.MkdirTemp("", "wombat-eval-test-")
	if err != nil {
		panic(err)
	}
	return dir
}

func TestRunTaskPasses(t *testing.T) {
	opts := testOptions(&fixedProvider{fix: true})
	defer os.RemoveAll(opts.Home)
	result := RunTask(context.Background(), testTask(), opts)
	if !result.Passed {
		t.Fatalf("task failed: %+v", result)
	}
	if result.Steps != 2 || result.Usage.In != 15 || result.Usage.Out != 3 || result.Usage.Cached != 1 {
		t.Fatalf("usage = %+v", result.Usage)
	}
	if result.DurationMS <= 0 {
		t.Fatalf("duration = %d", result.DurationMS)
	}
	if result.Error != "" {
		t.Fatalf("error = %q", result.Error)
	}
}

func TestRunTaskFailsCheck(t *testing.T) {
	opts := testOptions(&fixedProvider{fix: false})
	defer os.RemoveAll(opts.Home)
	result := RunTask(context.Background(), testTask(), opts)
	if result.Passed {
		t.Fatalf("task should fail: %+v", result)
	}
	if !strings.Contains(result.Error, "check:") {
		t.Fatalf("error = %q", result.Error)
	}
	if !strings.Contains(result.CheckOutput, "$ test") {
		t.Fatalf("check output = %q", result.CheckOutput)
	}
}

func TestPrepareRejectsTraversal(t *testing.T) {
	task := Task{
		ID:     "escape",
		Prompt: "x",
		Files:  map[string]string{"../escape.txt": "x"},
		Check:  []string{"true"},
	}
	err := prepareWorkdir(task, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected traversal error, got %v", err)
	}
}

func TestLoadTasks(t *testing.T) {
	dir := t.TempDir()
	raw := `{
		"id":"one",
		"prompt":"fix it",
		"files":{"a.txt":"x"},
		"check":["test -f a.txt"]
	}`
	if err := os.WriteFile(filepath.Join(dir, "one.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	tasks, err := LoadTasks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "one" || tasks[0].sourceDir != dir {
		t.Fatalf("tasks = %+v", tasks)
	}
}

func TestLoadTasksRejectsDuplicateIDs(t *testing.T) {
	dir := t.TempDir()
	raw := `{"id":"one","prompt":"fix it","files":{"a.txt":"x"},"check":["true"]}`
	for _, name := range []string{"one.json", "two.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LoadTasks(dir); err == nil || !strings.Contains(err.Error(), "duplicate task id") {
		t.Fatalf("expected duplicate-id error, got %v", err)
	}
}

func TestValidateRejectsFixtureTraversal(t *testing.T) {
	task := Task{
		ID:      "escape",
		Prompt:  "x",
		Fixture: "../fixture",
		Check:   []string{"true"},
	}
	if err := task.Validate(); err == nil || !strings.Contains(err.Error(), "fixture") {
		t.Fatalf("expected fixture validation error, got %v", err)
	}
}

func TestReportMarkdown(t *testing.T) {
	report := Summarize([]Result{
		{TaskID: "a", Passed: true, Usage: agent.UsageStats{In: 10, Out: 2}, DurationMS: 5},
		{TaskID: "b", Passed: false, Error: "check: failed", Usage: agent.UsageStats{In: 3, Out: 1}, DurationMS: 7},
	}, "fake/model", agent.Pricing{InputPerMTok: 1, OutputPerMTok: 2})
	if report.Tasks != 2 || report.Passed != 1 || report.PassRate != 0.5 {
		t.Fatalf("report = %+v", report)
	}
	md := report.Markdown()
	if !strings.Contains(md, "Wombat Eval Report") || !strings.Contains(md, "| b | no |") {
		t.Fatalf("markdown = %q", md)
	}
}

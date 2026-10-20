package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/orchestrator"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

type Task struct {
	ID                    string            `json:"id"`
	Name                  string            `json:"name,omitempty"`
	Prompt                string            `json:"prompt"`
	Files                 map[string]string `json:"files,omitempty"`
	Fixture               string            `json:"fixture,omitempty"`
	Setup                 []string          `json:"setup,omitempty"`
	Check                 []string          `json:"check"`
	TimeoutSeconds        int               `json:"timeout_seconds,omitempty"`
	CommandTimeoutSeconds int               `json:"command_timeout_seconds,omitempty"`
	AllowTestEdits        bool              `json:"allow_test_edits,omitempty"`
	Tags                  []string          `json:"tags,omitempty"`

	sourceDir string
}

func (t Task) Validate() error {
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("task id is required")
	}
	if strings.TrimSpace(t.Prompt) == "" {
		return fmt.Errorf("task %s: prompt is required", t.ID)
	}
	if len(t.Check) == 0 {
		return fmt.Errorf("task %s: at least one check command is required", t.ID)
	}
	if t.Fixture != "" && len(t.Files) > 0 {
		return fmt.Errorf("task %s: use fixture or files, not both", t.ID)
	}
	if t.Fixture != "" {
		if err := safeRelPath(t.Fixture); err != nil {
			return fmt.Errorf("task %s: fixture: %w", t.ID, err)
		}
	}
	for path := range t.Files {
		if err := safeRelPath(path); err != nil {
			return fmt.Errorf("task %s: %w", t.ID, err)
		}
	}
	return nil
}

type Options struct {
	Provider       llm.Provider
	Model          string
	Tools          []tools.Tool
	Env            tools.Env
	ContextWindow  int
	NumCtx         int
	Timeout        time.Duration
	CommandTimeout time.Duration
	Home           string
	KeepWorkdir    bool
	Pricing        agent.Pricing
	Orchestrator   bool
	OnTaskStart    func(Task)
}

type Result struct {
	TaskID      string           `json:"task_id"`
	Run         int              `json:"run,omitempty"`
	Name        string           `json:"name,omitempty"`
	Passed      bool             `json:"passed"`
	Error       string           `json:"error,omitempty"`
	CheckOutput string           `json:"check_output,omitempty"`
	Workdir     string           `json:"workdir,omitempty"`
	DurationMS  int64            `json:"duration_ms"`
	Steps       int              `json:"steps"`
	Delegations int              `json:"delegations,omitempty"`
	Usage       agent.UsageStats `json:"usage"`
	CostUSD     float64          `json:"cost_usd"`
}

type Report struct {
	Started      time.Time        `json:"started"`
	DurationMS   int64            `json:"duration_ms"`
	Model        string           `json:"model"`
	Tasks        int              `json:"tasks"`
	Passed       int              `json:"passed"`
	PassRate     float64          `json:"pass_rate"`
	PassRateLow  float64          `json:"pass_rate_low"`
	PassRateHigh float64          `json:"pass_rate_high"`
	Failures     map[string]int   `json:"failures,omitempty"`
	Usage        agent.UsageStats `json:"usage"`
	Delegations  int              `json:"delegations,omitempty"`
	CostUSD      float64          `json:"cost_usd"`
	Pricing      agent.Pricing    `json:"pricing"`
	Results      []Result         `json:"results"`
}

func LoadTasks(dir string) ([]Task, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var tasks []Task
	seen := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var task Task
		if err := json.Unmarshal(raw, &task); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		task.sourceDir = dir
		if err := task.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if prev, ok := seen[task.ID]; ok {
			return nil, fmt.Errorf("%s: duplicate task id %q (also in %s)", path, task.ID, prev)
		}
		seen[task.ID] = path
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return tasks, nil
}

func RunTask(ctx context.Context, task Task, opts Options) Result {
	start := time.Now()
	result := Result{TaskID: task.ID, Name: task.Name}
	if err := task.Validate(); err != nil {
		result.Error = err.Error()
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	if opts.Provider == nil {
		result.Error = "no provider configured"
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	if opts.OnTaskStart != nil {
		opts.OnTaskStart(task)
	}

	workdir, err := os.MkdirTemp("", "wombat-eval-"+safeName(task.ID)+"-")
	if err != nil {
		result.Error = err.Error()
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	if !opts.KeepWorkdir {
		defer os.RemoveAll(workdir)
	} else {
		result.Workdir = workdir
	}
	if err := prepareWorkdir(task, workdir); err != nil {
		result.Error = "prepare: " + err.Error()
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}

	home := opts.Home
	if home == "" {
		home, err = os.MkdirTemp("", "wombat-eval-home-")
		if err != nil {
			result.Error = err.Error()
			result.DurationMS = time.Since(start).Milliseconds()
			return result
		}
		defer os.RemoveAll(home)
	}
	sess, err := session.CreateAt(home, workdir, opts.Provider.Name(), opts.Model, task.ID)
	if err != nil {
		result.Error = "session: " + err.Error()
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}

	commandTimeout := opts.CommandTimeout
	if commandTimeout <= 0 {
		commandTimeout = 2 * time.Minute
	}
	if out, err := runCommands(ctx, workdir, task.Setup, commandTimeout); err != nil {
		result.Error = "setup: " + err.Error()
		result.CheckOutput = out
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	var pristine string
	if !task.AllowTestEdits {
		pristine, err = snapshotProtected(workdir)
		if err != nil {
			result.Error = "protect: " + err.Error()
			result.DurationMS = time.Since(start).Milliseconds()
			return result
		}
		if pristine != "" {
			defer os.RemoveAll(pristine)
		}
	}

	env := opts.Env
	env.Dir = workdir
	toolsList := opts.Tools
	if len(toolsList) == 0 {
		toolsList = tools.Registry()
	}
	ag := &agent.Agent{
		Provider:      opts.Provider,
		Model:         opts.Model,
		Tools:         toolsList,
		Env:           &env,
		Sess:          sess,
		Compact:       false,
		ContextWindow: opts.ContextWindow,
		NumCtx:        opts.NumCtx,
	}
	var orch *orchestrator.Orchestrator
	if opts.Orchestrator {
		orch, err = orchestrator.Attach(ag, orchestrator.Config{
			WorkerHome: filepath.Join(home, "workers"),
		})
		if err != nil {
			result.Error = "orchestrator: " + err.Error()
			result.DurationMS = time.Since(start).Milliseconds()
			return result
		}
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	if task.TimeoutSeconds > 0 {
		timeout = time.Duration(task.TimeoutSeconds) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	runErr := ag.Run(runCtx, task.Prompt)
	if pristine != "" {
		if restoreErr := restoreProtected(workdir, pristine); restoreErr != nil {
			if runErr == nil {
				runErr = fmt.Errorf("restore protected files: %w", restoreErr)
			}
		}
	}

	checkTimeout := commandTimeout
	if task.CommandTimeoutSeconds > 0 {
		checkTimeout = time.Duration(task.CommandTimeoutSeconds) * time.Second
	}
	checkOut, checkErr := runCommands(ctx, workdir, task.Check, checkTimeout)
	result.CheckOutput = clip(checkOut, 8000)
	result.Usage = ag.Usage()
	result.Steps = result.Usage.Calls
	if orch != nil {
		result.Delegations = orch.Delegations()
	}
	result.CostUSD = opts.Pricing.Cost(result.Usage)
	result.DurationMS = time.Since(start).Milliseconds()
	switch {
	case runErr != nil:
		result.Error = "agent: " + runErr.Error()
	case checkErr != nil:
		result.Error = "check: " + checkErr.Error()
	default:
		result.Passed = true
	}
	return result
}

func prepareWorkdir(task Task, workdir string) error {
	if task.Fixture != "" {
		src := filepath.Join(task.sourceDir, task.Fixture)
		if err := copyDir(src, workdir); err != nil {
			return err
		}
	}
	for path, content := range task.Files {
		if err := safeRelPath(path); err != nil {
			return err
		}
		dst := filepath.Join(workdir, path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("fixture %s is not a directory", src)
	}
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("fixture symlinks are not supported: %s", rel)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func runCommands(ctx context.Context, dir string, commands []string, timeout time.Duration) (string, error) {
	var out strings.Builder
	for _, command := range commands {
		if strings.TrimSpace(command) == "" {
			continue
		}
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		cmd := exec.CommandContext(runCtx, "sh", "-c", command)
		cmd.Dir = dir
		cmd.WaitDelay = 2 * time.Second
		data, err := cmd.CombinedOutput()
		cancel()
		out.WriteString("$ " + command + "\n")
		out.Write(data)
		if !strings.HasSuffix(out.String(), "\n") {
			out.WriteString("\n")
		}
		if err != nil {
			return out.String(), fmt.Errorf("%s: %w", command, err)
		}
	}
	return out.String(), nil
}

func Summarize(results []Result, model string, pricing agent.Pricing) Report {
	report := Report{
		Started: time.Now(),
		Model:   model,
		Tasks:   len(results),
		Pricing: pricing,
		Results: results,
	}
	for _, result := range results {
		if result.Passed {
			report.Passed++
		}
		report.Usage.Calls += result.Usage.Calls
		report.Usage.In += result.Usage.In
		report.Usage.Cached += result.Usage.Cached
		report.Usage.Out += result.Usage.Out
		report.Usage.Duration += time.Duration(result.DurationMS) * time.Millisecond
		report.Delegations += result.Delegations
		report.CostUSD += result.CostUSD
	}
	if report.Tasks > 0 {
		report.PassRate = float64(report.Passed) / float64(report.Tasks)
		report.PassRateLow, report.PassRateHigh = wilson(report.Passed, report.Tasks)
	}
	report.Failures = map[string]int{}
	for _, result := range results {
		if result.Passed {
			continue
		}
		report.Failures[failureClass(result.Error)]++
	}
	report.DurationMS = report.Usage.Duration.Milliseconds()
	return report
}

func WriteReport(dir string, report Report) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "report.md"), []byte(report.Markdown()), 0o644)
}

func (r Report) Markdown() string {
	var b strings.Builder
	passRate := 0.0
	if r.Tasks > 0 {
		passRate = 100 * r.PassRate
	}
	fmt.Fprintf(&b, "# Wombat Eval Report\n\n")
	fmt.Fprintf(&b, "- Model: `%s`\n", r.Model)
	fmt.Fprintf(&b, "- Tasks: %d\n", r.Tasks)
	fmt.Fprintf(&b, "- Passed: %d (%.1f%%, 95%% CI %.1f–%.1f%%)\n", r.Passed, passRate, 100*r.PassRateLow, 100*r.PassRateHigh)
	fmt.Fprintf(&b, "- Tokens: in %d, cached %d, out %d\n", r.Usage.In, r.Usage.Cached, r.Usage.Out)
	if r.Delegations > 0 {
		fmt.Fprintf(&b, "- Delegations: %d\n", r.Delegations)
	}
	fmt.Fprintf(&b, "- Estimated cost: $%.4f\n", r.CostUSD)
	fmt.Fprintf(&b, "- Wall clock: %s\n", (time.Duration(r.DurationMS) * time.Millisecond).Round(time.Millisecond))
	if len(r.Failures) > 0 {
		var classes []string
		for class, n := range r.Failures {
			classes = append(classes, fmt.Sprintf("%s=%d", class, n))
		}
		sort.Strings(classes)
		fmt.Fprintf(&b, "- Failure classes: %s\n", strings.Join(classes, ", "))
	}
	b.WriteString("\n| Task | Run | Passed | Steps | Delegations | In | Out | Cost | Duration | Error |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|---|\n")
	for _, result := range r.Results {
		mark := "no"
		if result.Passed {
			mark = "yes"
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %d | %d | %d | %d | $%.4f | %s | %s |\n",
			mdCell(result.TaskID), result.Run, mark, result.Steps, result.Delegations, result.Usage.In, result.Usage.Out,
			result.CostUSD, (time.Duration(result.DurationMS) * time.Millisecond).Round(time.Millisecond),
			mdCell(result.Error))
	}
	return b.String()
}

func safeRelPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("empty file path")
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("absolute path not allowed: %s", path)
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes task workspace: %s", path)
	}
	return nil
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "task"
	}
	return b.String()
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…[truncated]"
}

func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func failureClass(err string) string {
	switch {
	case strings.HasPrefix(err, "agent:"):
		return "agent"
	case strings.HasPrefix(err, "check:"), strings.HasPrefix(err, "restore protected"):
		return "check"
	case strings.HasPrefix(err, "setup:"):
		return "setup"
	case strings.HasPrefix(err, "prepare:"):
		return "prepare"
	case strings.HasPrefix(err, "session:"):
		return "session"
	case strings.HasPrefix(err, "orchestrator:"):
		return "orchestrator"
	case err == "":
		return "none"
	default:
		return "other"
	}
}

func wilson(passed, total int) (float64, float64) {
	if total <= 0 {
		return 0, 0
	}
	const z = 1.959963984540054
	n := float64(total)
	p := float64(passed) / n
	denom := 1 + z*z/n
	center := (p + z*z/(2*n)) / denom
	margin := z * math.Sqrt(p*(1-p)/n+z*z/(4*n*n)) / denom
	low := center - margin
	high := center + margin
	if low < 0 {
		low = 0
	}
	if high > 1 {
		high = 1
	}
	return low, high
}

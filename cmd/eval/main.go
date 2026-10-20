package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/cli"
	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/eval"
	"github.com/ramsterr/Wombat/internal/tokens"
	"github.com/ramsterr/Wombat/internal/tools"
)

func main() {
	var (
		tasksDir       = flag.String("tasks", "eval/tasks", "directory of task JSON files")
		outDir         = flag.String("out", "eval/results", "directory for report.json and report.md")
		providerName   = flag.String("provider", "", "provider override")
		model          = flag.String("model", "", "model override")
		maxSteps       = flag.Int("max-steps", 64, "maximum agent steps per task")
		orchestrator   = flag.Bool("orchestrator", true, "enable lead/worker orchestration")
		timeout        = flag.Duration("timeout", 10*time.Minute, "per-task agent timeout")
		commandTimeout = flag.Duration("command-timeout", 2*time.Minute, "per setup/check command timeout")
		dryRun         = flag.Bool("dry-run", false, "validate tasks and print the plan without calling a model")
		keep           = flag.Bool("keep", false, "keep each task workdir")
		filter         = flag.String("task", "", "only run task IDs containing this string")
		priceIn        = flag.Float64("price-in", 0, "USD per 1M input tokens")
		priceCached    = flag.Float64("price-cached", 0, "USD per 1M cached input tokens")
		priceOut       = flag.Float64("price-out", 0, "USD per 1M output tokens")
	)
	flag.Parse()

	tasks, err := eval.LoadTasks(*tasksDir)
	if err != nil {
		fatal(err)
	}
	if *filter != "" {
		var selected []eval.Task
		for _, task := range tasks {
			if strings.Contains(task.ID, *filter) {
				selected = append(selected, task)
			}
		}
		tasks = selected
	}
	if len(tasks) == 0 {
		fatal(fmt.Errorf("no tasks found in %s", *tasksDir))
	}
	if *dryRun {
		fmt.Printf("%d task(s) in %s:\n", len(tasks), *tasksDir)
		for _, task := range tasks {
			fmt.Printf("  %-28s %s\n", task.ID, task.Name)
			for _, check := range task.Check {
				fmt.Printf("    check: %s\n", check)
			}
		}
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	provider, resolvedProvider, resolvedModel, err := cli.BuildProvider(ctx, cfg, *providerName, *model)
	cancel()
	if err != nil {
		fatal(err)
	}

	pricing := agent.Pricing{
		InputPerMTok:  *priceIn,
		CachedPerMTok: *priceCached,
		OutputPerMTok: *priceOut,
	}
	home, err := os.MkdirTemp("", "wombat-eval-home-")
	if err != nil {
		fatal(err)
	}
	defer os.RemoveAll(home)

	opts := eval.Options{
		Provider: provider,
		Model:    resolvedModel,
		Tools:    tools.Registry(),
		Env: tools.Env{
			Permit: func(kind, detail string) tools.Decision {
				if kind == "outside" {
					return tools.Decision{}
				}
				return tools.Decision{Allow: true}
			},
			DenyDangerous: true,
			DenyNetwork:   true,
		},
		MaxSteps:       *maxSteps,
		Orchestrator:   *orchestrator,
		ContextWindow:  tokens.Window(resolvedModel),
		Timeout:        *timeout,
		CommandTimeout: *commandTimeout,
		Home:           home,
		KeepWorkdir:    *keep,
		Pricing:        pricing,
		OnTaskStart: func(task eval.Task) {
			fmt.Fprintf(os.Stderr, "running %s …\n", task.ID)
		},
	}

	results := make([]eval.Result, 0, len(tasks))
	for _, task := range tasks {
		result := eval.RunTask(context.Background(), task, opts)
		results = append(results, result)
		status := "FAIL"
		if result.Passed {
			status = "PASS"
		}
		fmt.Fprintf(os.Stderr, "  %s %s (%d steps, %d in / %d out, %s)\n",
			status, result.TaskID, result.Steps, result.Usage.In, result.Usage.Out,
			(time.Duration(result.DurationMS) * time.Millisecond).Round(time.Millisecond))
		if result.Workdir != "" {
			fmt.Fprintf(os.Stderr, "  workdir: %s\n", result.Workdir)
		}
		if result.Error != "" {
			fmt.Fprintf(os.Stderr, "  error: %s\n", result.Error)
		}
	}
	report := eval.Summarize(results, resolvedProvider+"/"+resolvedModel, pricing)
	if err := eval.WriteReport(*outDir, report); err != nil {
		fatal(err)
	}
	fmt.Printf("\n%d/%d passed (%.1f%%) · in %d (cached %d) out %d · $%.4f · %s\n",
		report.Passed, report.Tasks, 100*report.PassRate,
		report.Usage.In, report.Usage.Cached, report.Usage.Out, report.CostUSD,
		(time.Duration(report.DurationMS) * time.Millisecond).Round(time.Millisecond))
	fmt.Printf("wrote %s/report.json and %s/report.md\n", *outDir, *outDir)
	if report.Passed != report.Tasks {
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

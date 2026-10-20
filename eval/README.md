# Wombat evals

Eval harness. It runs a task in a fresh workspace, invokes the real agent loop, runs the task's checks, and writes a reproducible report with pass rate, tokens, latency, delegation count, and estimated cost.

## Quick start

```sh
# Validate the suite without spending tokens.
go run ./cmd/eval -tasks eval/tasks -dry-run

# Run the suite three times per task (variance / confidence interval).
go run ./cmd/eval -tasks eval/tasks -repeats 3 -out eval/results

# Run the suite with the configured provider/model.
go run ./cmd/eval -tasks eval/tasks -out eval/results

# Compare against the single-agent loop.
go run ./cmd/eval -tasks eval/tasks -orchestrator=false -out eval/results/single-agent

# Explicit provider, model, and pricing (USD per 1M tokens).
go run ./cmd/eval \
  -provider anthropic -model claude-sonnet-4-5 \
  -price-in 3 -price-cached 0.3 -price-out 15 \
  -out eval/results
```

Results are written to `eval/results/report.json` and `eval/results/report.md`. The command exits non-zero if any task fails, so CI can gate on it.

## Task format

Tasks are JSON files in `eval/tasks`. Each task is self-contained:

```json
{
  "id": "go-off-by-one",
  "name": "Fix off-by-one in Sum",
  "prompt": "Fix the failing test and run go test ./...",
  "files": {
    "go.mod": "module fixture\n\ngo 1.26\n",
    "sum.go": "package fixture\n"
  },
  "check": ["go test ./..."],
  "timeout_seconds": 300,
  "command_timeout_seconds": 120,
  "tags": ["go", "bugfix"]
}
```

- `files` writes the initial fixture; paths must stay inside the task workspace.
- `fixture` is an alternative to `files`: a directory copied into the workspace.
- `setup` commands run before the agent; `check` commands run after it. The task passes only if the agent and every check command succeed.
- `timeout_seconds` bounds the agent; `command_timeout_seconds` bounds setup/check commands.
- Test files (`*_test.go`, `*_test.py`, `*.test.js`, …), `testdata/`, `tests/`, `spec/`, `.github/`, and dependency manifests are snapshotted after setup and restored after the agent runs. This prevents an agent from weakening checks. Set `"allow_test_edits": true` only for a task where editing tests is the point.

## Baseline and comparisons

Run the suite once against the current single-agent harness and keep the report:

```sh
go run ./cmd/eval -tasks eval/tasks -orchestrator=false -out eval/baseline/single-agent
go run ./cmd/eval -tasks eval/tasks -orchestrator=true -out eval/baseline/orchestrated
```

Record the model, provider, date, pass rate, confidence interval, tokens, cost, and delegation count. Every later harness or delegation change should be compared against these reports. The suite has 20 focused Go fixtures covering bug fixes, edge cases, string/slice handling, and test-gated behavior. Add multi-file and test-repair fixtures as the harness grows.

Reports include a Wilson 95% confidence interval and failure classes (`agent`, `check`, `setup`, `prepare`, `session`, `orchestrator`). Use `-repeats N` for noisy models and compare per-task pass rates, not just the aggregate.

## Budget controls

- Use `-dry-run` in CI when no model call is needed.
- Use `-task <substring>` to run one task while iterating.
- Set a hard per-task timeout and keep the suite small.
- Pass pricing flags to see cost per solved task; leave them at 0 to report tokens only.
- Orchestration is on by default; use `-orchestrator=false` for the single-agent baseline.
- Worker fleets have hard per-worker and per-turn token caps (`worker_token_budget`, `turn_worker_token_budget`); lower them when measuring orchestration overhead.
- The harness denies network commands and dangerous shell commands by default. Workers are read-only and inherit the same workspace.
- Protected files are restored before checks, so a passing report cannot be produced by deleting or weakening tests.
- Writer tests require a clean git repository; the eval harness currently exercises the lead and read-only scout paths.

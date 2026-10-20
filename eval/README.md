# Wombat evals

Eval harness. It runs a task in a fresh workspace, invokes the real agent loop, runs the task's checks, and writes a reproducible report with pass rate, tokens, latency, delegation count, and estimated cost.

## Quick start

```sh
# Validate the suite without spending tokens.
go run ./cmd/eval -tasks eval/tasks -dry-run

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

## Phase 0 baseline

Run the suite once against the current single-agent harness and keep the report:

```sh
go run ./cmd/eval -tasks eval/tasks -out eval/baseline
```

Record the model, provider, date, pass rate, tokens, cost, and delegation count. Every later harness or delegation change should be compared against this report; use `-orchestrator=false` to isolate the single-agent loop. The seed suite is intentionally small; grow it toward 20–30 tasks covering bug fixes, multi-file changes, test repair, and refactors before drawing conclusions.

## Budget controls

- Use `-dry-run` in CI when no model call is needed.
- Use `-task <substring>` to run one task while iterating.
- Set a hard per-task timeout and keep the suite small.
- Pass pricing flags to see cost per solved task; leave them at 0 to report tokens only.
- Orchestration is on by default; use `-orchestrator=false` for the single-agent baseline.
- The harness denies network commands and dangerous shell commands by default. Workers are read-only and inherit the same workspace.

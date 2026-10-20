# Wombat Guardrails — deterministic change plan

Purpose: make the failure classes below structurally impossible, not discourage them with prose.
Each item names the file, the exact behavior change, the config surface, and the test that proves it.

## Invariants

1. The lead edits. Workers only scout. Delegation is for read-only, independent investigation that would flood lead context — never for the artifact itself.
2. Every delegation states why it exists and has a stop condition. Two failed workers disable delegation for the rest of the turn.
3. No package, model, or CLI install happens without an explicit `install` permission.
4. A check runs in the directory where the artifact lives. Completing a step runs its checks; a pending check never rejects the whole plan.
5. Visual claims require actual vision input. Without it, the agent says it is blind, runs deterministic checks, and opens the artifact for the user.
6. Checkpoints are bounded, text-first, deduplicated, and garbage-collected.
7. "Fast" is a hard budget: delegation off, step cap low, wall-clock stop, ship the artifact.

## P0 — ship first

### 1. Replace the delegation instruction

File: `internal/agent/prompt.go`, replace the `Work in parallel` block (currently lines 24–25).

New text:

```
Delegation gate — all must be true:
- the work is read-only investigation, not editing or producing the deliverable
- it is independent of what you are doing now
- reading it yourself would flood context (roughly >5 files or >500 lines)
- you can name the exact deliverable and the stop condition

If any condition is false, do it yourself. Never delegate the final artifact, an edit, or a
task with fewer than three lookups. Never delegate the same question twice. After two
workers fail or return partial, stop delegating this turn and finish the work directly.
```

Add three rules to the same prompt:

```
Artifact first: for any visual, audio, or file deliverable, produce a working draft before
broad research. Reference gathering is capped at one third of the turn.
Vision honesty: only claim you inspected an image if the model actually received it. If it
did not, say "I cannot see the render", run measurable checks, and open the file for the user.
Dependency rule: never install a package, model, or CLI unless it is required to run or
verify the deliverable. State the size and ask first.
```

File: `internal/agent/prompt_test.go`
- Replace the `"2-5 workers"` assertion with `"Delegation gate"`, `"fewer than three lookups"`, `"Vision honesty"`, `"Dependency rule"`.
- Keep the 4000-character limit; trim elsewhere if needed.

### 2. Cap worker context and preserve partial reports

Files: `internal/tools/tools.go`, `internal/tools/files.go`, `internal/agent/agent.go`, `internal/orchestrator/delegate.go`.

- Add to `tools.Env`: `NoMedia bool`, `MaxToolOutput int`.
- `readImage`: when `env.NoMedia` is true, return `"image not attached: read-only worker"` without calling `AddMedia`.
- `execTool` in `internal/agent/agent.go`: truncate to `env.MaxToolOutput` when set; keep 64KB as the lead default.
- `runWorker`: set `env.NoMedia = true` and `env.MaxToolOutput = 16 << 10`.
- `workerSystemPrompt`: add `"Do not read images or binary files. If the task needs visual inspection, return status=blocked."`
- `runWorker` budget/timeout branch: when `report.Findings` is non-empty, set `report.Status = "partial"` and keep the findings. Only use `failed` when there is no usable report.

Tests: `internal/tools/files_test.go` (no media when `NoMedia`), `internal/orchestrator/fleet_test.go` (partial report survives budget stop).

### 3. Require a delegation reason and add a circuit breaker

Files: `internal/orchestrator/tools.go`, `internal/orchestrator/report.go`, `internal/orchestrator/delegate.go`, `internal/orchestrator/orchestrator.go`.

- Add `Reason string \`json:"reason"\`` to `delegateArgs`, schema enum:
  `context_flood`, `independent_scan`, `parallel_compare`, `verification`.
- `validateDelegationArgs`: reject a missing reason.
- `prepareDelegations`: reject when `len(strings.TrimSpace(a.Objective))+len(strings.TrimSpace(a.Context)) < 300` with `"task is too small to delegate; do it directly"`.
- Add `workerFailures int` to `Orchestrator`; increment for each outcome whose status is `failed` or `blocked`.
- At the top of `prepareDelegations`: if `o.workerFailures >= 2`, return `"delegation disabled for this turn after 2 worker failures; continue directly"`.
- Reset `workerFailures` at turn start, next to `delegations`.

Tests: `fleet_test.go` covers missing reason, tiny task rejection, and the third delegation being refused after two failures.

### 4. Run checks in the artifact directory

Files: `internal/orchestrator/ledger.go`, `internal/orchestrator/tools.go`, `internal/orchestrator/checks.go`.

- Add `Workdir string \`json:"workdir,omitempty"\`` to `Check`.
- `update_plan` schema and merge: accept `workdir`; preserve it in `mergeChecks`.
- `runChecks`: for each check, if `Workdir` is set, resolve it against `env.Dir`, reject paths outside the workspace, and run that check with a copied `env` whose `Dir` is the resolved directory.
- Add `workdir` to `run_checks` arguments as an override for one invocation.
- Document in the tool description: "Set workdir for projects below the workspace root; checks with relative paths and no workdir run at the workspace root."

Tests: `internal/orchestrator/orchestrator_test.go` — a check with `workdir: "sub/project"` finds a file that does not exist at the root.

### 5. Run pending checks instead of rejecting the plan

File: `internal/orchestrator/orchestrator.go`, `updatePlan` (completion gate at lines 322–326).

- Remove the hard `completionGate` error path.
- Before saving, for every step that is `completed` and has a check that is not `passed`, execute those checks synchronously through the same code path as `runChecks`.
- If all pass: keep `completed`, store evidence.
- If any fail: set the step to `in_progress`, store the failure output, and return the normal plan summary plus a `checks_failed` warning. Do not reject the update.
- Keep `completionGate` as a read-only validator used before final answer, not before every plan save.

Tests: `orchestrator_test.go` — a plan marking a step completed with a failing check returns success, downgrades the step, and records the output; a passing check leaves it completed.

### 6. Bound checkpoints

Files: `internal/checkpoint/checkpoint.go`, `internal/cli/runner.go`, `internal/tui/chat.go`, `internal/config/config.go`.

- Extend `skipDir` with `.next`, `.turbo`, `coverage`, `.pytest_cache`, `.mypy_cache`, `tmp`.
- Add `skipExt` for binary/media: `.png .jpg .jpeg .gif .webp .bmp .tif .tiff .heic .mp4 .mov .webm .mp3 .wav .flac .zip .tar .gz .7z .pdf .sqlite .db .onnx .safetensors .bin .pt .pth`.
- `maxFileBytes = 2 << 20`, `maxTotalBytes = 32 << 20`.
- When `maxTotalBytes` would be exceeded, append the file to `manifest.Skipped` and continue instead of returning an error.
- Dedupe: keep a map of `sha256 -> first snapshot path`; if an identical file already exists in the checkpoint root, hardlink it (`os.Link`) and fall back to copy on error. Add a small `sameContent` helper.
- After each successful snapshot, keep the newest 20 snapshot directories per session and remove older ones.
- Preflight in `runner.go` and `tui/chat.go`: if free space on the checkpoint volume is below 2GB, disable checkpoints for the run and print one warning.
- Add config keys: `checkpoint_max_file_bytes`, `checkpoint_max_total_bytes`, `checkpoint_keep` (defaults 2MB, 32MB, 20).

Tests: `internal/checkpoint/checkpoint_test.go` — a 10MB PNG is skipped without error; a second identical file does not add bytes; pruning leaves 20 snapshots.

### 7. Gate installs

Files: `internal/tools/policy.go` (new `MayInstall`), `internal/tools/bash.go`, `internal/config/config.go`, `internal/agent/prompt.go`.

- Add `MayInstall(command string) bool` matching `pip install`, `python -m pip install`, `uv add|pip install`, `npm|pnpm|yarn install|add`, `brew install`, `go install`, `cargo install`, `apt|apt-get install`, `curl ... | sh`.
- In `bash.go`, before the snapshot: if `MayInstall`, call `env.permit("install", command)`.
- Default permissions add `"install": "ask"`; expose it in `wombat config set permissions.install ask|allow|deny`.
- Prompt: the dependency rule from item 1.

Tests: `internal/tools/policy_test.go` — each installer matches; `bash_test.go` — install is blocked without approval and allowed with `permissions.install=allow`.

## P1 — next

### 8. Fast mode

Files: `internal/config/config.go`, `internal/cli/runner.go`, `internal/cli/cli.go`.

- Add `--fast` to headless run and TUI startup. It sets, for the run only:
  `Orchestrator=false`, `MaxDelegations=0`, `MaxSteps=24`, `TurnBudgetSeconds=300`.
- Add `TurnBudgetSeconds int` to config; default 0 (off).
- Agent loop: at 80% of the wall-clock budget, append a system note: `"Budget nearly exhausted: ship the current artifact, run one check, and report."`
- Add `wombat config set fast true` as a persisted default.

Tests: `internal/agent/agent_test.go` — the note is injected once; `internal/config/config_test.go` — fast preset values.

### 9. Vision capability flag

Files: `internal/config/config.go`, `internal/agent/prompt.go`, `internal/llm/attach.go`, `internal/agent/agent.go`.

- Add `Vision bool` to `Provider` (default false; set true for known vision models in presets).
- In `buildSystem`, add a `You cannot see images in this session` paragraph when `Vision` is false; add the inverse sentence when true.
- When vision is true, downscale attached images to a 1568px longest edge, JPEG quality 85, and cap at 2 attachments per model turn.
- When vision is false, `readImage` returns metadata and a note instead of `AddMedia`, even for the lead.

Tests: `internal/agent/prompt_test.go` (both branches), `internal/llm/attach_test.go` (downscale and cap).

### 10. Observability and doctor

Files: `internal/trace`, `internal/orchestrator`, `internal/cli`.

- Emit structured events: `delegation_attempt` (reason, task count, budget), `delegation_outcome` (status, tokens, steps), `worker_budget_exceeded`, `checkpoint_skip` (path, reason, bytes), `install_attempt` (allowed/denied), `checks_autorun`.
- `wombat doctor` prints: free disk, checkpoint directory size, media files in workspace, `Vision`, delegation defaults, install permission.
- `wombat stats --last` prints: tool calls, workers started/failed, tokens by worker, checkpoint bytes, installs blocked.

Tests: one trace assertion per event; doctor smoke test.

## Config defaults after P0

| Key | Old | New |
| --- | --- | --- |
| `worker_max_steps` | 16 | 12 |
| `worker_token_budget` | 50,000 | 50,000 |
| `turn_worker_token_budget` | 260,000 | 160,000 |
| `max_delegations` | 6 | 4 |
| `permissions.install` | — | `ask` |
| `checkpoint_max_file_bytes` | 16MB | 2MB |
| `checkpoint_max_total_bytes` | 128MB | 32MB |
| `checkpoint_keep` | — | 20 |

## Rollout order

1. Prompt + tests (no runtime risk).
2. Checkpoint bounds + disk preflight (prevents outage).
3. Check `workdir` + auto-run (fixes false failures and plan churn).
4. Worker caps + partial reports + circuit breaker.
5. Install gate.
6. Fast mode, vision flag, observability.

One commit per numbered item, each with its tests. Roll back a single item by reverting its commit; no cross-item flags required.

## Definition of done

- No delegation without a reason; no task under 300 characters; no third delegation after two failures.
- No install without an explicit `install` permission.
- No checkpoint larger than 32MB and no media files inside one.
- No completed step whose checks were not executed; no plan update rejected for a pending check.
- No visual claim without vision input; the artifact is opened for the user.
- Fast mode finishes inside its step and time budgets.

Verification command:

```sh
go test ./internal/agent ./internal/orchestrator ./internal/checkpoint ./internal/config ./internal/tools
```

# Wombat efficiency work

> Branch: `efficienty`. No changes were made on `main`.

This page records Phase 0 (prompt-size telemetry) and Phase 1 (search
discipline) from the Pi vs Wombat benchmark. The goal is to reduce Wombat's
time and token usage without losing task quality.

## Phase 0: where the base prompt goes

The agent now emits a `prompt` trace event before every model call and a
`model_usage` event after it. The `prompt` event breaks the request into
system prompt, tool schemas, skill/team extra, AGENTS.md, task, tool results,
and history. The `model_usage` event records the provider-reported input and
cached tokens for that step.

Six high-ratio tasks were rerun with the instrumented binary (the Phase 1
guard is included, but it does not change the base prompt). First-step
numbers:

| Task | First-step input | Tool schemas (est.) | System (est.) | Extra/team (est.) | AGENTS.md (est.) | History (est.) | Tools |
|---|---:|---:|---:|---:|---:|---:|---:|
| `mcp_tool` | 8,453 | 9,967 | 2,359 | 1,184 | 267 | 337 | 38 |
| `git_commit` | 8,760 | 10,389 | 2,440 | 1,263 | 267 | 333 | 39 |
| `code_review` | 8,448 | 9,967 | 2,360 | 1,184 | 267 | 337 | 38 |
| `skill_adherence` | 8,428 | 9,967 | 2,361 | 1,184 | 267 | 305 | 38 |
| `cli_json` | 8,460 | 9,967 | 2,359 | 1,184 | 267 | 348 | 38 |
| `multi_file_refactor` | 8,468 | 9,967 | 2,363 | 1,184 | 267 | 360 | 38 |
| **Average** | **8,503** | **10,037** | **2,374** | **1,197** | **267** | **337** | **38** |

The token estimator overcounts relative to the provider, so the raw estimates
sum to more than the reported first-step input. Normalizing proportionally to
the provider input gives the useful shares:

| Component | Share of first-step prompt |
|---|---:|
| Tool schemas | **~71%** |
| System prompt | ~17% |
| Team/orchestration extra | ~8% |
| AGENTS.md | ~2% |
| History/task | ~2% |

**The dominant cost is the tool registry, not the system prompt, skill, or
tool output.** Wombat exposes 38-39 tools in these runs. Pi exposes 4 built-in
tools plus MCP. That matches the ~2× base-prompt difference.

Two nuances:

- `skill_tokens` was 0 because the lead did the task itself and read
  `.wombat/skills/bench.md` as a file. The custom-agent persona would add the
  skill only when the planner delegates to that agent.
- Tool results were small on these tasks (tens to ~1.5 KB), so they are not
  the source of the token gap. The gap is present before the first tool call.

## Phase 1: search discipline

### Changes

- Added `checkSearchScope` in `internal/tools/searchscope.go`. It rejects
  `find`, `grep`, `rg`, `fd`, `ls`, `du`, `tree`, `mdfind`, and `locate` when
  the path is `/`, `~`, `$HOME`, a parent directory, or any absolute path
  outside the workspace.
- Added a 30s default timeout for foreground bash commands. Background jobs
  keep their existing behavior.
- Updated the `bash` tool description/schema and the system prompt to say:
  search only inside the workspace; use `glob`/`grep`; never scan `/`, `~`, or
  `$HOME`.
- Added tests:
  - `TestCheckSearchScope` — blocked and allowed commands.
  - `TestSearchScopeErrorIsActionable` — the error includes a focused-path hint.
  - `TestBashDefaultTimeout` — a foreground `sleep` is stopped by the default
    timeout.

All `go test ./...` packages pass.

### Measured effect

The previous `skill_adherence` outlier was 240.2s, with 229s spent in:

```sh
find / -iname "*bench*" -type f 2>/dev/null | ...
```

That command is now rejected before it runs. In the rerun, the model used
`ls -la; find . -maxdepth 3` instead and completed in 34.9s. The guard removes
the 229s class of failure; the remaining 34.9s is model latency and 6 steps
versus Pi's 4.

Six-task comparison:

| Task | Before input | After input | Before time | After time | Steps |
|---|---:|---:|---:|---:|---:|
| `mcp_tool` | 25,523 | 25,634 | 4.2s | 6.2s | 3 |
| `git_commit` | 36,356 | 36,326 | 6.8s | 6.2s | 4 |
| `code_review` | 44,336 | 45,362 | 8.3s | 9.5s | 5 |
| `skill_adherence` | 77,431 | 53,856 | **240.2s** | **34.9s** | 7 → 6 |
| `cli_json` | 43,687 | 43,670 | 6.3s | 6.8s | 5 |
| `multi_file_refactor` | 77,244 | 46,315 | 15.5s | 13.8s | 8 → 5 |

The `multi_file_refactor` drop is mostly model variance and two fewer steps,
not the search guard. The robust Phase 1 result is the search guard test plus
the elimination of the 229s root scan.

Excluding `skill_adherence`, the five-task total is **42.6s** for Wombat vs
**43.5s** for Pi. The remaining time gap is concentrated in the skill task's
extra steps and model latency, not in shell execution.

## Full-suite regression

The full 18-task Wombat suite was rerun with the Phase 1 binary:

| Metric | Previous Wombat | Phase 1 Wombat | Pi baseline |
|---|---:|---:|---:|
| Tasks passed | 18/18 | **18/18** | 18/18 |
| Total wall time | 385.6s | **153.1s** | 270.2s |
| Median time | 8.5s | **7.3s** | 8.0s |
| Max task time | 240.2s | **23.2s** | 128.9s |
| Input tokens | 874,439 | 798,480 | 416,257 |
| Uncached input | 255,431 | 274,512 | 131,201 |
| Output tokens | 9,435 | 10,085 | 9,574 |
| Tool calls | 80 | 73 | 76 |
| Model steps | 96 | 88 | 91 |

Phase 1 met the time target in this run: **153s total, 7.3s median**, and
18/18 passed. The previous 240s skill outlier is gone. This is a single run,
so the token and output numbers move with model variance; the time result is
mostly the removal of the 229s root scan.

The token target is **not** met yet: input is still **1.92× Pi** and uncached
input is **2.09× Pi**. That is expected because Phase 1 did not touch the
38-tool schema payload.

## Phase 2: capability profiles

Implemented:

- Default `coding` profile: `read`, `write`, `edit`, `bash`, `glob`, `grep`,
  `apply_patch`, plus `tool_search` and `request_capability`.
- Optional capabilities: `web`, `semantic`, `verify`, `media`.
- `tool_search` returns compact capability matches; `request_capability` loads
  the chosen capability once and keeps it for the session.
- Config and CLI: `tool_profile`, `capabilities.<name>`, `--profile`, and
  `/profile` in chat.
- `imagegen` is excluded from all profiles. Git tools are removed; use `bash git`. Verification uses `bash` by default. Semantic tools are opt-in.
- Orchestration tools are attached only when the orchestrator is enabled and
  the profile includes `orchestrate`; `minimal` never includes them.

### Full-suite results

| Run | Pass | Input | Uncached | Output | Tools | Steps | Total | Median |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Pi baseline | 18/18 | 416,257 | 131,201 | 9,574 | 76 | 91 | 270.2s | 8.0s |
| Wombat auto + orchestrator | 18/18 | **554,656** | **210,400** | 10,635 | 88 | 100 | **168.2s** | 8.7s |
| Wombat minimal + no orchestrator | 18/18 | **244,613** | **130,885** | 8,509 | 69 | 82 | **133.6s** | **6.7s** |

The lean run is the target result: **41% less total input than Pi, uncached
input effectively tied (130.9k vs 131.2k), and lower output, tools, steps, and
time — with 18/18 passing.**

The default auto+orchestrator run is 1.33× Pi on total input and 1.60× on
uncached input. The remaining gap is the orchestration tools (~2.6k schema
tokens) plus model variance. Orchestration is now optional; `minimal` and
`orchestrator: false` give the lean path.

### Model-request path

A separate probe used `--profile minimal` with a browser task:

```text
Initial tools: 8
Initial schema: 1,219 tokens
First-step input: 1,877 tokens

Model: request_capability("web", "need to inspect the page")
→ loaded browser, webfetch, websearch
Next request: 11 tools, 2,690 schema tokens
```

The model requested the capability in one step and used the browser; no search
loop. `tool_search` remains available when the model does not know which
capability it needs.

## Tool dedup

The registry went from **34 tools to 18** (19 including `question`). The default
`coding` profile is now **9 tools**:

```text
read, write, edit, bash, glob, grep,
tool_search, request_capability, apply_patch
```

Removed from exposure:

```text
multi_edit, replace, rename, refactor, symbols,
semantic_search, git_status, git_diff, git_log,
git_show, git_blame, imagegen, verify_site, batch, jobs
```

Kept optional:

```text
lsp, browser, websearch, webfetch, request_more_research,
verify, imagesearch, job_output, job_kill
```

The self-contained duplicate implementations were deleted. `rename`,
`refactor`, and the `jobs` list were removed from the registry and their tests
were removed; their implementations remain for one release as a soft cut.

`TestRegistryDedup` locks the 18-tool registry and the 9-tool coding profile.

## Phase 3: bounded research and clarifying questions

Implemented:

- **Research modes:** `auto`, `off`, `quick`, `standard`, `deep`.
- **Soft budget + hard ceiling:** quick is 2/1 soft (4/2 hard), standard 5/3
  (8/4), deep 10/6 (15/8).
- **`request_more_research`:** one small extension with a required reason,
  capped by the hard ceiling.
- **Answer-first guidance:** recommendations and opinions should use knowledge
  first; web is for current facts or explicit verification.
- **Fast depth by default:** `websearch` uses the policy depth unless the model
  explicitly asks for `deep`.
- **Clarify policy:** vague prompts ask one question with 2-3 options, one
  round only; headless runs state assumptions instead of asking.
- **Question budget:** at most 2 question calls per run.

### Exact job prompt

The same prompt that produced 13 searches and 4 fetches before:

| | Old Wombat | `efficienty` before budget | `efficienty` with budget | Pi |
|---|---:|---:|---:|---:|
| Steps | 14 | 9 | **6** | 1 |
| Web calls | 17 | 7 | **3** | 0 |
| Input tokens | 345,936 | 80,300 | **42,600** | 3,410 |
| Uncached | 103,440 | 26,400 | **13,700** | 3,090 |
| Output | 5,794 | 2,000 | **1,500** | 2,241 |
| Time | 114.6s | 65.4s | **21.1s** | ~20s |

The budget run used 2 searches and 1 fetch; the second fetch was blocked with
“research fetch soft budget reached (1/1); synthesize now.” The model then
answered. It matched Pi’s time, but still used more tokens because it did live
verification and carried tool results. That is the intended trade-off: quick
research is a middle ground between Pi’s zero-research answer and the old
13-search loop.

For Pi-level token usage, use `research_mode=off` or a recommendation-only
prompt; for research-heavy tasks, use `standard` or `deep`.

## What this means

- **Time:** the worst-case search behavior is fixed. The lean run is 133.6s vs
  Pi's 270.2s, with a 6.7s median vs 8.0s. The exact job prompt dropped from
  114.6s to 21.1s.
- **Tokens:** the capability profile is the fix. The lean run is below Pi on
  total input and tied on uncached input. The default auto+orchestrator run is
  still 1.33×/1.60× because orchestration tools are loaded by default.
- **Research:** the budget is adaptive, not a fixed hard cap. Quick/standard/
  deep modes plus a hard ceiling prevent the 13-search loop without blocking
  genuine research.
- **Clarify:** vague prompts get one bounded question round; headless runs
  never ask and state assumptions.
- **Next lever:** make orchestration lazy, or default it off for simple tasks.

## Raw data

- [efficiency-baseline.json](efficiency-baseline.json) — the six-task rerun
  with first-step prompt estimates.

## Files changed on `efficienty`

- `internal/agent/agent.go` — prompt/model_usage trace events, ToolNote,
  pending capability tools.
- `internal/agent/capability_test.go` — mid-run capability load test.
- `internal/agent/promptstats.go` — prompt breakdown and tool-schema sizing.
- `internal/agent/promptstats_test.go` — trace event test.
- `internal/agent/prompt.go` — workspace-only search guidance.
- `internal/tools/bash.go` — default 30s foreground timeout and guard call.
- `internal/tools/bash_test.go` — default-timeout test.
- `internal/tools/searchscope.go` — root/outside search guard.
- `internal/tools/searchscope_test.go` — guard tests.
- `internal/tools/profiles.go` — capabilities, profiles, filter, index, search.
- `internal/tools/profiles_test.go` — profile/filter/search tests.
- `internal/tools/capability.go` — request_capability and pending tools.
- `internal/tools/toolsearch.go` — tool_search.
- `internal/tools/research.go` — research modes, budget, extension tool.
- `internal/tools/research_test.go` — policy/budget/question tests.
- `internal/tools/websearch.go`, `web.go` — enforce the research budget.
- `internal/tools/question.go` — question budget.
- `internal/agent/clarify.go` — vague-prompt heuristic and clarify note.
- `internal/agent/clarify_test.go` — clarify tests.
- `internal/config/config.go` — tool_profile, research_mode, capabilities.
- `internal/cli/runner.go` — profile, research budget, notes, question budget.
- `internal/cli/run.go` — `--profile` and `--research`.
- `internal/cli/repl.go` — `--profile`, `--research`, `/profile`, `/research`.
- `internal/cli/configcmd.go` — `tool_profile`, `research_mode`, `capabilities.*`.
- `eval/benchmarks/pi-vs-wombat/EFFICIENCY.md` — this report.

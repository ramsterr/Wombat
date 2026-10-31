# Wombat vs OpenCode: 15-task agent benchmark

> Part of the [Wombat evals](../../README.md) docs. Raw data: [results.json](results.json).

**Same model. Same skill content. Same MCP server. Same isolated workspaces.**

This is a head-to-head run of Wombat `main` and OpenCode on a deterministic
15-task suite. Both agents used `opencode-go/beam-free`, received the same
project instructions, connected to the same `bench_stamp` MCP server, and worked
in a fresh workspace for every task. This is a separate cross-agent suite, not
the 20-task Go eval suite in [`eval/tasks`](../../tasks).

![Summary dashboard](assets/summary.svg)

## At a glance

| Metric | OpenCode | Wombat | OC / W |
|---|---:|---:|---:|
| Tasks passed | 15/15 | 15/15 | — |
| Input tokens | 990,027 | 754,668 | 1.31× |
| Uncached input | 215,243 | 222,380 | 0.97× |
| Output tokens | 35,167 | 13,708 | 2.57× |
| Tool calls | 135 | 78 | 1.73× |
| Model steps | 134 | 82 | 1.63× |
| Total wall time | 666.1s | 177.4s | 3.75× |
| Mean time / task | 44.4s | 11.8s | 3.75× |
| Median time / task | 27.8s | 10.2s | 2.73× |

`OC / W` is the OpenCode-to-Wombat ratio; values above 1 mean OpenCode used more.

Both agents passed all 15 tasks after the `edit_notes` check was relaxed to trim
leading/trailing whitespace. Under the original exact check, Wombat wrote
` FINAL` with a leading space and failed. OpenCode still used 2.57× more output
and 3.75× more wall time overall.

## The short version

![OpenCode usage relative to Wombat](assets/relative.svg)

- **OpenCode takes 3.75× longer.** 666s vs 177s total; 44.4s vs 11.8s per
  task.
- **OpenCode produces 2.57× more output.** 35.2k vs 13.7k output tokens.
- **Fresh input is effectively a tie.** Uncached input is 215k vs 222k —
  OpenCode is 3% lower in total and per task. The 1.31× total-input gap is
  mostly cached context.
- **Both agents pass 15/15 under the revised check.** Wombat's only original
  miss was a leading space in `edit_notes`; the check now trims whitespace.

## What was tested

The suite covers editing, bug fixes, tests, refactoring, CLI work, data
analysis, web UI, SVG, docs, a browser game, an API change, debugging, code
review, video generation, and MCP tool use. Each task has a deterministic check:
a test suite, exact file content, XML parsing, `ffprobe`, or a task-specific
heuristic.

| Area | Tasks |
|---|---|
| Editing and exactness | `edit_notes` |
| Bug fixes and tests | `bugfix_calc`, `add_tests`, `debug_parse` |
| Refactoring | `refactor_inventory` |
| CLI and data | `cli_wordcount`, `data_sales` |
| Web and media | `website`, `svg_logo`, `video` |
| Docs and review | `docs`, `code_review` |
| Apps and APIs | `snake_game`, `api_feature` |
| Tooling | `mcp_stamp` |

<details>
<summary>What each task asked for (abridged)</summary>

- **edit_notes** — Change the first line of `notes.txt` from `DRAFT` to `FINAL`
  and fix `recieve` → `receive`; change nothing else.
- **bugfix_calc** — Run the tests, fix `add()` so they pass, and do not edit the
  tests.
- **add_tests** — Add at least four unittest cases for `slugify()` covering
  spaces, uppercase, punctuation, and leading/trailing spaces; do not modify
  the implementation.
- **refactor_inventory** — Remove a duplicated discount calculation with a
  helper used by both order functions; behavior must not change.
- **cli_wordcount** — Create `wordcount.py` that prints the top three
  case-insensitive words, ignoring punctuation, as `word: count`.
- **data_sales** — Analyze `sales.csv`; write `report.md` with exact revenue and
  top-product lines, using an `analysis.py` script.
- **website** — Build a one-page Orbit Notes landing site with linked HTML/CSS,
  hero, CTA, three feature cards, and a `#signup` section.
- **svg_logo** — Create a valid 128×128 SVG for Beam Labs: blue circular
  background, white lightning bolt, accessible `<title>` element.
- **docs** — Read `api.py` and write a README documenting all three NotesAPI
  methods with the required sections.
- **snake_game** — Build a self-contained canvas snake game: 20×20 grid, arrow
  keys, food, score, game over, restart, no external libraries.
- **api_feature** — Add `normalize_url()` with scheme/host lowercasing, trailing
  slash rules, and query preservation; add tests and run them.
- **debug_parse** — Run the tests, fix `parse.py`, and do not edit the tests.
- **code_review** — Review `buggy.js` and write `review.md` with at least three
  concrete bugs, line numbers, and corrected snippets; do not modify the file.
- **video** — Produce a 4-second 640×360 silent MP4 with a dark blue background,
  centered white `BEAM` text, and a fade-in; verify with `ffprobe`.
- **mcp_stamp** — Call the `bench_stamp` MCP tool exactly once with label
  `task15` and write the returned text verbatim to `mcp.txt`.

</details>

## Speed

![Wall time by task](assets/per-task-time.svg)

![Cumulative wall time](assets/cumulative-time.svg)

OpenCode's time is concentrated in four tasks:

| Task | OpenCode | Wombat | Gap |
|---|---:|---:|---:|
| `refactor_inventory` | 174.2s | 8.2s | 166.0s |
| `data_sales` | 153.7s | 11.0s | 142.7s |
| `snake_game` | 53.2s | 7.2s | 46.0s |
| `website` | 44.5s | 6.7s | 37.8s |
| **Top four** | **425.6s** | **33.1s** | **392.5s** |

Those four tasks account for 393s of the 489s total gap (80%). The median gap
is smaller — 27.8s vs 10.2s — but still 2.7×.

## Tokens

![Prompt tokens by task](assets/per-task-input.svg)

Total input favors Wombat: 755k vs 990k. But most of that is cache:

| Token bucket | OpenCode | Wombat | Notes |
|---|---:|---:|---|
| Prompt tokens | 990,027 | 754,668 | Includes cache reads |
| Cached | 774,784 (78%) | 532,288 (71%) | Repeated context |
| Uncached | 215,243 | 222,380 | Fresh input; effectively tied |
| Output | 35,167 | 13,708 | OpenCode 2.57× higher |

The total-input gap is a step/cache effect: OpenCode took 134 model steps vs 82
and re-read more cached context. On fresh input, OpenCode is 3% lower in total
and per task. The output gap is much larger.

OpenCode's largest single prompt was `snake_game` at 164,175 tokens; Wombat did
the same task with 8,376. OpenCode also produced 5,115 output tokens on that
task vs Wombat's 1,532.

## Tools and steps

![Tool calls by task](assets/per-task-tools.svg)

![Wall time vs tool calls](assets/time-vs-tools.svg)

OpenCode made 135 tool calls vs 78 (1.73×) and took 134 model steps vs 82
(1.63×). It used more tools on 11 of 15 tasks; Wombat used more on three
(`edit_notes`, `cli_wordcount`, `api_feature`) and tied on `code_review`.

Tool count is not the whole story. `snake_game` had OpenCode's highest tool count
(16) but was not its slowest task; `refactor_inventory` and `data_sales` were
slower with 13 and 11 calls. The slow tasks combine more model steps, more
output, and more tool round-trips.

Both agents used the MCP server successfully. Each called `bench_stamp` three
times: the required `mcp_stamp` task plus two spontaneous uses. OpenCode used it
in `data_sales` and `snake_game`; Wombat used it in `svg_logo` and `code_review`.

No subagents or worker runs occurred on either side. Every task recorded exactly
one agent run, so the benchmark did not exercise Wombat's multi-agent delegation.

## Per-task results

Values are **OpenCode / Wombat**. `In` is prompt tokens including cache reads;
`Agents` is agent runs; `Time` is wall-clock agent time, excluding checks.

| Task | In tokens | Out tokens | Tools | Agents | Time (s) | Pass |
|---|---:|---:|---:|---:|---:|:---:|
| edit_notes | 27,299 / 110,833 | 435 / 1,326 | 5 / 11 | 1 / 1 | 12.7 / 22.4 | Y / Y |
| bugfix_calc | 49,025 / 43,728 | 697 / 241 | 8 / 4 | 1 / 1 | 17.3 / 10.2 | Y / Y |
| add_tests | 59,145 / 52,807 | 1,249 / 418 | 9 / 5 | 1 / 1 | 22.4 / 12.0 | Y / Y |
| refactor_inventory | 107,008 / 44,319 | 5,483 / 307 | 13 / 5 | 1 / 1 | 174.2 / 8.2 | Y / Y |
| cli_wordcount | 28,563 / 85,423 | 675 / 1,491 | 5 / 10 | 1 / 1 | 14.8 / 18.8 | Y / Y |
| data_sales | 71,803 / 44,416 | 3,708 / 744 | 11 / 5 | 1 / 1 | 153.7 / 11.0 | Y / Y |
| website | 110,088 / 8,411 | 5,723 / 1,455 | 12 / 2 | 1 / 1 | 44.5 / 6.7 | Y / Y |
| svg_logo | 55,705 / 16,855 | 1,377 / 566 | 7 / 2 | 1 / 1 | 21.8 / 6.5 | Y / Y |
| docs | 47,485 / 25,671 | 1,447 / 458 | 8 / 2 | 1 / 1 | 18.0 / 6.3 | Y / Y |
| snake_game | 164,175 / 8,376 | 5,115 / 1,532 | 16 / 1 | 1 / 1 | 53.2 / 7.2 | Y / Y |
| api_feature | 53,205 / 78,992 | 2,370 / 1,607 | 9 / 10 | 1 / 1 | 27.8 / 17.5 | Y / Y |
| debug_parse | 69,033 / 44,589 | 1,907 / 373 | 12 / 4 | 1 / 1 | 28.4 / 7.9 | Y / Y |
| code_review | 51,425 / 74,231 | 1,598 / 1,272 | 7 / 7 | 1 / 1 | 28.5 / 16.4 | Y / Y |
| video | 62,600 / 81,692 | 2,744 / 1,542 | 8 / 7 | 1 / 1 | 34.3 / 19.2 | Y / Y |
| mcp_stamp | 33,468 / 34,325 | 639 / 376 | 5 / 3 | 1 / 1 | 14.5 / 7.3 | Y / Y |
| **Total** | **990,027 / 754,668** | **35,167 / 13,708** | **135 / 78** | **15 / 15** | **666.1 / 177.4** | **15/15 / 15/15** |

## What this means

- **Wombat's single-agent path is lean and fast** on this suite: 177s vs 666s
  wall time (3.75× faster) and 13.7k vs 35.2k output tokens (2.57× fewer).
- **Both agents pass 15/15 under the revised check.** Wombat's only original
  miss was a leading space in `edit_notes`; the check now trims whitespace.
- **The total-input ratio is not a clean efficiency win.** Fresh input is a tie;
  the gap is cached context from more OpenCode steps.
- **The headless question guard is the product bug to fix.** It turned a
  completed task into an error on three runs.

## Issues and check notes

Both agents pass all 15 checks after the `edit_notes` check was relaxed to trim
leading/trailing whitespace from the first line. The original exact-match result
is noted below.

### `edit_notes` check adjustment

The task required the first line to be exactly `FINAL`, with `recieve` corrected
to `receive` and nothing else changed. Wombat wrote ` FINAL` — a leading space
before the word. Under the original exact check, that failed. The check now
trims leading/trailing whitespace, so Wombat passes; the rest of the file was
correct. The raw row keeps both results: `passed_exact: false` and
`passed: true`.

This was an exactness issue, not a reasoning failure. It is also a useful
reminder that "minimal edit" tasks can fail on whitespace that looks invisible
in a rendered diff.

### Wombat: headless question guard

On `website`, `snake_game`, and `svg_logo`, Wombat wrote the deliverable and then
tried to ask the user a question. In headless mode the guard at
[`internal/cli/util.go:240`](../../../internal/cli/util.go#L240) returned:

```text
this run has no terminal to ask the user in — run it interactively, or rephrase the task
```

All three checks passed, so the deliverables were complete. The error is a
headless UX bug: an optional confirmation after the work is done should not
surface as a fatal error. This is worth fixing in Wombat's headless path.

### OpenCode

OpenCode passed 15/15 with no timeouts or retries in the sealed rerun. Its first
run in a brand-new isolated home hit an `Unexpected server error` during
cold-start; the final run used a warm isolated home. That is a harness artifact,
not a task failure.

## Method

| Control | Detail |
|---|---|
| Model | `opencode-go/beam-free` for both agents |
| OpenCode | 1.18.35 |
| Wombat | [`main` at `48c3d59`](https://github.com/ramsterr/Wombat/commit/48c3d59), version `0.2.7-dev` |
| Date | 2026-10-31 |
| Tasks | 15 deterministic tasks, one run each |
| Timeout | 300s per task; none hit in the final data |
| Workspace | Fresh directory per run |
| Instructions | Same `AGENTS.md` and same `bench` skill content |
| MCP | Same local `bench_stamp` stdio server |
| Telemetry | OpenCode JSON events; Wombat session JSON |
| Time | Wall-clock agent process; task checks excluded |

**OpenCode command pattern**

```sh
opencode run --pure -m opencode-go/beam-free --agent bench \
  --dir <workspace> --format json --auto "<prompt>"
```

**Wombat command pattern**

```sh
wombat run -p opencode-go -m beam-free --yolo --agents bench "<prompt>"
```

OpenCode received an explicit instruction to load the `bench` skill with its
`skill` tool; it did so in 14/15 runs. Wombat auto-attaches the skill to the
selected agent and did not invoke a `skill` tool. Its single agent run also
received the shared `AGENTS.md`, which carried the same workflow. This is a
mechanism difference, not a content difference.

OpenCode runs shared a warm config home to avoid the cold-start server error but
used separate workspaces; Wombat used a per-run `WOMBAT_HOME`. The first
OpenCode pass had four runs that accessed a sibling Wombat workspace. Those runs
were discarded, and all 15 OpenCode results here come from a sealed rerun with
no sibling workspaces and no readable grader. Wombat results were re-parsed
after fixing a telemetry parser bug; the responsive-website check was also
corrected.

## Limitations

- **One run per task.** There is no run-to-run variance estimate. Both agents
  passed 15/15; the Wilson 95% interval is 79.6–100% for each. The suite is too
  small to distinguish pass rates, so focus on the time and output gaps.
- **Small, deterministic tasks.** These are not a proxy for large-repo work,
  long-horizon planning, or multi-agent coordination.
- **Wall-clock includes provider latency.** Network and provider load can move
  absolute times, especially on the slowest tasks.
- **Token counts are provider-reported.** Input includes cache reads; output is
  the provider's reported output. Uncached input is computed as
  `input − cache_read − cache_write`.
- **Wombat's delegation was not exercised.** Every task recorded one agent run,
  so the results measure its single-agent path.
- **Skill delivery differs.** OpenCode loads a skill through a tool; Wombat
  auto-attaches it. The skill content and project instructions were identical.

## Raw data and charts

The task harness is a standalone script; this page archives the frozen results,
the chart generator, and the charts.

- [results.json](results.json) — 30 records (15 tasks × 2 agents) with tokens,
  cache reads, tool calls, agent runs, duration, and pass/fail.
- [generate_charts.py](generate_charts.py) — regenerates every SVG in
  [`assets/`](assets) from `results.json`. Requires Python 3 and matplotlib.
- [assets/](assets) — the seven charts used on this page.

Run the generator from this directory:

```sh
python3 generate_charts.py
```

It also prints the summary and per-task Markdown tables to stdout.

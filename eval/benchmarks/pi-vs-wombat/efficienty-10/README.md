# efficienty Wombat vs Pi: 10-task harness eval

> Part of the [Pi vs Wombat](../README.md) docs. Raw data: [results.json](results.json).

**Model: `deepseek-v4.1-flash` for both agents.** `beam-free` was returning
HTTP 429 during this run, so both agents were moved to the same paid fallback.
The suite has 4 simple and 6 complex tasks, with the same instructions, skill
content, MCP server, and Playwright install.

![Summary dashboard](assets/summary.svg)

## At a glance

| Metric | Wombat | Pi |
|---|---:|---:|
| Tasks passed | **10/10** | **9/10** |
| Input tokens | 498,144 | 836,303 |
| Uncached input | 96,736 | 129,999 |
| Output tokens | 17,711 | 16,246 |
| Tool calls | 66 | 74 |
| Model steps | 61 | 64 |
| Total wall time | 275.9s | 424.9s |

Wombat passed every task. Pi failed the research task: it made **35 bash
calls**, used **612k input tokens**, hit the **300s timeout**, and never wrote
`answer.md`. Pi has no `websearch`/`webfetch`, so its bash-only research loop is
the weak spot.

The totals are therefore skewed by one failed Pi task. On the **9 ordinary
tasks**, Pi is leaner and faster:

| Ordinary tasks only | Wombat | Pi | W / Pi |
|---|---:|---:|---:|
| Input tokens | 376,592 | 224,267 | 1.68× |
| Uncached input | 75,792 | 51,339 | 1.48× |
| Output tokens | 7,386 | 4,708 | 1.57× |
| Tool calls | 57 | 38 | 1.50× |
| Model steps | 53 | 42 | 1.26× |
| Wall time | 139.1s | 124.9s | 1.11× |

## The short version

- **Wombat 10/10, Pi 9/10.** Wombat wins the suite because it finishes the
  research task; Pi times out.
- **Pi is the leaner harness on ordinary work.** On the 9 non-research tasks it
  used 40% less input and was about 11% faster.
- **Wombat's research budget works.** It used 2 searches; the third was blocked
  with a clear “synthesize now” message. It wrote a 205-line `answer.md`.
- **Wombat still has overhead on simple tasks.** Exact edit: 46k vs Pi's 15k
  input. Refactor: 56k vs 28k. Browser: 43k vs 22k.
- **The caveats held up.** Vague prompt: both pass, Wombat exits 0 with no
  headless-question error. Git via bash, browser/Playwright, MCP, server
  control, and exact CRLF/UTF-8 edit all pass on both.

## What was tested

| Type | Tasks |
|---|---|
| Simple | `exact_edit`, `bugfix_tests`, `cli_json`, `vague_fix` |
| Complex | `multi_file_refactor`, `git_commit`, `server_process`, `browser_playwright`, `mcp_tool`, `research_bounded` |

<details>
<summary>Task details (abridged)</summary>

- **exact_edit** — Change `mode=draft` to `mode=final` in a CRLF/UTF-8 file,
  preserving the exact bytes.
- **bugfix_tests** — Run the tests, fix `calc.py`, and do not edit the tests.
- **cli_json** — Create `summarize.py` that prints a sorted JSON count map with
  no extra output.
- **vague_fix** — The prompt is only “fix it”; the workspace contains a failing
  test. Check tests pass and the process exits 0.
- **multi_file_refactor** — Add a `discounted` helper used by both order
  functions; behavior unchanged.
- **git_commit** — Create branch `feature`, change `app.txt` to `v2`, commit
  with `feat: bump app to v2`, and leave the tree clean.
- **server_process** — Start `server.py`, fetch `/health`, write the token to
  `health.txt`, and stop the server.
- **browser_playwright** — Use Playwright to click `#reveal`, read the dynamic
  token, write `token.txt`, and save a screenshot whose canvas pixel matches.
- **mcp_tool** — Call `bench_stamp` exactly once with label `task-mcp` and write
  the returned text verbatim.
- **research_bounded** — The job-recommendation prompt; write the final answer
  to `answer.md`.

</details>

## Results

### Ordinary tasks

![Prompt tokens by ordinary task](assets/per-task-input.svg)

![Wall time by ordinary task](assets/per-task-time.svg)

Pi is faster on 7 of the 9 ordinary tasks. Wombat is faster on `bugfix_tests`
(10s vs 22s) and `cli_json` (12s vs 19s). Wombat's input is higher on most
tasks, especially `exact_edit`, `multi_file_refactor`, `server_process`, and
`browser_playwright`; Pi is essentially tied on `bugfix_tests` and `cli_json`.

### Research task

![Research task comparison](assets/research-focus.svg)

| Metric | Wombat | Pi |
|---|---:|---:|
| Passed | **Yes** | **No** |
| Input tokens | 121,552 | 612,036 |
| Output tokens | 10,325 | 11,538 |
| Tool calls | 9 | 36 |
| Model steps | 8 | 22 |
| Wall time | 136.8s | 300.1s (timeout) |
| Answer file | `answer.md` written | missing |

Wombat loaded the web capability, ran two searches, and was stopped on the
third by the quick-mode budget. It then wrote a sourced answer and passed the
five-company / remote-India check. Pi had no search tool; it tried 35 bash
calls, accumulated 612k input, timed out at 300s, and never wrote `answer.md`.

![Cumulative wall time](assets/cumulative-time.svg)

The cumulative chart shows the same story: the two agents are close through the
first eight tasks, then Pi jumps to the 300s timeout on research while Wombat
finishes at 137s.

### Per-task results

Values are **Wombat / Pi**. `In` is prompt tokens; `Out` is output tokens;
`Steps` is model steps; `Time` is wall-clock agent time, excluding checks.

| Task | In tokens | Out tokens | Tools | Steps | Time (s) | Pass |
|---|---:|---:|---:|---:|---:|:---:|
| exact_edit | 46,058 / 15,180 | 760 / 330 | 6 / 2 | 7 / 3 | 16.2 / 8.7 | Y / Y |
| bugfix_tests | 32,901 / 33,938 | 530 / 733 | 6 / 7 | 5 / 6 | 9.9 / 22.0 | Y / Y |
| cli_json | 32,416 / 32,266 | 730 / 649 | 4 / 5 | 5 / 6 | 12.3 / 18.8 | Y / Y |
| multi_file_refactor | 56,470 / 28,190 | 1,031 / 767 | 10 / 6 | 8 / 5 | 19.8 / 13.2 | Y / Y |
| git_commit | 34,471 / 20,422 | 604 / 403 | 4 / 3 | 5 / 4 | 13.8 / 11.2 | Y / Y |
| server_process | 48,764 / 26,488 | 896 / 628 | 6 / 4 | 6 / 5 | 17.7 / 12.9 | Y / Y |
| browser_playwright | 42,722 / 22,139 | 1,223 / 605 | 6 / 4 | 5 / 4 | 16.6 / 14.4 | Y / Y |
| mcp_tool | 31,562 / 19,561 | 392 / 229 | 5 / 3 | 5 / 4 | 12.3 / 10.7 | Y / Y |
| research_bounded | 121,552 / 612,036 | 10,325 / 11,538 | 9 / 36 | 8 / 22 | 136.8 / 300.1 | Y / N |
| vague_fix | 51,228 / 26,083 | 1,220 / 364 | 10 / 4 | 7 / 5 | 20.5 / 13.1 | Y / Y |
| **Total** | **498,144 / 836,303** | **17,711 / 16,246** | **66 / 74** | **61 / 64** | **275.9 / 424.9** | **10/10 / 9/10** |

## What this means

- **For research-heavy work, Wombat is the better harness here.** Pi's default
  four-tool setup has no web search; bash research loops are slow and can time
  out. Wombat's web capability plus bounded research policy finished the task.
- **For ordinary coding work, Pi is the leaner harness.** Even after the dedup,
  Wombat's profile, system prompt, and orchestrator context cost more tokens on
  simple tasks. If token cost matters, Pi is the better default for small work.
- **Wombat's caveats are now covered.** The vague prompt exits 0 with no
  headless-question error; git works through bash after the git tools were
  removed; browser, MCP, server control, and exact edits pass.
- **No delegation was exercised.** Both agents recorded one agent run per task.
  The suite measures the single-agent/lead path, not Wombat's worker fleet.

## Method

| Control | Detail |
|---|---|
| Model | `deepseek-v4.1-flash` for both agents |
| Wombat | `efficienty` at `beba1b8` (dedup + research timer fix) |
| Pi | 1.4.0 |
| Tasks | 10 tasks, one run each |
| Timeout | 300s per task |
| Workspace | Fresh directory per run |
| Instructions | Same `AGENTS.md` and `bench` skill content |
| MCP | Same local `bench_stamp` server |
| Browser | Same shared Playwright install |
| Wombat profile | Auto + orchestrator enabled |
| Pi tools | Default four tools plus MCP |
| Telemetry | Pi JSON events; Wombat session JSON |
| Time | Wall-clock agent process; checks excluded |

The research task was rerun after the timer fix. The other tasks do not use web
research, so the timer change does not affect their numbers.

## Limitations

- **One run per task.** There is no variance estimate. The Pi research timeout
  is one observation, but the failure mode is clear: no web search tool, 35 bash
  calls, no answer file.
- **The model is a fallback.** `beam-free` was rate-limited (429), so this is
  not directly comparable to the earlier beam-free tables.
- **Wombat's orchestrator adds overhead.** The default auto profile includes
  orchestration tools; simple tasks pay for them even when no workers run.
- **No delegation.** Every task recorded one agent run, so this does not test
  Wombat's multi-agent fleet.
- **Deterministic checks, not quality scores.** The research check requires five
  known company/repo names and a remote/India mention; it does not judge prose
  quality.

## Raw data and charts

- [results.json](results.json) — 20 records (10 tasks × 2 agents) with tokens,
  cache reads, tool calls, steps, duration, timeout, and pass/fail.
- [generate_charts.py](generate_charts.py) — regenerates every SVG in
  [`assets/`](assets) from `results.json`.
- [assets/](assets) — the five charts used on this page.

Run the generator from this directory:

```sh
python3 generate_charts.py
```

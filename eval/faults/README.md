# Fault-injection evals

`eval/tasks` measures whether a model can code. This directory measures whether
the **harness** stays honest when the world misbehaves: the provider returns 500s,
cuts the stream mid-tool-call, or never answers at all.

The trick is that no real model is involved. `cmd/faultsrv` speaks the
OpenAI-compatible streaming protocol and plays the part of the model: on the
first request it emits a `write` tool call that fixes the `go-off-by-one`
fixture, and on every later request it emits a short final message. Because the
underlying task is trivial, any failure isolates the transport fault rather than
the model's coding ability. Every scenario drives the real agent loop through
`cmd/eval`, including the real tool registry and the real `go test ./...` check.

## Run

```sh
sh eval/faults/run.sh              # all scenarios
sh eval/faults/run.sh rate429      # one scenario
sh eval/faults/run.sh --list       # names only
KEEP=1 sh eval/faults/run.sh       # keep per-scenario artifacts
```

The script builds `cmd/faultsrv` and `cmd/eval` once, then for each scenario
starts a server on an ephemeral port, writes a throwaway config that points a
`fault` provider at it, and runs one eval task with `-orchestrator=false`.
A scenario prints `ok` when the observed exit code, request count and elapsed
time match its expectation. Exit status is non-zero if any scenario disagrees.

No API key and no network access are needed. Artifacts (config, request log,
report, stderr) live in a temp directory and are removed unless `KEEP=1`.

## Scenarios

| Scenario | Injection | Expected |
| --- | --- | --- |
| `clean` | well-formed stream | solves the task in 2 requests |
| `flaky500` | 500 on the first request | retries, completes |
| `rate429` | 429 with `Retry-After: 1` | waits at least the advertised delay |
| `unauthorized` | 401 always | fails fast, exactly one request |
| `truncate` | TCP reset mid-tool-call | retries the whole turn, completes |
| `nodone` | full stream, no `[DONE]` | accepted |
| `noend` | complete tool call, no `finish_reason`, no `[DONE]`, clean EOF | accepted — see findings |
| `cleancut` | tool call cut off by a clean EOF | the turn must not be treated as usable |
| `badjson` | `data: {oops` line | skipped, stream continues |
| `badargs` | tool call whose arguments are invalid JSON | surfaced as a task failure, no crash |
| `nousage` | no usage chunk | completes, no invented token counts |
| `slow` | 2 s between deltas | waits, does not abort |
| `fallback` | primary always 503, `good-model` healthy | falls back and completes |
| `hang` | 200 with a body that never arrives | aborts at the task timeout |

## Adding a scenario

1. Add a `case` to `cmd/faultsrv/main.go`. Use `newStream` for SSE, `writeJSON`
   for HTTP errors, and `abrupt` for a reset mid-stream. `solve` emits the
   scripted tool call or final message; `toolResult` tells you which turn it is.
2. Add a line to `SCENARIOS` in `run.sh`:
   `name|expect_exit|min_requests|min_elapsed_ms|timeout_s|expectation`.
3. Run it and check the artifacts before trusting the verdict: `KEEP=1 sh
   eval/faults/run.sh name` then read `out/name/report/report.json`.

State-corruption faults (session and checkpoint files) live next to the code
they protect instead, because they need no model at all:
`internal/session/fault_test.go` and `internal/checkpoint/fault_test.go`.

## Scope

These scenarios cover the lead agent only. Delegation, worker worktrees and the
writer merge gate need a git repository and a second model turn, and are not
exercised here; that is the next layer (see `findings.md`).

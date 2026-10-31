# Fault-injection findings

Run: 2026-10-22, `KEEP=1 sh eval/faults/run.sh` on `main`, Go 1.26.
14 transport scenarios, 11 state-corruption assertions. All scenario
expectations pass. Three findings, one of them a fixed data-loss bug.

| # | Finding | Severity | Status |
| --- | --- | --- | --- |
| 1 | A truncated stream is accepted as a complete turn | High | Open |
| 2 | Fallback re-probes the dead primary on every turn | Medium (cost/latency) | Open |
| 3 | `checkpoint.Restore` deleted files after a failed checkout | High (data loss) | Fixed |

---

## 1. A truncated stream is accepted as a complete turn

**What happened.** The `noend` scenario sends a complete `write` tool call, then
closes the connection cleanly without ever sending `finish_reason` or `[DONE]`.
The harness treated it as a finished turn, applied the edit, and the task passed:

```
noend    ok   exit=0   requests=2   passed=True   steps=2
```

The `cleancut` scenario cuts the same stream in the middle of the arguments
string, also with a clean EOF. The harness executed the resulting tool call,
got `bad arguments` back from the write tool, told the model, and carried on:

```
cleancut ok   exit=1   requests=2   steps=2   err="check: go test ./...: exit status 1"
```

Note `steps=2` and a second request with `tool_result=true`: the partial turn was
dispatched, not rejected.

**Why it matters.** A stream that ends on a value boundary is indistinguishable
from a complete one, so a proxy that cuts a response at a lucky byte offset can
cause a *well-formed but unintended* tool call to execute. Today the only
backstop is the tool's own argument validation, which will not catch a
semantically wrong but syntactically valid call.

**Where.** `internal/llm/sse.go:30` returns `nil` at EOF, and
`internal/llm/openai.go:238` treats `[DONE]` as a no-op, so nothing ever records
whether the stream reached a terminal state.

**Suggested fix.** Track `sawFinish` (any non-empty `finish_reason`) and
`sawDone`. After the read loop, if neither was seen, return
`fmt.Errorf("%s: incomplete stream", o.name)` so the retry wrapper decides.
That rule keeps `nodone` working, since that scenario does send
`finish_reason` before closing.

---

## 2. Fallback re-probes the dead primary on every turn

**What happened.** With the primary returning 503 and `fallback_models:
["good-model"]`, a two-turn task cost 12 requests and 11.5 s:

```
 1-5  fault-model  tool_result=false     (5 attempts, ~4.3 s)
 6    good-model   tool_result=false     (returns the write call)
 7-11 fault-model  tool_result=true      (5 attempts again, ~5.6 s)
 12   good-model   tool_result=true      (final message)
```

Requests 6 and 7 are 2 ms apart: the tool result came back and the next turn
immediately restarted at the primary and burned the full retry budget again.

**Why it matters.** Fallback is not sticky. Every turn of every task pays
`MaxAttempts` failed round-trips before reaching a healthy model, so a dead
primary multiplies latency and request count by the number of turns. A 60-turn
session against a dead primary makes ~300 doomed requests.

**Where.** `internal/llm/fallback.go:20` wraps a `Provider` with a model list and
as far as the code shows, holds no state between `Chat` calls; `buildProvider`
applies it per provider, not per session.

**Suggested fix.** Keep the model that last succeeded for the remainder of the
session (sticky fallback), or add a small circuit breaker: after N consecutive
transport failures, skip the primary for a cooldown window. Either way the
second turn should make 1 request, not 6.

---

## 3. `checkpoint.Restore` deleted files after a failed checkout

**What happened.** `Restore` ran `git checkout --force` for each path with the
error discarded, then removed every workspace file absent from the manifest. If
the shadow object store is missing or corrupt, the checkout restores nothing,
the deletion pass still runs, and `Restore` returns `nil`.

Reproduce on the pre-fix tree:

```sh
go test ./internal/checkpoint -run TestFaultRestoreMissingObjectStore
# restore without an object store must return an error
```

**Blast radius.** A full restore deletes files created after the snapshot. So a
pruned `.wombat` directory, a partial backup restore, or a cleaned temp dir turns
"undo my last edit" into silent deletion of newer work, reported as success.

**Where.** `internal/checkpoint/checkpoint.go`, in `Restore`.

**Fix applied.** Distinguish "this scoped path was never in the snapshot"
(`did not match any file`, legitimate) from any other checkout failure, and
abort before the removal pass:

```go
if _, err := m.git("checkout", "--force", manifest.ID, "--", path); err != nil {
	if strings.Contains(err.Error(), "did not match any file") {
		continue
	}
	return fmt.Errorf("restore %s: %w", id, err)
}
```

`TestFaultRestoreMissingObjectStore` fails before the change and passes after
it. The other four checkpoint fault tests (missing manifest, corrupt manifest,
foreign workspace, corrupt entry in `List`) passed before the fix.

---

## What held up

Worth stating, because these are the behaviours the retry layer exists for:

- 500 → retry → success (`flaky500`, 3 requests).
- `Retry-After: 1` honoured: 1.8 s versus 1.2 s for the same task without it.
- 401 is not retried: exactly one request (`unauthorized`).
- A TCP reset mid-tool-call is retried and the discarded arguments never reach
  a tool (`truncate`, 3 requests, green check).
- An undecodable `data:` line is skipped, not fatal (`badjson`).
- Invalid tool arguments surface as a task failure with no panic (`badargs`).
- A stalled body aborts at the task timeout: 26.8 s against a 25 s budget
  (`hang`), failure class `agent`, no leaked process.
- Missing usage does not fabricate token counts (`nousage`).
- Sessions: a torn trailing line keeps every complete message; a corrupt header
  and an oversized line return errors instead of panicking
  (`internal/session/fault_test.go`).
- Checkpoints: corrupt manifests are skipped by `List` and refused by `Restore`.

## Next layer

Not covered yet, because both need a git repository and extra model turns:

- `delegate_write` with a worker that returns an empty patch, a patch outside
  its declared ownership, or a patch that fails review — the guards exist at
  `internal/orchestrator/writers.go:467`, `:484`, `:501` but are untested
  end-to-end.
- Killing a worker mid-run and asserting nothing merges and the worktree is
  cleaned up.
- A leaked background job after `jobs` (`internal/tools/jobs.go:132`).

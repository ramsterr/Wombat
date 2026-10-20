# Changelog

All notable changes to Wombat are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Removed

- **The OS sandbox is gone.** `sandbox-exec`/`bwrap` policies, sandbox
  modes, network gating, and the `/sandbox` command are removed. Commands
  run with the user's own permissions; the permission layer is the gate.
- **No workflow limits.** `max_steps`, `max_delegations`,
  `max_parallel_scouts`, worker/writer step and token budgets, turn budgets,
  worker/writer timeouts, fleet size caps, plan step/fact/check caps, and the
  default bash timeout are removed. A turn ends when the model stops calling
  tools, the user interrupts, or the provider fails.

### Changed

- Permissions default to `allow` for bash, write, edit, network, and outside
  paths, so a headless run works without `--yolo`. A version-1 config with the
  old ask-by-default permissions is migrated automatically; an explicit deny
  or allow is left alone.
- Checkpoints no longer snapshot the whole workspace before a mutating bash
  command; only the file a write, edit, or multi-edit is about to touch is
  snapshotted. Paths outside the workspace are skipped instead of failing the
  write. Checkpoint file/size caps are removed.

### Fixed

- A provider error no longer poisons every later prompt. On a 400/422
  (`invalid_request`, context overflow, bad tool ordering), Wombat backs up
  the session and retries the latest prompt from a fresh context. If that also
  fails, it drops the failed prompt and restores the last good history, so the
  next prompt starts clean instead of replaying the same error.
- Text-only models (Beam, LongCat) no longer fail with `Beam supports text
  input only` when the conversation contains an image. `read` returns image
  metadata without attaching it, existing image attachments are replaced with
  a text note per request, and a provider rejection retries once without the
  images and switches the turn to text-only mode.
- Read-only workers now get `websearch` and `webfetch`, so delegated research
  can actually reach the network.
- Thinking-mode providers no longer reject a worker after several tool turns:
  reasoning is kept on every assistant turn that has tool calls.
- `delegate_many` accepts several scouts that share one ledger step instead of
  rejecting the whole fleet.
- `run_checks` goes through the same permission gate as bash instead of
  running arbitrary commands with no approval.
- The line CLI hides reasoning when `show_thinking` is false, and a headless
  run prints a "still working" line during long silent model turns.
- `question` fails loudly instead of reading garbage from stdin in a
  non-interactive run.
- `wombat --version` and `wombat -v` work.


### Added

- **A scratch directory per session** (`~/.wombat/tmp/<session>`, also in
  `$WOMBAT_TMP`), so temporary files stay out of the project and out of the
  checkpoints.
- **Parallel workers are the default.** The prompt, the tool descriptions, and
  the plan all push the agent to dispatch a fleet of two to five workers for
  anything past two steps. `wombat config set parallel_agents false` turns it
  off.

### Fixed

- A checkpoint no longer dies on a directory it cannot read. The macOS trash
  (`~/.Trash`) and `~/Library` are hidden by the operating system, so a
  snapshot of the home directory failed and blocked every command. They are
  skipped now, and an unreadable directory is recorded as skipped rather than
  failing the snapshot or the restore.
- Pressing Enter in the question prompt now takes the highlighted option.
- The snapshot no longer fails when the workspace is too big. It captures what
  it can, stops early instead of walking gigabytes, and a restore only puts
  back the files it captured, so nothing is deleted. The old "checkpoint is
  too large" and "too many files" errors are gone.
- A redirect to /dev/null or a file-descriptor copy no longer counts as a
  mutation, so read-only commands no longer trigger a checkpoint at all.
- Running out of steps is no longer an error. The last step gives the model no
  tools, so it has to answer in text, and the turn ends normally instead of
  failing with "stopped after N steps without completing".

### Added

- **A `question` tool**: when the output depends on a choice only the user can
  make, the agent asks - with two or three concrete options, and the user can
  type their own answer - instead of guessing. The prompt also tells the agent
  to hand the work back when it is done rather than keep polishing on its own.

- **Web tools**: `websearch` and `webfetch` are real tools now, so research
  costs one round-trip instead of a shell command plus a permission prompt.
  The read-only workers get them as well.
- **`batch`**: several read, grep, and glob calls in one round-trip.
- **`multi_edit`**: several edits to one file, checked before any of them is
  written, so the file is never half-edited.
- Large search results are written to a file with a pointer, so the model
  reads only the part it needs instead of carrying the whole output.
- **Parallel tool execution**: independent read-only tool calls now run at the
  same time, while anything that can change the workspace still runs alone and
  in order. Tool execution is about half of an agent's wall-clock time, so
  this is the largest single speed win.
- A doom-loop guard: the same call repeated with the same arguments three
  times stops with "stop repeating it" instead of burning the turn.
- `/thinking on|off` and a `show_thinking` setting. The thinking is hidden by
  default and shown as a one-line summary; it is always kept in the session.
- A `scout_model` setting: the read-only workers can run on a cheaper model
  than the lead, which is where most of the reading happens.
- A `reasoning` setting (off, low, medium, high) that maps to the provider's
  thinking controls, so the cost of thinking is the user's choice.

### Changed

- Only the newest assistant turn keeps its reasoning in the request. Older
  reasoning was replayed on every request, paying input tokens for text the
  model no longer needs. The session still keeps it for the transcript.

### Added

- The agent can look at images it finds: reading an image file attaches it to
  the next message, so a vision-capable model sees it exactly like a pasted
  image. Download with curl, then read the file.
- The system prompt tells the agent to work in parallel: fan out two to five
  workers with `delegate_many` for research, comparison, or anything with
  more than two steps, each on a different angle, then synthesize.

### Changed

- The system prompt is compressed by about 40% (it is sent with every
  request), and the orchestration rules were tightened the same way.
- The lead keeps far less tool output verbatim in its history (16k tokens
  instead of 40k) and tool results are smaller, so every request is cheaper.
- Five scouts run at once by default, each with a tighter step and token
  budget, so a fleet finishes in one wave. The per-turn delegation budget is
  now six, so a five-scout fleet fits in a single turn.

### Removed

- The agent's turn budgets: `max_turn_tokens`, `max_turn_cost`,
  `max_turn_duration_seconds`, and `max_tool_calls` (and the `price_*`
  settings that fed the cost budget). A turn now ends when the work is done
  or when `max_steps` is reached. Worker, writer, and delegation budgets are
  unchanged.

### Added

- A **craft standard** in the system prompt: aim at a professional result, and
  research before building whenever the network is available. The agent is
  told to study how professionals do the work, prefer proven and licensed
  sources, match the repository's own examples, and verify the result by
  building and looking at it. Concrete starting points are named per kind of
  work (3D, frontend, charts, icons, code, audio).

### Changed

- The model is now told explicitly whether the network is available, so it
  researches when it can and asks when it cannot.

- **Modes**: `chat` (look and explain, change nothing) and `create` (read, write,
  edit, run anything). Switch with `/mode chat|create`, list with `/modes`. The
  mode is enforced in the tools, shown in the header, and told to the model.
- **Command palette**: typing `/` in the chat shows every command, filtered as
  you type. Arrow keys move, tab completes, esc dismisses. The line REPL
  suggests the closest matches for an unknown command.
- `/toolset` lists the tools of the session and how each is gated, `/sources`
  lists the sites and files the session used, `/history` lists its messages.

### Changed

- The OS sandbox is now **off by default**. The agent asks for every tool call
  instead of being silently confined; switch the sandbox on with
  `/sandbox mode workspace-write` when you want the extra layer.

- `/sandbox` in the TUI and the REPL: show the sandbox mode, turn the network
  on or off, and switch between `off`, `read-only`, `workspace-write`, and
  `danger-full-access` without restarting the session.
- Commands that need the network now fail with a message that says how to
  enable it, instead of an opaque DNS error, and the model is told to ask
  the user instead of working around the restriction.
- The sandbox asks for permission instead of dead-ending: when a command is
  blocked, Wombat prompts you to allow it once, for the session, or not at
  all, and the command runs when you allow it. The same applies to writes
  outside the workspace.

## [0.2.5] — 2026-10-20

### Fixed

- The network and dangerous-command prompts now actually reach the user. The
  static checks used to refuse the command before the escalation could ask,
  and a session approval did not persist.
- The dangerous-command list (`sudo`, `rm -rf /`, `mkfs`, `dd of=/dev/`,
  `git push --force`, fork bombs, …) is enforced for the lead agent again;
  it was implemented but never enabled outside the eval harness.
- Writers get their own sandbox settings instead of inheriting the lead's,
  so a read-only lead no longer produces read-only writers.
- `wombat config set permissions.outside allow|deny|ask` is accepted.

- The Homebrew tap update authenticates with an authorization header and
  reports what is wrong when the token cannot reach the tap repository.

## [0.2.4] — 2026-10-20

### Fixed

- The Homebrew tap update now commits the formula with git instead of the
  contents API, and a failed tap update is reported instead of being silently
  ignored.

## [0.2.3] — 2026-10-20

### Added

- A `Homebrew tap` workflow that refreshes the formula on demand, without
  cutting a new release.

### Fixed

- The release workflow now reports a clear error when the tap secret is
  missing instead of silently skipping the update.

## [0.2.2] — 2026-10-20

### Added

- A Homebrew tap at `ramsterr/homebrew-wombat`, refreshed automatically after
  every release. Install with `brew tap ramsterr/wombat && brew install
  wombat`, update with `brew upgrade wombat`.

## [0.2.1] — 2026-10-20

### Fixed

- The sandbox now falls back to `off` on hosts that cannot enforce it, so
  writers keep working on Linux without `bwrap`.
- The release workflow no longer uses the `secrets` context in a step
  condition, which made GitHub reject the workflow file.
- Binaries installed with `go install` now report the module version instead
  of `dev`.

## [0.2.0] — 2026-10-20

Reliability, verification, parallel writers, and the release pipeline.

### Added

- **OS sandbox** — `off`, `read-only`, `workspace-write`, and
  `danger-full-access` modes with `sandbox-exec` (macOS) and `bwrap` (Linux)
  backends. Bash runs sandboxed by default; unsupported platforms fall back
  to `off`.
- **Checkpoints** — scoped and full snapshots taken before mutating bash,
  writes, and edits, with `wombat checkpoint list` and
  `wombat checkpoint restore` to recover a workspace.
- **Retries** — typed HTTP errors with `Retry-After` support and exponential
  backoff for transient provider failures.
- **Turn budgets** — caps on tokens, cost, wall-clock time, and tool calls per
  turn, with a hard timeout that cancels the provider request.
- **Verification** — per-step checks in the task ledger, a `run_checks` tool,
  and a completion gate that blocks a step whose checks are pending or failing.
- **Trace log** — a JSONL event log per session with run, model, and tool
  events.
- **Check-gated parallel writers** — `delegate_write` runs coding tasks in
  isolated git worktrees with disjoint file ownership, per-writer step and
  token budgets, and a serialized merge queue that only applies a patch when
  the checks pass.
- **Context packs** — writers receive a scoped view of the repository instead
  of the whole tree.
- **Eval hardening** — protected test and dependency files, repeat runs,
  Wilson confidence intervals, and failure classes. The suite grew to 20
  Go fixtures.
- **Release pipeline** — goreleaser builds for macOS, Linux, and Windows,
  a tag-triggered GitHub Release workflow, an install script, `wombat upgrade`,
  and an update check in `wombat doctor`.

### Changed

- Bash runs through a shared command runner with a bounded output buffer.
- The version string is stamped at build time instead of being hardcoded.

[Unreleased]: https://github.com/ramsterr/Wombat/compare/v0.2.5...HEAD
[0.2.5]: https://github.com/ramsterr/Wombat/releases/tag/v0.2.5
[0.2.4]: https://github.com/ramsterr/Wombat/releases/tag/v0.2.4
[0.2.3]: https://github.com/ramsterr/Wombat/releases/tag/v0.2.3
[0.2.2]: https://github.com/ramsterr/Wombat/releases/tag/v0.2.2
[0.2.1]: https://github.com/ramsterr/Wombat/releases/tag/v0.2.1
[0.2.0]: https://github.com/ramsterr/Wombat/releases/tag/v0.2.0

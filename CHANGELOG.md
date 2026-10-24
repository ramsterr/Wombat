# Changelog

All notable changes to Wombat are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Model Context Protocol servers.** Add local stdio or remote HTTP/SSE
  servers with `wombat mcp add`, check them with `wombat mcp test`, and import
  existing `mcpServers` or OpenCode configs. Their tools appear to the agent as
  `mcp__<server>__<tool>`, with per-server permission gates, tool filters,
  timeouts, session call counts, and a TUI `/mcp` view. Remote OAuth servers
  use `wombat mcp login`/`logout`, with PKCE, loopback callbacks, automatic
  refresh, and tokens stored under `~/.wombat/mcp-auth` with `0600` permissions.
- **A rebuilt terminal interface.** The chat now has a framed inspector with
  session facts, context usage, live preview, attachments, recent tool calls,
  and the key map; a live `ctrl+o` preview opener; prompt history; a searchable
  command palette with command groups and inline options; and `@`-file
  completion with `Tab`.
- **Fifteen colour themes** — `wombat`, `tokyo`, `catppuccin`, `gruvbox`,
  `nord`, `rosepine`, `dracula`, `everforest`, `kanagawa`, `solarized`,
  `monokai`, `iceberg`, `nightfox`, `onedark`, and `github` — with `/theme`, a
  picker, and `ctrl+t` cycling. The choice is saved in the config.
- **A help and command centre** on `ctrl+g`/`?`/`/help` (`/keys`), plus a
  grouped command reference. `alt+1..5` jumps between chat, sessions, models,
  usage, and doctor. The `/` palette shows theme names and swatches inline.
- **Richer transcripts**: tool glyphs and live timers, diff-coloured tool
  output, box tables, numbered lists, links, and fenced-code labels.
- The Wombat banner shown at the start of every new session, plus a compact
  WOMBAT mark pinned to the bottom of the inspector. The inspector key list was
  removed.
- **Project references** in the inspector (`ctrl+r`, `/reference add|rm|clear`).
  Files, folders, and URLs are stored per project under `~/.wombat/references/`
  and survive sessions.
- **An interactive sources browser** (`/sources`) with all/sites/files tabs;
  enter opens a site or attaches a file.
- A scrollbar for the transcript, `ctrl+up/down` and `ctrl+home/end` scrolling,
  optional mouse-wheel scrolling via `/mouse`, and a themed terminal background
  that is restored when Wombat exits.
- **Multiple previews**: local servers, HTML, PDFs, images, and video are
  collected, de-duplicated by origin or file path, ordered newest-first in a
  left preview rail, and browsable with `/previews` or `/preview <n>`.
- **A visual hand-off gate**: after the first complete visual artifact, the
  harness asks "Looks good" / "Needs changes" before another modelling step.
  Approval stops the run; feedback continues it. Headless runs stop after three
  visual passes instead of polishing without a reviewer.
- **Opt-in image generation**: `imagegen` is hidden from the model by default.
  `/imagen` enables it for the session and `/imagen <prompt>` generates
  directly, so the agent cannot start generating images on its own.
- **Provider configuration**: selecting a provider opens a Configure form for
  its base URL and API key. `/configure` is the primary command, with
  `/provider` and `/providers` as aliases. Provider tabs switch with ←/→.
- **Unconfigured models stay selectable**: Wombat shows the chosen model with
  `no key` in the header, keeps it blocked from chat, and tells you to run
  `/configure <provider>`. Providers no longer inherit an unrelated
  `OPENAI_API_KEY`.
- **Reasoning picker**: after selecting a reasoning model, choose off/low/
  medium/high/max; `/reasoning` reopens it.
- **Copy support**: `ctrl+y` copies the last reply; `/copy` opens a picker for
  any earlier reply; `/copy all` copies the transcript. A terminal-clipboard
  fallback keeps copy working when the platform helper is unavailable. Mouse
  reporting is off by default so terminal text selection works; `/mouse`
  enables wheel scrolling. Terminal control sequences and stray mouse reports
  are scrubbed from the composer and transcript so scrolling cannot corrupt
  the screen.

### Fixed

- A checkpoint restore now aborts when git reports a real error instead of
  silently reporting success. A scoped path that did not exist at snapshot time
  is still skipped.
- Transient network errors no longer stop a turn after streaming has begun:
  retries reset the partial stream, retry with backoff, and the agent adds an
  explicit "Retry the last request" message if provider retries are exhausted.
  The TUI shows the retry status instead of silently stopping.

## [0.2.6] — 2026-10-22

### Removed

- The OS sandbox, entirely. There is no jail for commands any more: the agent
  works with your own permissions, and the permission classes are the only
  gate. `sandbox` and `sandbox_network` are gone from the config.
- Every cap that was holding the agent back: `max_steps`, the turn budgets,
  the worker and writer token budgets, the delegation cap, and the step caps.
  The agent works until the task is done; the doom-loop guard is the only backstop.
- The context and output limits: tool output is no longer trimmed to a
  fraction of what it used to be, and the history keeps far more.

### Changed

- Writer slots are counted while writers are active instead of being reserved
  for a whole turn, so a writer reuses the slot as soon as its patch merges.
- Warm worktrees are pooled and reset between writers (ignored files such as
  `node_modules` survive), and writer checks run once before merge when the
  base is unchanged.
- `delegate_many` accepts `id` and `depends_on` to run a dependency DAG; a
  dependent starts after its upstream reports are available.
- `run_plan` prefers the critical path (longest chain of dependents) when
  slots are contested.
- Browser screenshots are attached in memory and are never written into the
  project. A site or UI task checks one desktop and one mobile view, starts
  the preview, and asks for feedback ("Looks good" / "Needs changes" / a typed
  answer) before polishing further.
- `run_plan` now schedules continuously instead of in barrier waves. A slow
  step no longer holds back independent or newly-ready work, writers can merge
  while scouts are still running, timed-out and transient steps get one retry,
  and slots are shared between scouts and writers.
- Checkpoints now use a private shadow git repository instead of copying the
  workspace. Unchanged files are stored once, old full-copy snapshots are gone,
  and restoring a checkpoint still reverts added files while preserving files
  that were already present but ignored by `.gitignore`.
- Permissions default to `allow` for `bash`, `write`, `edit` and `network`,
  so a headless `wombat run` actually does the work instead of refusing
  every call. Set a class to `ask` to be consulted first.
- Parallel workers stay on: the agent fans out two to five workers for
  anything past two steps.

### Added

- **Batched browser checks.** `browser action=script` runs a whole sequence of
  navigate, click, type, eval, and screenshot steps in one call. Assertions
  written as `{ok, failures}` are compacted, and only failed steps are shown
  by default.
- **Site verification.** `verify_site` runs the project's check script and
  audits pages for console errors, failed requests, broken links and anchors,
  missing assets, JavaScript syntax errors, and horizontal overflow at desktop
  and mobile widths. Results are cached by page fingerprint, so unchanged
  pages are skipped.
- **Multi-file replacement.** `replace` applies a literal or regular
  expression across files, directories, or globs in one call, with capture
  groups and case-insensitive matching.
- **Workers can ask instead of guessing.** A worker or writer can finish with
  `status=needs_input` and questions. `resume_worker` answers it and continues
  the same session (and worktree, for writers), so no work is thrown away.
- **Background plans.** `run_plan background=true` returns immediately; the
  lead can do other work and join with a plain `run_plan`, or stop it with
  `cancel=true`. Cancellation leaves unfinished steps pending, and a later
  `run_plan` resumes them while skipping completed steps.
- **Policy controls.** `check_policy` chooses whether checks run in the
  worktree, after merge, both, or automatically; `review_policy` chooses
  whether every writer is reviewed, only risky ones, or none.
- **Credentials are gated and redacted.** Reads of `.env`, `~/.ssh`, `~/.aws`,
  `~/.wombat/config.json`, and other credentials files require a
  `sensitive-read` decision; shell commands that name one require the same
  approval. Tool output is redacted before it enters the model, and `grep`
  skips credentials files. A `.env` written outside `.gitignore` gets a
  warning.
- **Dangerous commands are a separate permission.** `sudo`, recursive deletes
  of `/` or `$HOME`, force pushes, `terraform destroy`, database drops, and
  piping a download into a shell ask first; with nobody available to approve
  them, they are refused.
- **A diff before every write.** `write`, `edit`, `multi_edit`, and
  `apply_patch` show the proposed change in the permission prompt.
- **Fuzzy edits and atomic multi-file patches.** Edits retry with CRLF and
  whitespace normalization, then match a unique code-token sequence so
  reindented or reformatted blocks are found without matching the wrong one;
  Go files use `go/scanner`, other files use a language-agnostic lexer.
  `apply_patch` validates every hunk before writing and rolls back files it
  already changed if a later write fails.
- **Background jobs.** `bash background=true` starts a managed process;
  `jobs`, `job_output` (`since`, `wait_ms`, `follow`), and `job_kill` manage
  it, and jobs are stopped when the session ends.
- **`verify` and project detection.** Wombat detects Go, Node, Python, Rust,
  Maven, Gradle, Ruby, PHP, CMake, and Makefile projects and runs their own
  test, build, typecheck, lint, and format commands.
- **`symbols`.** Go definitions and references come from the AST, with
  declaration patterns for other languages, so a comment is not mistaken for
  code.
- **Automatic model capability detection.** Attachments, context and output
  limits, reasoning support, tool calling, and the OpenCode wire protocol are
  read from the models.dev catalogue, cached and refreshed daily, so new models
  are recognized without a Wombat release. The built-in model families remain
  as an offline fallback.
- **Browser automation and visual verification.** `browser` opens a local
  Chromium page, clicks, types, presses keys, scrolls, runs JavaScript, resizes
  the viewport, and captures viewport or full-page screenshots. Screenshots are
  attached to the conversation, so frontend work can be checked instead of
  inferred from HTML. It finds Chrome, Chromium, Edge, Brave, Playwright, and
  Puppeteer installs, or uses `WOMBAT_BROWSER`.
- **Image generation and editing.** `imagegen` calls an OpenAI-compatible
  image API, saves the result in the workspace, and attaches it for review.
  It supports generate and edit (with an optional mask), configurable through
  `image_provider` / `image_model` or `WOMBAT_IMAGE_*` variables.
- **Semantic code intelligence through real language servers.** `lsp` asks
  gopls (or the installed server for TypeScript, Python, Rust, C/C++, Java,
  Ruby, PHP, Lua, C#, Swift, Kotlin, or shell) for definitions, references,
  diagnostics, completions, workspace symbols, or a file outline. `rename`
  computes a semantic rename, shows one combined diff, and applies every file
  atomically with a snapshot and rollback. `refactor` applies the server's
  AST-aware code actions — extract or inline code, organize imports, rewrite
  expressions, and quick fixes — through the same preview, permission, and
  rollback path. Servers are discovered on `PATH`, in Go and Cargo bin
  directories, or through `WOMBAT_LSP` / `WOMBAT_LSP_<LANG>`.
- **Semantic search.** `semantic_search` indexes the workspace into chunks and
  ranks them by embedding similarity, so code can be found by what it does
  rather than its exact text. The index is cached outside the repository and
  updated incrementally. It uses any OpenAI-compatible embedding endpoint
  (`embedding_provider` / `embedding_model`, or `WOMBAT_EMBED_*`) and falls
  back to a deterministic offline embedder.
- **Plan scheduler and reviewed delegation.** `run_plan` executes the ledger as
  a dependency graph: ready steps run in parallel, write steps get worktrees,
  declared file ownership, checks, an independent reviewer, and a three-way
  merge, and a failure blocks only its dependents. Delegates now report through
  a `submit_report` tool with a robust JSON fallback, and writers block on
  reviewer verdicts when `review_writers` is enabled (the default). Successful
  merges are applied with git's three-way fallback and left unstaged.
- **Full-power parallel workers.** Delegated workers now default to full access:
  write/edit, network, browser, image generation, and the rest of the
  non-orchestration tools, running in isolated git worktrees with review and
  merge. Research tasks that make no changes complete normally.
  `worker_full_access false` restores read-only scouts. Worker and writer token
  budgets default to unlimited; `worker_token_budget` and the turn caps can be
  set to a positive number to opt back into limits.
- **Faster web research.** `websearch` hedges Exa and Tavily (keyless by
  default), falls back to Bing RSS and DuckDuckGo HTML after a short grace
  period, caches results, and supports Exa/Tavily/Brave/Serper keys. `webfetch`
  caches responses, retries connection failures, extracts main article text,
  and uses a shared 12-second client. Independent `websearch`/`webfetch` calls
  now run alongside other reads.
- **Compaction crash fixed.** Summarizing a short history no longer indexes past
  the message slice; the TUI also recovers an internal panic and reports it
  instead of terminating the session.
- **Unlimited delegations by default.** The per-turn delegation cap is gone;
  `max_parallel_scouts` still bounds concurrency and tokens remain optional.
- **Delegation discipline.** The lead is told to use scripts for scraping and
  repeated shell work, give each worker a disjoint slice and output file, use
  `run_plan` for dependencies, and stop when the artifact and checks pass.
- **Transient network retries.** Connection resets, refused connections,
  DNS failures, timeouts, and unexpected EOFs are retried up to four times
  with backoff before the turn is allowed to fail. Non-streaming requests only,
  so output is never duplicated.
- **Preview-ready banner.** When a tool reports a local dev-server URL, the TUI
  shows a highlighted bar under the header with the URL and whether the agent
  is still iterating. `/preview` opens it; `esc` stops the run.
- **Reasoning-replay recovery.** If a thinking-mode provider rejects a request
  because `reasoning_content` must be passed back, Wombat now disables
  older-reasoning trimming for that session, repairs assistant turns whose
  reasoning was never captured, and retries instead of failing. Providers that
  do not require replay are unaffected.
- **Provider fallback.** `fallback_models` tries the next model on a
  rate-limit, outage, or server error, but never after output has streamed.
- **Binary and image safety.** `read` refuses binary files and attaches images
  (downscaling a large one) instead of dumping bytes into the context.
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

[Unreleased]: https://github.com/ramsterr/Wombat/compare/v0.2.6...HEAD
[0.2.6]: https://github.com/ramsterr/Wombat/releases/tag/v0.2.6
[0.2.5]: https://github.com/ramsterr/Wombat/releases/tag/v0.2.5
[0.2.4]: https://github.com/ramsterr/Wombat/releases/tag/v0.2.4
[0.2.3]: https://github.com/ramsterr/Wombat/releases/tag/v0.2.3
[0.2.2]: https://github.com/ramsterr/Wombat/releases/tag/v0.2.2
[0.2.1]: https://github.com/ramsterr/Wombat/releases/tag/v0.2.1
[0.2.0]: https://github.com/ramsterr/Wombat/releases/tag/v0.2.0

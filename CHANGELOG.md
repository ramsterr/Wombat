# Changelog

All notable changes to Wombat are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Nothing yet.

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

[Unreleased]: https://github.com/ramsterr/Wombat/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/ramsterr/Wombat/releases/tag/v0.2.0

# Wombat features

See the [README](README.md) for install, usage, and configuration.

## Standard

| Feature | What it does |
|---|---|
| Code editing | Read, write, edit, and atomic multi-file patches. |
| Shell and jobs | Run commands, manage background jobs, run tests and builds. |
| Git | Use `bash git` for status, diff, log, and commits. |
| Verification | `verify`, browser assertions, and project checks. |
| Code intelligence | Language-server definitions, references, diagnostics, and completions. |
| Search | Glob and grep. |
| Web | Search and fetch with caching, retries, readable-text extraction, and page excerpts. |
| MCP | Add local stdio or remote HTTP/SSE Model Context Protocol servers, with interactive OAuth; their tools appear as `mcp__<server>__<tool>`. |
| Browser | Local and web automation, screenshots, clicks, typing, scrolling, and JS evaluation. |
| Media | Read images, video, audio, and documents; search images when enabled. |
| Sessions | Resumable sessions, browsing, continuation, and JSONL transcripts. |
| Context | Automatic pruning, summarization, compaction, and token accounting. |
| Checkpoints | Shadow-git snapshots with list and restore. |
| Permissions | Per-class gates for bash, writes, edits, network, sensitive reads, and each MCP server. |
| Providers | OpenAI-compatible APIs, Anthropic, OpenCode, and local model servers. |
| Interface basics | Command palette, file picker, history, copy, themes, and scrollback. |

## Unique

| Feature | What it does |
|---|---|
| Parallel fleet | Lead plus scouts and writers in isolated git worktrees; changes are reviewed and three-way merged. |
| Continuous plan scheduler | A dependency DAG where free slots start newly-ready work, slow steps do not block independent work, and interrupted steps resume. |
| Resumable workers | A worker that needs a decision returns `needs_input` and can be resumed in the same session and worktree. |
| Visual hand-off gate | After the complete visual deliverable, Wombat asks `Looks good` / `Needs changes` at the end of the run; headless runs never stop mid-build for a review. |
| Multi-preview system | Detects local servers, HTML, PDFs, images, and video; de-duplicates by origin or path; shows the newest above the prompt and keeps a `/previews` picker. |
| Source browser | `/sources` browses every site and file the session used, with counts and open/attach actions. |
| Project references | `/reference` keeps files, folders, and URLs in a per-project reference list under `~/.wombat/references/`. |
| Centered chat | A calm, opencode-style conversation column with the wombat art centred on the welcome screen and a live status footer. |
| Search ranking | Merges Brave, Serper, Exa, Tavily, Bing RSS, DuckDuckGo, and Mojeek; boosts trusted docs, engine position, and cross-engine hits; filters adult/SEO spam; caps domains. |
| Image search | Finds direct image URLs via Google Images (Serper), Brave, DuckDuckGo, and Openverse, and downloads them into the workspace. |
| Provider setup | `/configure` opens a base URL/API-key form; unconfigured models stay selectable but show `no key` and are blocked from chat. |
| Reasoning picker | After selecting a reasoning model, choose `off`, `low`, `medium`, `high`, or `max`; `/reasoning` reopens it. |
| Themes | Fifteen palettes and a live picker. |
| Copy | `/copy` for any user, agent, or tool output, with an OSC 52 fallback. |
| File picker | `@` path completion with directory navigation and quoted paths for spaces. |
| Built to be driven | Headless `wombat run`, scriptable sessions, and an interface designed around a centred chat, a live footer, and keyboard flow. |

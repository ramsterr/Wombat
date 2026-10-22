# Wombat

<img width="2872" height="676" alt="Wombat" src="https://github.com/user-attachments/assets/53857c41-0947-4be9-b90f-72dd92ab9f99" />

Wombat is a coding agent for your terminal. It reads and edits files, runs commands, searches the web, uses a browser, and works through multi-step tasks with a plan. It runs against OpenAI-compatible APIs, Anthropic, OpenCode, or a local model server.

## Install

The latest tagged release does not include every change on `main` yet. Use the
source build below to run the current features.

### Run current main

You only need Go and git. This fetches only the latest `main` commit and no old tags; omit the flags if you need full repository history.

```sh
git clone --depth 1 --single-branch --no-tags https://github.com/ramsterr/Wombat.git
cd Wombat
go build -o wombat ./cmd/wombat
./wombat version
./wombat setup
./wombat
```

Already have the checkout? Update it and rebuild the binary:

```sh
git pull
go build -o wombat ./cmd/wombat
./wombat
```

Install it somewhere on your PATH:

```sh
go build -o /usr/local/bin/wombat ./cmd/wombat
```

Requirements: Go 1.26+ for building from source. The core agent has no runtime
dependencies. Optional features use standard tools: a Chromium browser for
`browser`, a language server for `lsp`/`refactor`, git for isolated workers,
and `sh` for `bash`.

### Stable release (tagged versions)

The one-line installer, Homebrew, and `go install ...@latest` install the
latest **tagged release**, not unreleased changes on `main`.

**macOS or Linux**

```sh
curl -fsSL https://raw.githubusercontent.com/ramsterr/Wombat/main/install.sh | bash
```

If `wombat` is not found:

```sh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

**Homebrew**

```sh
brew tap ramsterr/wombat
brew install ramsterr/wombat/wombat
```

**Go**

```sh
go install github.com/ramsterr/Wombat/cmd/wombat@latest
```

**Windows — WSL2**

Install Ubuntu from PowerShell:

```powershell
wsl --install
```

Then, inside Ubuntu:

```sh
sudo apt update && sudo apt install -y git curl
curl -fsSL https://raw.githubusercontent.com/ramsterr/Wombat/main/install.sh | bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
wombat
```

Git Bash also works for the native Windows build, but Wombat's `bash` tool
needs a POSIX `sh`. Native PowerShell/cmd is not fully supported.

## First run

```sh
wombat setup       # choose a provider, paste an API key, choose a model
wombat doctor      # check config, permissions, and connectivity
wombat             # open the chat UI
```

`wombat setup` runs automatically on first launch. Your provider, model, and
API key are saved in `~/.wombat/config.json`.

Without a terminal UI:

```sh
wombat run "summarize this repository and find the test command"
wombat run --yolo "fix the failing tests and explain the change"
```

`--yolo` auto-approves tool permissions; use it for trusted, non-interactive
runs.

## Common commands

| command | what it does |
|---|---|
| `wombat` | open the chat UI |
| `wombat run "<task>"` | run one task and exit |
| `wombat run -c "<task>"` | continue the latest session |
| `wombat resume [id\|last]` | reopen a session |
| `wombat sessions` | list sessions for this directory |
| `wombat setup` | change provider, key, or model |
| `wombat models [provider]` | list available models |
| `wombat doctor` | diagnose config and connectivity |
| `wombat config` | show config, with keys redacted |
| `wombat config set <key> <value>` | change one setting |
| `wombat checkpoint list` | list shadow-git checkpoints |
| `wombat checkpoint restore <id>` | restore a checkpoint |
| `wombat upgrade` | install the latest stable release |
| `wombat version` | print the version |

## Inside the chat

Type a request and press Enter. Useful shortcuts:

- `esc` — stop the current run
- `ctrl+c` — quit
- `/` — show commands
- `@path` — attach a file
- `ctrl+v` — attach an image from the clipboard (macOS/Linux)

Common slash commands:

```text
/help          commands
/model         switch model
/mode          chat or create mode
/attach <path> attach a file
/preview       open the local preview URL
/new           new session
/compact       compact context now
/status        current provider, model, and directory
/cost          token and cache usage
/exit          quit
```

When the agent starts a local web server, a highlighted **preview ready** bar
appears under the header. Use `/preview` to open it, and `esc` to stop when it
looks right.

## What it does

- **Reads and writes code** — read, write, edit, multi-edit, and atomic multi-file patches.
- **Runs commands** — shell, managed background jobs, tests, builds, and project verification.
- **Searches** — glob, grep, AST symbols, semantic search, and web search/fetch.
- **Understands code** — language-server definitions, references, diagnostics, completions, rename, and AST-aware code actions.
- **Sees** — screenshots, images, video, audio, and documents on models that support them.
- **Creates** — image generation and editing, plus browser automation.
- **Plans** — a persistent task ledger with dependencies, checks, structured reports, and continuous scheduling: a slow step does not hold back ready work, and timed-out or transient steps retry once.
- **Delegates** — parallel workers in isolated git worktrees, with independent review and three-way merge.
- **Recovers** — retries transient network failures, repairs rejected thinking-mode histories, and keeps long sessions usable.
- **Caches** — context pruning and summarization, checkpoints, and resumable sessions.

## Parallel agents

Workers are full-access by default. They can read, write, edit, run commands,
use the network, browse, generate images, and use the coding/research toolset.
Orchestration tools and `question` stay with the lead.

- `delegate_many` runs independent workers in one wave.
- `run_plan` executes the ledger as a dependency graph with continuous scheduling. Free slots start newly-ready work instead of waiting for a slow step; writers can review and merge while scouts are still running; timed-out/transient steps retry once; and slots are shared between scouts and writers.
- `delegate_write` gives writers explicit file ownership.
- `rename` and `refactor` use a real language server.
- Workers run in git worktrees when possible; changes are reviewed and merged.
- Token budgets are unlimited by default. `max_parallel_scouts` and
  `max_parallel_writers` limit concurrency.

Use workers for independent files, categories, pages, or audits. Do not use
them for a single shell command or for several agents reading the same data.

```text
Use update_plan to make a scout step and two write steps with disjoint files,
then call run_plan.
```

Set `worker_full_access false` for read-only research workers.

## Configuration

Show the current configuration:

```sh
wombat config
```

### Web search and fetch

`websearch` starts Exa and Tavily keyless searches, caches results for ten
minutes, and falls back to Bing RSS and DuckDuckGo HTML if those are slow or
empty. Set `EXA_API_KEY`, `TAVILY_API_KEY`, `BRAVE_API_KEY`, or
`SERPER_API_KEY` before starting Wombat to use your own search account:

```sh
export EXA_API_KEY=...
export TAVILY_API_KEY=...
export BRAVE_API_KEY=...
export SERPER_API_KEY=...
```

`webfetch`
extracts the main article text when possible, caches responses for repeated
reads, retries transient connection failures, and uses a 12-second HTTP client.

### Common settings

```sh
wombat config set provider openai
wombat config set model gpt-5
wombat config set mode create
wombat config set fallback_models gpt-5-mini
wombat config set scout_model gpt-5-mini
wombat config set worker_full_access false
wombat config set review_writers false
wombat config set max_parallel_scouts 5
wombat config set permissions.bash ask
```

The file lives at `~/.wombat/config.json`. API keys can also come from provider
environment variables (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, and so on).
`wombat setup` is the easiest way to create the config.

## Troubleshooting

**`wombat: command not found`**

Add `~/.local/bin` to `PATH`, or run the binary by its full path.

**No provider or model**

Run `wombat setup`, then `wombat doctor`.

**Writers fail with “main workspace has tracked changes”**

Commit or stash tracked changes first. Workers use git worktrees and need a
clean tracked tree.

**No preview bar appears**

The banner only appears after a tool reports a local URL such as
`http://localhost:5173`.

**An upgrade replaced my source build**

`wombat upgrade` installs the latest tagged release. Rebuild from source to
stay on the current `main`.

## Uninstall

Delete the binary and, if you want to remove settings and sessions too:

```sh
rm -f ~/.local/bin/wombat
rm -rf ~/.wombat
```

## Development

```sh
gofmt -w .
go vet ./...
go test ./...
go build -o /tmp/wombat ./cmd/wombat
```

CI runs on the default branch and on pull requests. Issues and pull requests
are welcome.

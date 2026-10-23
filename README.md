  # Wombat General Agent
  
  <img width="1735" height="907" alt="image" src="https://github.com/user-attachments/assets/e714a11d-a915-4003-97f7-837198c47f1d" />



<p align="center">
  <a href="https://github.com/ramsterr/Wombat/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/ramsterr/Wombat/actions/workflows/ci.yml/badge.svg?branch=main&style=flat-square"></a>
  <a href="https://github.com/ramsterr/Wombat/releases"><img alt="Latest release" src="https://img.shields.io/github/v/release/ramsterr/Wombat?style=flat-square"></a>
  <a href="https://github.com/ramsterr/Wombat/blob/main/go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/ramsterr/Wombat?style=flat-square"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/github/license/ramsterr/Wombat?style=flat-square"></a>
</p>

Wombat is a general purpose agent.
Can do anything and beyond than your standard agent harness , that too for lesser tokens at times.


## Quick start

**macOS / Linux — latest release.** Install, configure, and start:

```sh
curl -fsSL https://raw.githubusercontent.com/ramsterr/Wombat/main/install.sh | bash
wombat setup
wombat
```

**Current `main` — newest features.** The tagged release can lag behind this
branch. With Go 1.26.3+:

```sh
go install github.com/ramsterr/Wombat/cmd/wombat@main
wombat setup
wombat
```

`go install` writes `wombat` to `$(go env GOPATH)/bin`; add that directory to
`PATH` if the command is not found. Update a release build with `wombat upgrade`;
rerun the `go install` command to update a `main` build (upgrade switches it to
the latest tagged release).

**Windows** — install WSL2 and Ubuntu (`wsl --install`), then run the
macOS/Linux command inside Ubuntu. Git Bash works for the native Windows build,
but Wombat's `bash` tool needs a POSIX `sh`; native PowerShell/cmd is not fully
supported.

`wombat setup` runs automatically on first launch and saves your provider,
model, and API key in `~/.wombat/config.json`.

## Install

The release installers below install the latest tagged release. Use the source
build when you want the current `main` features.

### Build from source (current main)

You only need Go 1.26.3+ and git. This fetches only the latest `main` commit and no old tags; omit the flags if you need full repository history.

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

Requirements: Go 1.26.3+ for building from source. The core agent has no runtime
dependencies. Optional features use standard tools: a Chromium browser for
`browser`, a language server for `lsp`/`refactor`, git for isolated workers,
and `sh` for `bash`.

### Release installers

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

Install Ubuntu from PowerShell with `wsl --install`, then run the macOS/Linux
installer inside Ubuntu. If `wombat` is not found, add `~/.local/bin` to your
`PATH` as shown above.

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

- `esc` — stop the current run, close an overlay
- `ctrl+c` — quit
- `/` — command palette (options appear inline)
- `@` — file picker with tab completion
- `ctrl+v` — attach an image from the clipboard (macOS/Linux)
- `ctrl+b` — toggle the inspector panel
- `ctrl+o` — open the live preview
- `ctrl+g` or `?` — help and key map
- `ctrl+t` — cycle colour themes
- `ctrl+p` / `ctrl+n` — prompt history
- `ctrl+r` — add a project reference (file, folder, or URL)
- `ctrl+y` — copy the last reply; `/copy` picks any reply; `/copy all` copies
  the whole transcript
- `alt+1..5` — jump between chat, sessions, models, usage, and doctor
- `pgup` / `pgdn` or `ctrl+up/down` — scroll the transcript
- mouse wheel — mouse reporting is off by default so you can select and copy
  text normally; run `/mouse` to enable wheel scrolling

Common slash commands:

```text
/help          keyboard map and every command
/keys          alias for /help
/reference     keep files and links in the inspector
/sources       browse the sites and files the session used
/preview [n]   open a preview; /previews lists them all
/copy [n|all]  pick a reply to copy, or copy the transcript
/model         switch model
/configure     choose and configure a provider
/providers     list every provider
/reasoning     off, low, medium, high, or max
/mode          chat or create mode
/attach <path> attach a file
/panel         toggle the inspector
/theme         open the live theme picker
/new           new session
/compact       compact context now
/status        current provider, model, and directory
/cost          token and cache usage
/exit          quit
```

Themes ship with the app: `wombat`, `tokyo`, `catppuccin`, `gruvbox`, `nord`,
`rosepine`, `dracula`, `everforest`, `kanagawa`, `solarized`, `monokai`,
`iceberg`, `nightfox`, `onedark`, and `github`. Pick one with `/theme`, or cycle
with `ctrl+t`; the choice is saved in `~/.wombat/config.json`. The welcome screen
shows the Wombat banner, and the inspector keeps a compact WOMBAT mark at the
bottom. The theme also tints the terminal background while Wombat is open and
restores it on exit (`wombat config set paint_background false` disables this).

**References** are project-scoped notes that stay in the right-hand inspector.
Press `ctrl+r` to add a path or URL, or use `/reference add <path|url>`. Remove
them with `/reference rm <n>` or clear the list with `/reference clear`. They are
stored under `~/.wombat/references/`, never in your repository.

**Sources** (`/sources`) opens an interactive browser of every site and file the
session has used. Switch tabs with `tab`, move with the arrows, and press enter
to open a URL or attach a file.

**Previews** are collected as the agent works: local dev servers, HTML pages,
PDFs, images, and video. They are de-duplicated by site origin or file path, the
newest is shown in the banner, and the left preview rail keeps the ordered
list. Use `ctrl+o` to open the newest or `/previews` to choose from all of them.

Wombat inherits your terminal's font; a terminal UI cannot install a font.
For the intended look and complete glyph set, use a Nerd Font such as JetBrains
Mono, Berkeley Mono, or MesloLGS NF in your terminal profile.

When the agent starts a local web server, a highlighted **preview ready** bar
appears under the header. Use `/preview` to open it, and `esc` to stop when it
looks right. For a new site, the agent builds the complete first version, checks
one desktop and one mobile view, then waits for your feedback before iterating.
Browser screenshots stay in memory and are not saved in the project.

## What it does

- **Reads and writes code** — read, write, edit, multi-edit, atomic multi-file patches, and regex or literal replacement across files.
- **Runs commands** — shell, managed background jobs, tests, builds, and project verification.
- **Checks sites** — `verify_site` runs the project check and audits pages for console errors, failed requests, broken links and anchors, missing assets, syntax errors, and overflow; unchanged pages are cached.
- **Searches** — glob, grep, AST symbols, semantic search, and web search/fetch.
- **Understands code** — language-server definitions, references, diagnostics, completions, rename, and AST-aware code actions.
- **Sees** — screenshots, images, video, audio, and documents on models that support them.
- **Creates** — image generation and editing, plus batched browser automation.
- **Plans** — a persistent task ledger with dependencies, checks, structured reports, and continuous scheduling: a slow step does not hold back ready work, and timed-out or transient steps retry once.
- **Delegates** — parallel workers in isolated git worktrees, with independent review and three-way merge. A worker that needs a decision returns `needs_input` with questions and can be resumed in the same session and worktree.
- **Recovers** — retries transient network failures, repairs rejected thinking-mode histories, and keeps long sessions usable.
- **Caches** — context pruning and summarization, checkpoints, and resumable sessions.

## Coming next

- Images in the terminal
- Premade environments for specific kinds of tasks
- Agent observability
- Source tracking
- Personalisation

## Parallel agents

Workers are full-access by default. They can read, write, edit, run commands,
use the network, browse, generate images, and use the coding/research toolset.
Orchestration tools and `question` stay with the lead.

- `delegate_many` runs independent workers in one wave. Tasks can declare `id` and `depends_on` to form a DAG; dependents wait for their upstream reports, which are added to their context.
- `run_plan` executes the ledger as a dependency graph with continuous scheduling. Free slots start newly-ready work instead of waiting for a slow step; writers can review and merge while scouts are still running; long dependency chains are scheduled first; timed-out/transient steps retry once; and slots are shared between scouts and writers. `background=true` returns immediately, `cancel=true` stops the plan, and canceled or interrupted steps stay pending so a later `run_plan` resumes them.
- `delegate_write` gives writers explicit file ownership; writes outside the declared files are refused.
- `resume_worker` answers a worker that reported `needs_input` and continues the same session and worktree.
- `rename` and `refactor` use a real language server.
- Workers run in git worktrees when possible; warm worktrees are reused between steps, changes are reviewed and merged, and checks run once before merge.
- Token budgets are unlimited by default. `max_parallel_scouts` and
  `max_parallel_writers` limit concurrency.
- `check_policy` controls where checks run (`auto`, `worktree`, `merge`, `both`) and `review_policy` controls writer review (`always`, `auto`, `never`).

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

`websearch` runs the available backends in parallel — Brave, Serper, Exa, and
Tavily when configured, plus Bing RSS and DuckDuckGo HTML — then merges,
cross-checks, and ranks their results. Official docs, GitHub, and trusted
domains are boosted; tracking parameters are removed; adult and SEO-spam hits
are filtered; and a domain can contribute at most two results. Scraped backends
use strict safe search. Results are cached for ten minutes. Set
`EXA_API_KEY`, `TAVILY_API_KEY`, `BRAVE_API_KEY`, or `SERPER_API_KEY` before
starting Wombat to use your own search account:

```sh
export EXA_API_KEY=...
export TAVILY_API_KEY=...
export BRAVE_API_KEY=...
export SERPER_API_KEY=...
```

`webfetch` extracts the main article text with its title and description,
pretty-prints JSON, returns text/XML, and gives a clear note for PDFs and
binary payloads. It caches responses for repeated reads, retries transient
connection failures, and uses a 12-second HTTP client.

`imagesearch` returns direct image URLs from Google Images (through Serper),
Brave Images, DuckDuckGo Images, and Openverse, and can download the selected
images into the workspace. Set `SERPER_API_KEY` for Google Images or
`BRAVE_API_KEY` for Brave Images; DuckDuckGo and Openverse work without a key.

### Common settings

```sh
wombat config set provider openai
wombat config set model gpt-5
wombat config set mode create
wombat config set fallback_models gpt-5-mini
wombat config set scout_model gpt-5-mini
wombat config set worker_full_access false
wombat config set review_writers false
wombat config set check_policy auto
wombat config set review_policy always
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

regards
    ____
   

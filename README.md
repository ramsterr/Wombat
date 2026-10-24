
  <img width="2456" height="1192" alt="image" src="https://github.com/user-attachments/assets/3af03a01-9cce-4fe6-b60e-9105f41f4796" />




<p align="center">
  <a href="https://github.com/ramsterr/Wombat/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/ramsterr/Wombat/actions/workflows/ci.yml/badge.svg?branch=main&style=flat-square"></a>
  <a href="https://github.com/ramsterr/Wombat/releases"><img alt="Latest release" src="https://img.shields.io/github/v/release/ramsterr/Wombat?style=flat-square"></a>
  <a href="https://github.com/ramsterr/Wombat/blob/main/go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/ramsterr/Wombat?style=flat-square"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/github/license/ramsterr/Wombat?style=flat-square"></a>
</p>
 https://wombat-agent.vercel.app/
 
(wombat's site is made by wombat itself)



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
| `wombat mcp add/list/test` | add and check MCP servers |
| `wombat mcp login/logout` | sign in to an OAuth MCP server |
| `wombat checkpoint list` | list shadow-git checkpoints |
| `wombat checkpoint restore <id>` | restore a checkpoint |
| `wombat upgrade` | install the latest stable release |
| `wombat version` | print the version |

## Inside the chat
<img width="3350" height="1928" alt="image" src="https://github.com/user-attachments/assets/2c543b66-a075-4bcf-9857-a83c95b1326d" />


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
/imagen        enable image generation, or generate an image
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

# whats preview first?
agents are notorious for iterating for multiple minutes in the name of "improvement" and "polishing" without even making any considerable improvements.

wombat gives the user the power to review fast and feed back faster.
example:
<img width="766" height="194" alt="image" src="https://github.com/user-attachments/assets/c8ab105b-8a81-4054-b054-e7f661a5e682" />


Image generation is off by default. Use `/imagen` to enable it for the session,
or `/imagen <prompt>` to generate an image directly. The `imagegen` tool is not
offered to the model until you opt in.

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

## See other features

The features you expect from a serious coding agent, and the parts that make
Wombat different.

### Standard

| Feature | What it does |
|---|---|
| Code editing | Read, write, edit, multi-edit, atomic multi-file patches, and literal/regex replacement. |
| Shell and jobs | Run commands, manage background jobs, run tests and builds. |
| Git | Status, diff, log, show, and blame as structured read-only tools, including inside worker worktrees. |
| Verification | `verify`, `verify_site`, browser assertions, and project checks. |
| Code intelligence | Language-server definitions, references, diagnostics, completions, rename, and refactor. |
| Search | Glob, grep, AST symbols, and semantic search. |
| Web | Search and fetch with caching, retries, readable-text extraction, and page excerpts. |
| MCP | Add local stdio or remote HTTP/SSE Model Context Protocol servers, with interactive OAuth; their tools appear as `mcp__<server>__<tool>`. |
| Browser | Local and web automation, screenshots, clicks, typing, scrolling, and JS evaluation. |
| Media | Read images, video, audio, and documents; edit and generate images when enabled. |
| Sessions | Resumable sessions, browsing, continuation, and JSONL transcripts. |
| Context | Automatic pruning, summarization, compaction, and token accounting. |
| Checkpoints | Shadow-git snapshots with list and restore. |
| Permissions | Per-class gates for bash, writes, edits, network, sensitive reads, and each MCP server. |
| Providers | OpenAI-compatible APIs, Anthropic, OpenCode, and local model servers. |
| TUI basics | Command palette, file picker, history, copy, themes, and scrollback. |

### Unique

| Feature | What it does |
|---|---|
| Parallel fleet | Lead plus scouts and writers in isolated git worktrees; changes are reviewed and three-way merged. |
| Continuous plan scheduler | A dependency DAG where free slots start newly-ready work, slow steps do not block independent work, and interrupted steps resume. |
| Resumable workers | A worker that needs a decision returns `needs_input` and can be resumed in the same session and worktree. |
| Visual hand-off gate | After the first complete visual artifact, Wombat asks `Looks good` / `Needs changes` before another pass; headless runs stop after three visual passes. |
| Multi-preview system | Detects local servers, HTML, PDFs, images, and video; de-duplicates by origin or path; keeps an ordered left rail and a `/previews` picker. |
| Source browser | `/sources` browses every site and file the session used, with counts and open/attach actions. |
| Project references | `ctrl+r` / `/reference` keeps files, folders, and URLs in a per-project inspector list under `~/.wombat/references/`. |
| Inspector | Live session facts, context gauge, previews, sources, recent tools, references, and a pinned WOMBAT mark. |
| Search ranking | Merges Brave, Serper, Exa, Tavily, Bing RSS, DuckDuckGo, and Mojeek; boosts trusted docs, engine position, and cross-engine hits; filters adult/SEO spam; caps domains. |
| Image search | Finds direct image URLs via Google Images (Serper), Brave, DuckDuckGo, and Openverse, and downloads them into the workspace. |
| Opt-in image generation | `imagegen` is hidden until `/imagen`; then `/imagen <prompt>` generates directly. |
| Provider setup | `/configure` opens a base URL/API-key form; unconfigured models stay selectable but show `no key` and are blocked from chat. |
| Reasoning picker | After selecting a reasoning model, choose `off`, `low`, `medium`, `high`, or `max`; `/reasoning` reopens it. |
| Themes | Fifteen palettes, a two-column picker, `ctrl+t` cycling, and a reclaimed terminal background on exit. |
| Copy | `ctrl+y` for the last reply and `/copy` for any user, agent, or tool output, with an OSC 52 fallback. |
| File picker | `@` path completion with directory navigation and quoted paths for spaces. |
| Built to be driven | Headless `wombat run`, scriptable sessions, and a TUI designed around the inspector, preview rail, and keyboard flow. |

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
Tavily when configured, plus Bing RSS, DuckDuckGo HTML, and Mojeek RSS — then
merges, cross-checks, and ranks their results. It accepts 2–4 query variants,
`freshness`, `language`, `region`, and pagination; searches retry transient
failures and allow up to 15 seconds, with a 5-second grace window after the
first backend replies. By default it also fetches the top three pages and adds
a relevant excerpt plus any date and author it can find; `depth=deep` fetches
up to six pages and `depth=fast` skips page fetching. Official docs, GitHub,
and trusted domains are boosted, engine position and Exa results carry extra
weight, tracking parameters are removed, adult and SEO-spam hits are filtered,
and a domain can contribute at most three results. Backend failures are
reported instead of hidden. Scraped backends use strict safe search. Results
are cached for ten minutes. Set `EXA_API_KEY`, `TAVILY_API_KEY`,
`BRAVE_API_KEY`, or `SERPER_API_KEY` before starting Wombat to use your own
search account:

```sh
export EXA_API_KEY=...
export TAVILY_API_KEY=...
export BRAVE_API_KEY=...
export SERPER_API_KEY=...
```

`webfetch` extracts the main article text with its title and description,
pretty-prints JSON, returns text/XML, and gives a clear note for PDFs and
binary payloads. It caches responses for repeated reads, retries transient
connection failures, and shares a 15-second HTTP client with search.

`imagesearch` returns direct image URLs from Google Images (through Serper),
Brave Images, DuckDuckGo Images, and Openverse, and can download the selected
images into the workspace. Set `SERPER_API_KEY` for Google Images or
`BRAVE_API_KEY` for Brave Images; DuckDuckGo and Openverse work without a key.

### MCP servers
example:
<img width="2276" height="788" alt="image" src="https://github.com/user-attachments/assets/7142dd92-eaf4-4668-a4a3-cb3ae5d5b21f" />


Wombat is an MCP client. Add local stdio servers or remote Streamable HTTP/SSE
servers; their tools appear to the agent as `mcp__<server>__<tool>`.

**Commands**

```sh
# local stdio
wombat mcp add playwright -- npx -y @playwright/mcp@latest
wombat mcp add github --env GITHUB_TOKEN=... -- \
  npx -y @modelcontextprotocol/server-github

# remote HTTP with a static token
wombat mcp add context7 --url https://mcp.context7.com/mcp
wombat mcp add github --url https://api.githubcopilot.com/mcp/ \
  --header 'Authorization=Bearer ${GITHUB_TOKEN}'

# manage and inspect
wombat mcp list
wombat mcp test [name...]
wombat mcp enable <name>
wombat mcp disable <name>
wombat mcp remove <name>
wombat mcp import .mcp.json
```

`wombat mcp test` connects, lists the server's tools, and shows errors and
server stderr. Startup defaults to 15 seconds; use `--timeout 60` for a slow
first `npx` download and `--call-timeout 600` for slow tools. `import` accepts
the Claude Code/Cursor `mcpServers` format and the OpenCode `mcp.servers` format.

**Interactive OAuth**

```sh
wombat mcp add notion --url https://mcp.notion.com/mcp
wombat mcp login notion
wombat mcp test notion
wombat mcp logout notion
```

`mcp login` opens the browser, completes a loopback OAuth callback with PKCE
and state checks, and stores access and refresh tokens in
`~/.wombat/mcp-auth/` with `0600` permissions. Tokens refresh automatically.
Agent startup uses cached tokens only and reports
`run wombat mcp login <server>` when a login is missing. `logout`, updating, or
removing a server clears the saved login.

Use `"oauth": false` for API-key servers, or set `oauth_client_id`,
`oauth_client_secret`, `oauth_scopes`, and `oauth_redirect_url` for a
pre-registered client. Remote MCP endpoints must use HTTPS unless they are
loopback. Some providers restrict OAuth registration to approved clients; use a
PAT-based or local server variant instead.

**In chat**

`/mcp` shows each attached server, its tools, status, and this session's call
counts.

**Config and permissions**

Servers live under `mcp_servers` in `~/.wombat/config.json`:

```json
{
  "mcp_servers": {
    "filesystem": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."],
      "class": "read"
    },
    "context7": {
      "type": "http",
      "url": "https://mcp.context7.com/mcp",
      "headers": {"Authorization": "Bearer ${CONTEXT7_API_KEY}"}
    }
  }
}
```

`${VAR}`, `${VAR:-default}`, and `{env:VAR}` expand when the server starts, so
secrets can stay in the environment. MCP calls are gated by
`mcp:<server>`, falling back to `mcp`, which defaults to `ask`:

```sh
wombat config set permissions.mcp allow
wombat config set permissions.mcp:github ask
```

Use `--class read` to let delegated scouts use a read-only server; other servers
stay lead-only unless you pass `--workers`. Local servers run as your user with
your environment, so only add servers you trust.

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

**Connection reset by peer**

Wombat retries transient connection errors automatically, including after part
of a response has streamed. It clears the partial output, retries with backoff,
and asks the model to continue if the provider retries are exhausted. If it
still fails, check your network, proxy, or VPN and retry.

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
   

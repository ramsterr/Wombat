## Wombat
<img width="2872" height="676" alt="Wombat" src="https://github.com/user-attachments/assets/53857c41-0947-4be9-b90f-72dd92ab9f99" />

[![CI](https://github.com/ramsterr/Wombat/actions/workflows/ci.yml/badge.svg)](https://github.com/ramsterr/Wombat/actions/workflows/ci.yml)
[![Version](https://img.shields.io/badge/version-0.2.5-blue)](https://github.com/ramsterr/Wombat)
[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](#install)
[![Binary](https://img.shields.io/badge/binary-~11%20MB-brightgreen)](#install)
[![Runtime deps](https://img.shields.io/badge/runtime%20deps-none-success)](#install)
[![Multimodal](https://img.shields.io/badge/multimodal-images%20%7C%20video%20%7C%20audio%20%7C%20files-blueviolet)](#attachments)
[![Bubble Tea](https://img.shields.io/badge/built%20with-Bubble%20Tea-ff69b4)](https://github.com/charmbracelet/bubbletea)
[![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen)](#contributing)

Wombat is a lightweight, chat-first coding agent for the terminal. One self-contained binary, fast startup, small memory footprint.

It runs an agentic loop against any OpenAI-compatible API, Anthropic, or a local model server (Ollama, LM Studio, llama.cpp, vLLM, MLX). Gemini and OpenAI Responses models are supported through OpenCode. It reads, writes, edits, and searches files, runs shell commands, streams tool steps and model reasoning, and keeps long sessions under the model window with automatic context pruning and summarization.

Run `wombat` with no arguments to open the full-screen TUI: streaming chat, inline tool calls and permission prompts, a session browser with transcript previews, a filterable model picker, a connectivity dashboard, and usage charts.

## Contents

- [Features](#features)
- [Install](#install)
- [Updating](#updating)
- [Modes](#modes)
- [Slash commands](#slash-commands)
- [Quickstart](#quickstart)
- [The TUI](#the-tui)
- [Commands](#commands)
- [Attachments](#attachments)
- [Providers](#providers)
- [Tools and permissions](#tools-and-permissions)
- [Guardrails](#guardrails)
- [Context management](#context-management)
- [Sessions](#sessions)
- [Configuration](#configuration)
- [Orchestration](#orchestration)
- [Examples](#examples)
- [Evals](#evals)
- [Architecture](#architecture)
- [Contributing](#contributing)

## Features

- **Chat-first terminal UI** with streaming replies, inline tool steps, and permission prompts. Reasoning is shown for thinking models, persisted with the turn, and replayed where the provider requires it.
- **Multimodal input** — images, video, audio, and documents, wherever the selected model supports them.
- **Resumable sessions** stored as JSONL, keyed to the working directory, with a browsable history and transcript previews.
- **14 provider presets** plus any OpenAI-compatible endpoint; automatic protocol routing for OpenCode Zen and Go.
- **Useful tools** — read, write, edit, atomic patches, shell and background jobs, search, semantic code intelligence, browser automation and screenshots, image generation, and project verification — with per-class allow/deny/ask permissions.
- **Safety guardrails** — secrets, `.env` files, destructive commands, git history, and security-sensitive code are covered by built-in rules and warnings.
- **Two working modes** — `chat` explores and explains without touching anything; `create` writes and edits. Switch with `/mode`.
- **It just works** — no sandbox, no caps, no permission prompts by default: the agent reads, writes, and runs commands. Ask before you act by setting a permission to `ask`. File mutations are snapshotted, so you can undo with `wombat checkpoint restore`.
- **Two-tier context management** — prune old tool output, then summarize older history, so long sessions keep working.
- **Task ledger, verification, and delegation** — the lead agent keeps a persistent plan, records checks and evidence, runs bounded read-only scouts, and can hand independent coding tasks to writers in isolated git worktrees.
- **Check-gated parallel writers** — writers own disjoint files, run in worktrees, and merge sequentially only after their offers pass tests; conflicts and failures are kept for inspection.
- **No runtime dependencies on macOS and Linux** — one self-contained binary; no Python, Node, or external service required.

## Install

One self-contained binary, about 11 MB. No runtime dependencies, no Docker, no Python, no Node.

### First time? Pick one line

**macOS, Linux, WSL2, or Git Bash**

```sh
curl -fsSL https://raw.githubusercontent.com/ramsterr/Wombat/main/install.sh | bash
```

**macOS or Linux with Homebrew**

```sh
brew tap ramsterr/wombat && brew install ramsterr/wombat/wombat
```

**Anywhere with Go 1.26+**

```sh
go install github.com/ramsterr/Wombat/cmd/wombat@latest
```

Then check it works:

```sh
wombat version      # wombat 0.2.5
wombat setup        # pick a provider, paste your API key, pick a model
wombat              # start chatting
```

The install script picks the right build for your machine, checks its SHA-256
checksum, and puts the binary in `~/.local/bin`. If `wombat` is "command not
found", add that folder to your PATH:

```sh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc   # zsh
```

Other options: `PREFIX=/usr/local/bin` to install elsewhere, or
`VERSION=0.2.5` to pin a version. To uninstall, delete the binary and, if you
want, `~/.wombat/`.

**If `wombat version` shows an old version**, you have more than one copy on
your PATH. Find them and delete the old ones:

```sh
which -a wombat
```

### Windows

Wombat builds natively as `wombat.exe`; the TUI, sessions, providers, and file tools work on Windows. The `bash` tool runs commands through a POSIX `sh`, so use **WSL2** or **Git Bash**. Native PowerShell/cmd is not fully supported yet: chat and file editing work, but shell commands fail without `sh` on `PATH`.

#### WSL2 (recommended)

Install Ubuntu once from an elevated PowerShell:

```powershell
wsl --install
```

Then, inside the Ubuntu terminal (Windows Terminal works well):

```sh
sudo apt update && sudo apt install -y git curl
curl -fsSL https://raw.githubusercontent.com/ramsterr/Wombat/main/install.sh | bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc

wombat version
```

Run `wombat` from the same Ubuntu terminal. Repos on Windows drives are available under `/mnt/c/...` (for example `/mnt/c/Users/you/code/app`); keeping projects in the WSL home directory is faster. Config and sessions live in `~/.wombat/` inside WSL.

WSL shares the Windows clipboard through WSLg, but image paste also needs `wl-paste` or `xclip`. If it is unavailable, use `/attach /mnt/c/Users/you/Pictures/image.png` instead.

#### Git Bash (native `wombat.exe`)

Install [Go for Windows](https://go.dev/dl/) and [Git for Windows](https://git-scm.com/download/win), then run this in **Git Bash**:

```sh
curl -fsSL https://raw.githubusercontent.com/ramsterr/Wombat/main/install.sh | bash
~/.local/bin/wombat.exe version
```

The script detects Windows and unpacks the release archive, so `unzip` must be available. It installs to `~/.local/bin`; add that folder to your user `PATH` (or copy the binary to `~/bin`). Or download `wombat_<version>_windows_amd64.zip` from the [releases page](https://github.com/ramsterr/Wombat/releases) and unzip it yourself.

Or build from source with [Go for Windows](https://go.dev/dl/):

```sh
git clone https://github.com/ramsterr/Wombat.git
cd Wombat
go build -o ~/bin/wombat.exe ./cmd/wombat
```

`~/bin` maps to `%USERPROFILE%\bin`. Add that folder to your user `PATH`, or call the binary by path. Launch Wombat from Git Bash so `sh.exe` is available to the `bash` tool. Config and sessions live in `%USERPROFILE%\.wombat\`.

Clipboard image paste is not implemented on Windows; attach an image by path instead:

```text
/attach C:\Users\you\Pictures\screenshot.png
```

To uninstall, delete `wombat.exe` and, if you want, the `%USERPROFILE%\.wombat` folder.

## Updating

### Already have wombat? One command

```sh
wombat upgrade
```

That is all. It finds the newest release, checks the checksum, and replaces
your binary. It works no matter how you installed Wombat — install script,
Homebrew, or `go install` — because it looks at where the binary lives.

```sh
wombat upgrade --check   # just tell me if there is a newer version
wombat doctor            # shows the update status as well
```

Prefer your package manager? Use the same one you installed with:

```sh
brew upgrade ramsterr/wombat/wombat                                                  # Homebrew
go install github.com/ramsterr/Wombat/cmd/wombat@latest                              # Go
curl -fsSL https://raw.githubusercontent.com/ramsterr/Wombat/main/install.sh | bash   # script
```

Set `WOMBAT_NO_UPDATE_CHECK=1` to stop Wombat from ever checking for new releases.

## Quickstart

```sh
wombat                 # open the chat TUI
wombat setup           # provider + API key + model wizard (runs on first launch)
wombat doctor          # check config, permissions, and connectivity
wombat run "add tests for the parser and run them"
wombat sessions        # list sessions for this directory
wombat resume last     # continue the latest session in the TUI
```

On a fresh install the setup wizard runs once, then the chat opens. Configure a key once and it is reused. Everything also works without a TTY: `wombat chat`, `wombat run`, and piped input fall back to a line-based REPL.

## Modes

Two working modes, switchable at any time:

| mode | what the agent may do |
|---|---|
| **chat** | look and explain: read, glob, grep, and run inspection commands. It cannot create, edit, or delete anything. |
| **create** | the full agent: read, write, edit, and run any command you approve. |

```sh
/mode chat       # explore and explain, change nothing
/mode create     # read, write, edit, and run any command
/modes           # list the modes
```

The mode is shown in the header and told to the model, so it does not try to
change files in chat mode — it asks you to switch instead. The default comes
from `wombat config set mode chat|create`.

## Slash commands

Type `/` in the chat and the command list appears as you type — arrow keys to
move, tab to complete, esc to dismiss. In the line REPL, `/help` prints the
same list, and an unknown `/command` shows the closest matches.

| command | what it does |
|---|---|
| `/mode`, `/modes` | switch or list the working modes |
| `/thinking on\|off` | show or hide the model's thinking |
| `/toolset` | the tools this session can use, and how each is gated |
| `/sources` | the sites and files this session used |
| `/history` | the messages in this session |
| `/sessions` | browse past sessions |
| `/status` | the current settings |
| `/cost` | token usage and cost |
| `/model`, `/provider`, `/models` | switch the model or provider |
| `/attach`, `/paste`, `/detach` | attachments |
| `/new`, `/compact`, `/clear` | session and context management |
| `/yolo` | toggle auto-approve |
| `/setup`, `/help`, `/exit` | the rest |

## The TUI

Type a message and press `enter`. Responses stream in; tool calls appear as they run; reasoning-capable models show their thinking above the answer.

| key | action |
|---|---|
| `enter` | send |
| `alt+enter` / `ctrl+j` | newline |
| `esc` | interrupt the current run |
| `pgup` / `pgdn`, `ctrl+u` / `ctrl+d` | scroll the transcript |
| `up` / `down` | scroll one line when the input is empty |
| `ctrl+v` | attach an image from the clipboard (macOS/Linux) |
| `ctrl+c` | quit (interrupts while running) |
| `/mouse` | toggle mouse: wheel scroll vs. native text selection |
| `/` | slash commands |

The mouse is **off by default** so drag-select and your terminal's copy shortcut work normally. `/mouse` enables wheel scrolling; hold Option or Shift to select text while it is on.

Permission prompts accept `y` (allow), `a` (always allow this class), `n` (deny), or `f` (deny with instructions the model will see).

### Slash commands

| command | action |
|---|---|
| `/help` | list commands |
| `/model [name]` | switch model (no name opens the picker) |
| `/provider [name]` | switch provider |
| `/models` | browse models |
| `/sessions` | browse and resume sessions |
| `/attach <path>` | attach an image, video, audio, or file |
| `/paste` | attach an image from the clipboard (macOS/Linux) |
| `/detach [n\|all]` | remove pending attachments |
| `/new` | start a fresh session |
| `/compact` | compact context now |
| `/cost` | token usage and cache hits |
| `/status` | current provider, model, session, and directory |
| `/yolo` | toggle auto-approve for tools |
| `/mouse` | toggle mouse mode |
| `/clear` | clear the transcript view |
| `/setup` | reconfigure providers and models |
| `/exit` | quit |

## Commands

| command | what it does |
|---|---|
| `wombat` | full-screen chat TUI |
| `wombat chat` | same TUI; line REPL when stdin is not a TTY |
| `wombat resume [id\|last]` | resume a session in the TUI |
| `wombat run "<task>"` | one-shot headless run (exit code reflects failure) |
| `wombat run -c "<task>"` | continue the latest session headless |
| `wombat setup` | provider, API key, and model wizard |
| `wombat models [provider]` | list models from the provider |
| `wombat sessions` | list sessions for this directory |
| `wombat doctor` | config, permissions, and connectivity checks |
| `wombat config` | show config with API keys redacted |
| `wombat config set <key> <value>` | set `provider`, `model`, `mode`, writers, or a permission |
| `wombat checkpoint list` | list snapshots for the latest session |
| `wombat checkpoint restore <id>` | restore a snapshot (`--yes` for non-interactive use) |
| `wombat upgrade` | update wombat to the latest release (`--check` to only look) |
| `wombat version` | print the version |

Flags for `run` and `resume`: `-m <model>`, `-p <provider>`, `--yolo` (auto-approve tools), `-c` (continue latest), `--quiet`, and for `run` also `--image`, `--video`, `--audio`, and `--file` (repeatable).

Line REPL commands: `/model`, `/models`, `/provider`, `/new`, `/compact`, `/cost`, `/status`, `/yolo`, `/mode`, `/help`, `/exit`.

## Attachments

Send images, video, audio, and documents to any model that supports them. Attach files by:

- typing `@path/to/file` inside a message (`@"path with spaces"` when needed)
- using `/attach <path>` in the TUI — pending files appear above the input; `/detach [n|all]` removes them
- pressing `ctrl+v` or running `/paste` to grab an image from the system clipboard (macOS, or Linux with `wl-paste`/`xclip`; on Windows use `/attach <path>`)
- pasting a media file path on its own line
- passing `--image`, `--video`, `--audio`, or `--file` to `wombat run`

```sh
wombat run --image screenshot.png "what is broken in this layout?"
# or, inside the TUI:
@design/mock.png make the page match this
```

Files are copied into the session store at `~/.wombat/sessions/<project-hash>/.attachments/` and referenced from the transcript, so history and `wombat resume` keep working. Each file is capped at 20 MB; text-like files are folded into the prompt, and everything else is sent as native media. Gemini inlines at most about 14 MB per request, so shrink or split large files.

Wombat resolves model capabilities from the [models.dev](https://models.dev) catalogue — attachment support, context and output limits, reasoning, tool calling, and the OpenCode wire protocol — cached in the OS cache directory and refreshed at most once a day, so a new model works without waiting for a Wombat update. When no catalogue entry is available it falls back to built-in model families.

| provider | images | video | audio | documents |
|---|---|---|---|---|
| Anthropic / Claude | yes (png, jpeg, gif, webp) | no | no | PDF, text |
| OpenAI Chat Completions (`gpt-4o`, compatible servers) | yes | no | wav, mp3 (audio models) | text only |
| OpenAI Responses (GPT and o-series via OpenCode) | yes | no | audio models | PDF, text |
| Google Gemini (via OpenCode) | yes | yes | yes | PDF and most documents |
| Ollama | vision models only | no | no | text only |
| OpenCode Zen / Go | follows the routed protocol above | | | |

If the selected model does not accept an attachment, the provider returns an explicit error; switch to a multimodal model and resend.

## Providers

Presets: `openai`, `anthropic`, `openrouter`, `deepseek`, `groq`, `xai`, `mistral`, plus local `ollama`, `lmstudio`, `llamacpp`, `vllm`, and `mlx` — and any custom OpenAI-compatible endpoint.

API keys resolve in order: config file → preset environment variable (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, …) → `<PROVIDER>_API_KEY`. Local servers need no key.

### Image generation

`imagegen` uses an OpenAI-compatible image endpoint. By default it uses the `openai` provider, `gpt-image-1`, and the same key resolution as chat. Point it at another provider or model with `wombat config set image_provider <name>` and `wombat config set image_model <model>`, or override with `WOMBAT_IMAGE_PROVIDER`, `WOMBAT_IMAGE_BASE_URL`, `WOMBAT_IMAGE_API_KEY`, and `WOMBAT_IMAGE_MODEL`. `generate` creates an asset; `edit` takes an input image and optional mask. Both save into the workspace and attach the result for review.

`semantic_search` finds code by meaning instead of exact text. It indexes the workspace into line chunks, caches vectors outside the repository, and re-embeds only files that changed. Point it at any OpenAI-compatible embedding endpoint with `wombat config set embedding_provider <name>` and `wombat config set embedding_model <model>` (for example `openai` with `text-embedding-3-small`, or `ollama` with `nomic-embed-text`), or override with `WOMBAT_EMBED_PROVIDER`, `WOMBAT_EMBED_BASE_URL`, `WOMBAT_EMBED_API_KEY`, and `WOMBAT_EMBED_MODEL`. With no embedding provider configured it uses a deterministic offline embedder, so the tool still works without network access.

`browser` drives a local Chromium browser over the DevTools protocol. It finds Chrome, Chromium, Edge, Brave, Playwright, or Puppeteer installations; set `WOMBAT_BROWSER` to choose an executable. Open the page, interact with it, and take a screenshot that is attached to the conversation, so frontend work is verified visually instead of guessed from HTML.

### Reasoning models

For thinking models, reasoning is streamed into the UI and saved with the assistant turn. When a provider requires it, Wombat replays that reasoning in follow-up requests: `reasoning_content` for OpenAI-compatible thinking APIs (GLM, DeepSeek), encrypted reasoning items for the OpenAI Responses API, and thought signatures attached to Gemini tool calls. This keeps multi-step tool loops valid without leaking reasoning into later prompts when a model does not need it.

Tool signatures and reasoning items are stored in the session JSONL, so a resumed session can continue a thinking-mode tool loop without a 400 from the provider.

### OpenCode Zen and Go

`opencode` ([Zen](https://opencode.ai/zen), pay-as-you-go) and `opencode-go` ([Go](https://opencode.ai/go), subscription) are supported natively. Each model is served on a different protocol, and Wombat routes automatically:

| family | protocol |
|---|---|
| GLM, Kimi, DeepSeek, MiMo, LongCat, Qwen Max, … | OpenAI chat completions |
| Claude, MiniMax, Qwen Plus/Flash | Anthropic messages |
| GPT, Grok, Muse | OpenAI Responses API |
| Gemini | Google generateContent |

Unknown models start on chat completions and fall back across protocols if the gateway reports a protocol mismatch; the winning route is cached per model. Wombat sends the `User-Agent` and `x-opencode-session` headers the Go gateway expects for routing and prompt caching — the same session ID is reused across turns, so cache hits show up in `/cost`.

If the OpenCode app is installed on the same machine, `wombat setup` detects its stored credentials and offers to reuse them — no key to paste. Note: `*-free` models are restricted to the OpenCode app and cannot be used from third-party clients.

## Tools and permissions

| tool | class | notes |
|---|---|---|
| `read` | read | line-numbered, offset/limit; attaches images and refuses binary files |
| `write` | write | creates parent directories; shows a diff before writing |
| `edit` | edit | exact replace, then CRLF/whitespace-tolerant and code-token matching; shows a diff |
| `multi_edit` | edit | several edits to one file, applied atomically |
| `apply_patch` | edit | atomic multi-file patches, validated then rolled back on failure |
| `bash` | bash | no default timeout; `background` starts a managed job |
| `glob` | read | `**` patterns; skips `.git` and `node_modules` |
| `grep` | read | pure-Go search; skips credentials files and redacts output |
| `symbols` | read | Go AST definitions/references plus language-specific declarations |
| `lsp` | read | language-server definition, references, diagnostics, completion, symbols, and outline |
| `browser` | network | drive Chromium: open, click, type, scroll, eval, resize, and screenshot |
| `imagegen` | write | generate or edit an image and attach it for visual review |
| `rename` | edit | language-server rename, with a combined diff and atomic application |
| `refactor` | edit | AST-aware language-server refactorings (extract, inline, organize imports, quick fixes) |
| `semantic_search` | read | find code by meaning with embeddings; cached, incremental index |
| `batch` | read | several `read`/`grep`/`glob` calls in one round-trip |
| `verify` | bash | runs the project's own tests, build, typecheck, lint, and format |
| `jobs` / `job_output` / `job_kill` | read/bash | list, stream, and stop background processes |
| `webfetch` | network | fetch a URL and return readable text |
| `websearch` | network | search the web and return ranked results |
| `question` | ask | ask the user to choose, with 2-3 options or free text |
| `update_plan` | plan | create/replace the ledger, steps, and checks |
| `run_checks` | verify | run a step's checks and record evidence |
| `delegate` / `delegate_many` | delegate | read-only investigation workers |
| `delegate_write` | write | isolated git worktree writers (opt-in in config) |

`lsp` asks the server for definitions, references, diagnostics, completions, symbols, and outlines; `rename` and `refactor` apply the server's semantic edits. They use whichever language server is installed: `gopls` for Go, `typescript-language-server` for TypeScript/JavaScript, `pyright` or `pylsp` for Python, `rust-analyzer`, `clangd`, `jdtls`, `solargraph`, `intelephense`, `lua-language-server`, `csharp-ls`, `sourcekit-lsp`, `kotlin-language-server`, and `bash-language-server`. Override a server with `WOMBAT_LSP` or per language with `WOMBAT_LSP_GO`, `WOMBAT_LSP_PY`, and so on. If no server is installed the tool says so; `symbols` and `semantic_search` still work as fallbacks.

Permissions are set per class to `ask`, `allow`, or `deny`. `bash`, `write`, `edit`, and `network` default to `allow`; `dangerous` and `sensitive-read` default to `ask`, so destructive commands and credentials files are never read automatically. Read is otherwise allowed. In non-interactive runs, `ask` behaves as `deny` unless `--yolo` is passed. Credentials files are also skipped by `grep` and redacted from tool output.

## Craft standard

The system prompt tells the agent to aim at a professional result and to
**research before it builds**:

> Aim at what the best practitioners in the field achieve, not at your first
> attempt.

Concretely, the agent is told to look at how the best people in the field do
the work — established libraries, official docs and design systems, curated
assets, well-known open-source projects — and to learn from their craft
(proportions, palette, typography, spacing, motion, error states,
accessibility, performance). It prefers proven, licensed sources over
inventing from scratch, credits what it reuses, reads the repository's own
`examples/` directory first, and looks at its own output before claiming it
works.

## Looking at images

The agent can look at images it finds, not only ones you paste. It downloads
one (with `curl`, or any tool) and then reads it:

```sh
curl -sL -o /tmp/cat.png https://example.com/cat.png
```

Reading an image attaches it to the agent's next message, so a
vision-capable model actually sees it — the same path a pasted image takes.
A large image is downscaled before it is attached. Text models simply get a
note that the file is an image.

## Speed

Wombat is built to be fast, and the changes below come from how the quick
harnesses do it:

- **Tool calls run in parallel.** Independent read-only calls (`read`,
  `glob`, `grep`, and bash that only inspects) run at the same time instead of
  one after another; anything that can change the workspace runs on its own,
  in order. Tool execution is roughly half of an agent's wall-clock time, so
  this is the biggest single win.
- **The history stays small.** Only the newest assistant turn keeps its
  reasoning in the request, and tool results are trimmed hard, so every
  request is cheap to prefill.
- **A stuck agent is stopped.** The same call repeated with the same
  arguments three times ends with "stop repeating it", instead of burning the
  turn.
- **A script beats a pile of reads.** The prompt asks the agent to compute
  answers with one command rather than reading twenty files into its context.
- **Parallel scouts, by default.** For anything past two steps the agent
  dispatches a fleet of two to five workers in one `delegate_many` call, each
  in its own context, so the lead's context stays small. When your plan has
  independent steps, the prompt points that out. Turn it off with
  `wombat config set parallel_agents false`.
- **A scratch directory.** Each session gets `~/.wombat/tmp/<session>` and
  `$WOMBAT_TMP` points there, so temporary files never land in the project or
  in a checkpoint.
- **Web tools.** `websearch` and `webfetch` are first-class tools, so research
  does not need a shell round-trip or a permission prompt for every `curl`.
  The read-only workers get them too.
- **One call instead of many.** `batch` runs several reads and searches in a
  single round-trip, and `multi_edit` applies several edits to one file at
  once. A big search result is written to a file with a pointer instead of
  being carried in the model's context, so the model reads only the part it
  needs.
- **Cheaper scouts.** The read-only workers can run on a different, cheaper
  model than the lead, which is the biggest cost lever because the workers do
  the reading:

  ```sh
  wombat config set scout_model gpt-5-mini
  ```

## Asking you, instead of guessing

The agent has a `question` tool. When the result depends on a choice only you
can make - which direction, which option, what "good" means here - it asks
instead of picking for you:

```
? Which direction should the poster take?
  1) Editorial / magazine - Bold serif headline, high contrast.
  2) Minimal / gallery    - Lots of white space, small type.
  (number, or type your own answer): 1
```

In the TUI, press `1`-`9` to pick, `f` to type your own answer, or `esc` to
skip. The answer comes back to the model, which then acts on it.

The prompt also tells the agent to **hand the work back when it is done** rather
than keep refining on its own: another round of its own guesses is slower and
usually worse than one question.

## When the workspace is your home directory

Checkpoints walk the working directory. If your workspace is `~`, that walk
meets directories macOS hides from ordinary processes (`.Trash`, `Library`),
and the home directory holds far more than a checkpoint can hold. Neither
stops you now: hidden directories are skipped, an unreadable directory is
recorded as skipped, and a snapshot that outgrows its limits captures what it
can and stops early instead of failing. A restore only puts back the files it
captured, so nothing is lost.

Point Wombat at a project directory rather than your home directory when you
can - it is faster and the checkpoints stay meaningful.

## Showing or hiding the thinking

The model's thinking is hidden by default — you get a one-line summary instead
of a wall of text. Turn it on when you want to see it:

```sh
/thinking on      # show the thinking as it arrives
/thinking off     # hide it again
```

The setting is saved. The thinking is always stored in the session, so
`/history` and a resumed session still show it.

## Parallel work

The prompt also tells the agent to **work in parallel**:

> For research, comparison, or anything more than two steps, fan out 2-5
> workers with delegate_many to investigate different angles at the same time,
> then synthesize their reports. That is faster and uses far fewer of your own
> tokens than working through it alone.

So a typical research task becomes one `delegate_many` call with two to five
scouts, each with a different angle, instead of the lead exploring one thread
at a time. Up to `max_parallel_scouts` run at once (five by default), so a
fleet finishes in a single wave.

## Guardrails

The system prompt carries the rules that shape the work: never reveal or
transmit secrets, never commit `.env` files, do not commit or push unless
asked, no destructive or irreversible commands without permission, write secure
code, and never disable a test to make a check pass. It also asks the agent to
**ask you instead of guessing** and to **hand the work back when it is done**
rather than polishing on its own.

The application-level gate is the permission system: `bash`, `write`, `edit`,
`outside` and `network` are all `allow` by default so the agent just works.
Set any of them to `ask` to be consulted first:

```sh
wombat config set permissions.bash ask
```

## Context management

Long sessions stay under the model window with two tiers:

1. **Prune** — old tool output is replaced with short placeholders. The last two user turns and roughly 40k tokens of recent tool output are protected.
2. **Summarize** — if the history is still too large, older messages are condensed into a structured summary while the most recent ~15k tokens stay verbatim.

Token usage, cache hits, and compaction events are reported live. Prompt caching works on Anthropic and OpenAI.

## Sessions

Sessions are append-only JSONL files under `~/.wombat/sessions/<project-hash>/`, keyed to the working directory. Every run is resumable with `wombat resume` or `/sessions` inside the TUI; attachments live beside the session in `.attachments/`. Task ledgers live beside sessions as `<session>.ledger.json`, so a resumed run keeps its plan. Checkpoints live in `<session>.checkpoints/`, and structured run events are written to `<session>.events.jsonl`. Deleting a session file removes its history; delete the ledger and checkpoint directory too to reset the plan and rollback history.

## Configuration

Config lives at `~/.wombat/config.json` (mode 0600). Overrides: `WOMBAT_HOME`, `WOMBAT_CONFIG`, `WOMBAT_PROVIDER`, `WOMBAT_MODEL`.

```json
{
  "provider": "ollama",
  "model": "qwen3:14b",
  "providers": {
    "ollama": {
      "kind": "ollama",
      "base_url": "http://127.0.0.1:11434",
      "num_ctx": 32768
    }
  },
  "permissions": {"bash": "allow", "write": "allow", "edit": "allow", "dangerous": "ask", "sensitive-read": "ask"},
  "fallback_models": ["gpt-5-mini"],
  "orchestrator": true,
  "max_parallel_scouts": 3,
  "checkpoints": true,
  "require_verification": true,
  "writers": true,
  "max_parallel_writers": 2,
  "mouse": false
}
```

`mouse` defaults to false so terminal text selection works; `/mouse` toggles it and saves the choice. `orchestrator` defaults to true; set it to false to run the original single-agent loop. There are no step, token, or time caps: the agent works until the task is done. `max_parallel_scouts` and `max_parallel_writers` bound how many run at once (five and two). Checkpoints are on by default, so any change can be undone.

`fallback_models` is an ordered list tried when the selected model is rate-limited, unavailable, or returns a server error. The model is only switched before any output has streamed, so a reply is never duplicated; an invalid request is reported instead of hidden by a fallback.

## Orchestration

The lead agent owns the plan, edits, commands, and final answer. For multi-step work it maintains a task ledger (`update_plan`) with a goal, durable facts, and steps that are `pending`, `in_progress`, `completed`, `blocked`, or `failed`. The ledger is injected into the system prompt and persisted next to the session, so plans survive resume and compaction.

`delegate` hands one self-contained investigation to a fresh worker with its own context and session. `delegate_many` runs two to five independent scouts concurrently — one wave, up to `max_parallel_scouts` at a time. Workers are read-only — they get `read`, `glob`, `grep`, `symbols`, `lsp`, `semantic_search`, `browser`, `batch`, `websearch`, and `webfetch`, cannot edit files, run mutating commands, prompt for credentials, or delegate further — and return a structured JSON report with a status, summary, findings, path:line evidence, tests, risks, and next steps. Worker usage is counted in the lead's totals and in the hard per-turn budget. Delegation is deliberately sparse: the lead should use it when exploration would flood its context, not for work it can do directly. Dependent steps run sequentially; only independent scouting tasks belong in one fleet.

### Checks, verification, and writers

Steps can declare `checks` in `update_plan`. `run_checks` executes them in the lead workspace, records pass/fail output in the ledger, and the completion gate refuses to mark a step completed while a check is pending or failing. Writers (`delegate_write`) are available in git repositories when `writers` is enabled. Each writer gets a context pack and a temporary worktree, must declare the exact files it owns, runs the step checks, and is committed before merge. Merge is sequential: the patch is applied to the main workspace, checks run again, `git diff --check` runs, and a failing or conflicting patch is reverted while the worktree is kept for inspection. Writers cannot overlap file ownership; use them for independent implementation steps, not dependent edits.

```sh
wombat config set orchestrator false        # disable ledger + delegation
wombat config set max_parallel_scouts 2     # concurrent scouts per wave
wombat config set worker_token_budget 80000 # per-worker token cap
wombat config set turn_worker_token_budget 160000 # all workers per turn
wombat config set permissions.bash ask     # ask before every command
wombat config set checkpoints false            # disable snapshots
wombat config set writers false                # disable worktree writers
```

Worker and writer transcripts are stored under `~/.wombat/workers/` for inspection. Writers run in `~/.wombat/worktrees/` by default; successful worktrees are removed, failures are kept. The lead remains the default writer and owns merge decisions.

## Examples

`examples/` groups reference tasks and outputs by genre — design, scraping, browser use, data visualization, and more. See [examples/README.md](examples/README.md) for the full list, including the Nash equilibrium visual, a Billboard chart, and an SVG cat.

## Evals

The project includes a reproducible eval harness. It runs a task in a fresh workspace, invokes the real agent loop, runs the task's checks, and writes a report with pass rate, tokens, latency, and estimated cost.

```sh
go run ./cmd/eval -tasks eval/tasks -dry-run      # validate without spending tokens
go run ./cmd/eval -tasks eval/tasks -out eval/results
go run ./cmd/eval -tasks eval/tasks -orchestrator=false   # single-agent comparison
```

Tasks are self-contained JSON fixtures under `eval/tasks/` (20 tasks today). The runner protects tests, `testdata`, dependency manifests, and CI files from agent edits, runs each task a configurable number of times, and reports a Wilson 95% confidence interval plus failure classes. See [eval/README.md](eval/README.md) for the schema, pricing flags, repeats, and baseline workflow.

## Architecture

```text
cmd/wombat        entry point
cmd/eval          eval harness CLI
internal/cli      REPL, headless run, setup wizard, doctor, checkpoints, upgrade
internal/agent    agent loop, system prompt, hooks, budgets, usage, compaction
internal/orchestrator  ledger, checks, read-only delegation, writers, merge queue
internal/worktree isolated git worktree manager and patch merge queue
internal/checkpoint workspace snapshots and scoped restore
internal/trace    append-only JSONL run events
internal/eval     task loader, protection, repeats, reports, pricing
internal/llm      provider adapters, HTTP errors, retry/backoff
internal/release  GitHub release lookup, checksums, version comparison
internal/tools    read/write/edit/bash/glob/grep, batch, multi_edit, web tools, permissions
internal/tui      Bubble Tea chat UI, sessions, models, usage, doctor
internal/session  JSONL session persistence and attachment store
internal/config   config load/save, presets, key resolution
internal/tokens   token estimation and model context windows
internal/clip     clipboard image readers
eval/tasks        self-contained eval fixtures
packaging         release process notes and the Homebrew tap updater
examples          genre-grouped tasks and reference outputs
```

## Contributing

Issues and pull requests are welcome. Before sending a change:

```sh
gofmt -w .
go vet ./...
go test ./...
go build -o /tmp/wombat ./cmd/wombat
```

CI runs the same checks on every push to `main` and on pull requests.

### Cutting a release

Wombat ships as GitHub releases. Pushing a `v<version>` tag builds the
binaries, publishes them with checksums, moves the rolling `stable` tag, and
refreshes the Homebrew tap when a tap token is configured. The full checklist
lives in [packaging/RELEASING.md](packaging/RELEASING.md).

```sh
git tag v0.3.0 && git push origin v0.3.0
```

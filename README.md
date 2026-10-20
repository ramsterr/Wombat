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
- **Useful tools** — `read`, `write`, `edit`, `bash`, `glob`, and `grep` — with per-class allow/deny/ask permissions.
- **Safety guardrails** — secrets, `.env` files, destructive commands, git history, and security-sensitive code are covered by built-in rules and warnings.
- **Two working modes** — `chat` explores and explains without touching anything; `create` writes and edits. Switch with `/mode`.
- **Ask before you act** — every tool call can ask you first, and the sandbox (an OS-level jail for commands) is opt-in, not enforced. File mutations are snapshotted so you can restore a session workspace with `wombat checkpoint restore`.
- **Two-tier context management** — prune old tool output, then summarize older history, so long sessions keep working.
- **Task ledger, verification, and delegation** — the lead agent keeps a persistent plan, records checks and evidence, runs bounded read-only scouts, and can hand independent coding tasks to writers in isolated git worktrees.
- **Check-gated parallel writers** — writers own disjoint files, run in worktrees, and merge sequentially only after their offers pass tests; conflicts and failures are kept for inspection.
- **No runtime dependencies on macOS and Linux** — one self-contained binary; no Python, Node, or external service required. On Windows, shell commands need Git Bash or WSL and the OS sandbox falls back to off.

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
| `/sandbox` | sandbox and network control |
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
| `wombat config set <key> <value>` | set `provider`, `model`, `mode`, `max_steps`, sandbox, writers, or a permission |
| `wombat checkpoint list` | list snapshots for the latest session |
| `wombat checkpoint restore <id>` | restore a snapshot (`--yes` for non-interactive use) |
| `wombat upgrade` | update wombat to the latest release (`--check` to only look) |
| `wombat version` | print the version |

Flags for `run` and `resume`: `-m <model>`, `-p <provider>`, `--yolo` (auto-approve tools), `-c` (continue latest), `--quiet`, and for `run` also `--image`, `--video`, `--audio`, and `--file` (repeatable).

Line REPL commands: `/model`, `/models`, `/provider`, `/new`, `/compact`, `/cost`, `/status`, `/yolo`, `/sandbox`, `/help`, `/exit`.

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
| `read` | read | line-numbered, offset/limit, 8 MB cap |
| `write` | write | creates parent directories |
| `edit` | edit | exact-string replace; must match uniquely unless `replace_all` |
| `bash` | bash | 120s timeout, OS sandbox by default, output truncated head and tail |
| `glob` | read | `**` patterns; skips `.git` and `node_modules` |
| `grep` | read | pure-Go regular-expression search |
| `update_plan` | plan | create/replace the ledger, steps, and checks |
| `run_checks` | verify | run a step's checks and record evidence |
| `delegate` / `delegate_many` | delegate | read-only investigation workers |
| `delegate_write` | write | isolated git worktree writers (opt-in in config) |

Permissions are set per class to `ask` (default), `allow`, or `deny`. Read is always allowed. In non-interactive runs, `ask` behaves as `deny` unless `--yolo` is passed.

## Craft standard

The system prompt tells the agent to aim at a professional result and to
**research before it builds** whenever the network is available:

> Aim at a professional result. Anything a person will judge on quality — a 3D
> model, an illustration, a page, a chart, a game, a sound, a plugin, a CLI — is
> measured against the best work you can find, not against your first attempt.

Concretely, the agent is told to:

- look at how professionals do it first — established libraries, official
  design systems, curated asset packs, well-known open-source projects;
- prefer proven, maintained, licensed sources over inventing from scratch,
  and say why it chose one;
- study the reference's *craft* (proportions, palette, typography, spacing,
  motion, error states, accessibility, performance), never copy a copyrighted
  asset;
- fetch references with the shell, preferring primary sources — the project's
  own docs and source over a blog summary;
- pick sources it can actually read (a text agent cannot see a rendered image,
  so model files, source, specs, and docs beat screenshots);
- check licences and keep the credits;
- read the repository's own `examples/` directory first and match its polish;
- build it, run it, render it, **look at the output**, and fix what is wrong
  before claiming it works.

When the network is unavailable the agent is told to say so plainly, work from
its best knowledge, and list the assumptions it had to make. The prompt also
names concrete starting points per kind of work (Poly Haven and Sketchfab for
3D, Material and Apple HIG for UI, Observable Plot and D3 for charts, Lucide
and Phosphor for icons, and so on).

## Guardrails

The system prompt carries safety rules that apply even when the user does not mention them:

- **Secrets** — never print, log, commit, or transmit API keys, tokens, passwords, private keys, cookies, or connection strings; use placeholders and environment variables.
- **Environment files** — never commit `.env`; before creating or editing `.env`, `.env.*`, credentials, or key files, ensure `.gitignore` covers them and keep a `.env.example` with dummy values.
- **Git** — do not commit or push unless explicitly asked. When asked, inspect `git status` and `git diff`, stage only intended files, scan them for secrets, never bypass hooks, and never force-push, amend, or rewrite history without permission.
- **Destructive actions** — do not run `rm -rf`, `git reset --hard`, `git clean -fd`, database drops or migrations, deploys, mass deletions, recursive `chmod`/`chown`, or `kill` unless explicitly requested; state the risk and prefer a reversible path.
- **System safety** — no `sudo`, no disabling TLS or authentication, no piping remote scripts into a shell, and no edits outside the working directory without permission.
- **Secure code** — validate input, parameterize SQL, avoid shell injection, escape output, keep least privilege, and do not weaken auth, crypto, or security checks.
- **Tests** — never disable, skip, or delete tests, linters, or hooks just to make a check pass.

As a second layer, the `write` and `edit` tools append a warning when a secrets-like file (`.env`, `.env.*`, `*.pem`, `*.key`, `credentials.*`, …) is written in a git repository but is not ignored. The warning appears in the tool result so the agent can fix `.gitignore` before committing.

These are guardrails plus an optional OS sandbox. On macOS, `workspace-write` uses `sandbox-exec`; on Linux it uses `bwrap` when installed. Windows has no sandbox backend yet and falls back to off. Permission prompts remain the application-level gate; `--yolo` and `permissions: allow` bypass them, so review diffs before committing.

### Every permission, and how it looks

Each kind of permission has its own colour and symbol, so you always know
what you are approving:

| | request | colour | what it means |
|---|---|---|---|
| `?` | `bash`, `write`, `edit` | orange | ordinary tool use inside the workspace |
| `?` | outside the workspace | blue | the command or file leaves your project |
| `▲` | network access | magenta | the sandbox is blocking the internet |
| `▲` | write outside the sandbox | magenta | the sandbox blocked a write |
| `!` | dangerous command | red | `sudo`, `rm -rf /`, `mkfs`, `dd of=/dev/`, `git push --force`, fork bombs, … |

In the TUI the prompt box border and the title use that colour. In the line
REPL the symbol and text use it.

What can be requested, and how you change it:

| permission | default | how to change it |
|---|---|---|
| `bash`, `write`, `edit` | ask | `wombat config set permissions.bash allow\|deny\|ask` (same for `write`, `edit`) |
| outside the workspace | ask | `wombat config set permissions.outside allow\|deny\|ask` |
| network | off | `/sandbox network on` · `wombat config set sandbox_network true` |
| dangerous commands | ask, then refused | approve the prompt, or `permissions.bash allow` |
| sandbox mode | `workspace-write` | `/sandbox mode …` · `wombat config set sandbox …` |
| read-only workers | always on | writers and scouts are read-only by construction |
| git mutations (writers) | always blocked | writers may not touch git state |
| checkpoints | on | `wombat config set checkpoints false` |

`--yolo` (and `permissions: allow`) skips the prompts but never the OS
sandbox, the dangerous-command list, or the worker budgets. Reads are never
gated: `read`, `glob`, and `grep` always work.

### You control the sandbox

The sandbox is **off by default** — Wombat asks you for every tool call
instead of silently constraining the agent. The OS sandbox is an extra layer
you can switch on when you want it:

```sh
/sandbox mode workspace-write   # confine commands to the working directory
/sandbox mode read-only         # commands may not write at all
/sandbox mode off               # no sandbox (the default)
```

The sandbox is yours to change, at any time, without restarting.

```sh
/sandbox                      # show the current mode and network state
/sandbox network on           # let commands reach the internet
/sandbox network off          # block it again
/sandbox mode off             # no sandbox at all
/sandbox mode read-only       # reads only
/sandbox mode workspace-write # writes limited to the working directory (default)
/sandbox mode danger-full-access
```

Set the defaults for new sessions with `wombat config set sandbox workspace-write`
and `wombat config set sandbox_network true`.

### It asks before it gives up

When the sandbox stops a command, Wombat asks you instead of failing quietly.
You get the same prompt you know from tool permissions:

```
╭─ permission  sandbox: network access ─────────────────────╮
│  the sandbox blocked this command — git fetch origin         │
│                                                              │
│  y  allow   a  always sandbox: network access   n  deny      │
╰──────────────────────────────────────────────────────────────╯
```

- **y** — run this one command with the network allowed, then keep the sandbox as it was
- **a** — turn the network on for the rest of the session
- **n** — refuse it; the agent is told the answer and moves on

The same prompt appears when a command tried to write outside the workspace.
A refused command comes back to the model as a clear message, not a DNS error:

```
network access is blocked by the sandbox: git fetch origin
  the user can enable it with `/sandbox network on` (or `wombat config set sandbox_network true`)
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
  "permissions": {"bash": "ask", "write": "ask", "edit": "ask"},
  "max_steps": 64,
  "orchestrator": true,
  "max_delegations": 3,
  "max_parallel_scouts": 3,
  "worker_max_steps": 24,
  "worker_token_budget": 120000,
  "turn_worker_token_budget": 240000,
  "sandbox": "workspace-write",
  "sandbox_network": false,
  "checkpoints": true,
  "require_verification": true,
  "writers": true,
  "max_parallel_writers": 2,
  "writer_max_steps": 40,
  "writer_token_budget": 200000,
  "turn_writer_token_budget": 400000,
  "mouse": false
}
```

`mouse` defaults to false so terminal text selection works; `/mouse` toggles it and saves the choice. `orchestrator` defaults to true; set it to false to run the original single-agent loop. `max_delegations` limits delegations per user turn, `max_parallel_scouts` caps concurrent scouts, and `worker_max_steps` caps each worker. `worker_token_budget` caps a single worker and `turn_worker_token_budget` caps all workers in one turn; both are hard stops. Lead turns are not capped: the only stop is `max_steps`. `sandbox` is `off`, `read-only`, `workspace-write`, or `danger-full-access`; checkpoints are on by default. Writers additionally use `max_parallel_writers`, per-writer and per-turn token budgets, and step/timeout caps.

## Orchestration

The lead agent owns the plan, edits, commands, and final answer. For multi-step work it maintains a task ledger (`update_plan`) with a goal, durable facts, and steps that are `pending`, `in_progress`, `completed`, `blocked`, or `failed`. The ledger is injected into the system prompt and persisted next to the session, so plans survive resume and compaction.

`delegate` hands one self-contained investigation to a fresh worker with its own context and session. `delegate_many` runs two to six independent scouts concurrently, in waves bounded by `max_parallel_scouts`. Workers are read-only — they get `read`, `glob`, and `grep`, cannot edit files, run mutating commands, or delegate further — and return a structured JSON report with a status, summary, findings, path:line evidence, tests, risks, and next steps. Worker usage is counted in the lead's totals and in the hard per-turn budget. Delegation is deliberately sparse: the lead should use it when exploration would flood its context, not for work it can do directly. Dependent steps run sequentially; only independent scouting tasks belong in one fleet.

### Checks, verification, and writers

Steps can declare `checks` in `update_plan`. `run_checks` executes them in the lead workspace, records pass/fail output in the ledger, and the completion gate refuses to mark a step completed while a check is pending or failing. Writers (`delegate_write`) are available in git repositories when `writers` is enabled. Each writer gets a context pack and a temporary worktree, must declare the exact files it owns, runs the step checks, and is committed before merge. Merge is sequential: the patch is applied to the main workspace, checks run again, `git diff --check` runs, and a failing or conflicting patch is reverted while the worktree is kept for inspection. Writers cannot overlap file ownership; use them for independent implementation steps, not dependent edits.

```sh
wombat config set orchestrator false        # disable ledger + delegation
wombat config set max_delegations 2         # per-turn delegation budget
wombat config set max_parallel_scouts 2     # concurrent scouts per wave
wombat config set worker_max_steps 16       # per-worker step cap
wombat config set worker_token_budget 80000 # per-worker token cap
wombat config set turn_worker_token_budget 160000 # all workers per turn
wombat config set sandbox workspace-write      # off | read-only | workspace-write | danger-full-access
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
internal/sandbox  macOS sandbox-exec and Linux bwrap policies
internal/trace    append-only JSONL run events
internal/eval     task loader, protection, repeats, reports, pricing
internal/llm      provider adapters, HTTP errors, retry/backoff
internal/release  GitHub release lookup, checksums, version comparison
internal/tools    read/write/edit/bash/glob/grep, sandboxed commands, permissions
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

## Wombat
<img width="2872" height="676" alt="Wombat" src="https://github.com/user-attachments/assets/53857c41-0947-4be9-b90f-72dd92ab9f99" />

[![CI](https://github.com/ramsterr/Wombat/actions/workflows/ci.yml/badge.svg)](https://github.com/ramsterr/Wombat/actions/workflows/ci.yml)
[![Version](https://img.shields.io/badge/version-0.1.0-blue)](https://github.com/ramsterr/Wombat)
[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](#install)
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
- [Examples](#examples)
- [Architecture](#architecture)
- [Contributing](#contributing)

## Features

- **Chat-first terminal UI** with streaming replies, inline tool steps, and permission prompts. Reasoning is shown for thinking models, persisted with the turn, and replayed where the provider requires it.
- **Multimodal input** — images, video, audio, and documents, wherever the selected model supports them.
- **Resumable sessions** stored as JSONL, keyed to the working directory, with a browsable history and transcript previews.
- **14 provider presets** plus any OpenAI-compatible endpoint; automatic protocol routing for OpenCode Zen and Go.
- **Useful tools** — `read`, `write`, `edit`, `bash`, `glob`, and `grep` — with per-class allow/deny/ask permissions.
- **Safety guardrails** — secrets, `.env` files, destructive commands, git history, and security-sensitive code are covered by built-in rules and warnings.
- **Two-tier context management** — prune old tool output, then summarize older history, so long sessions keep working.
- **No runtime dependencies** — one self-contained binary; no Python, Node, or external service required.

## Install

Requires Go 1.26+. The result is a single self-contained binary of about 11 MB.

```sh
git clone https://github.com/ramsterr/Wombat.git
cd Wombat

mkdir -p ~/.local/bin
go build -o ~/.local/bin/wombat ./cmd/wombat
```

Builds are silent on success — no output is expected. Verify:

```sh
wombat version        # wombat 0.1.0
```

If the command is not found, add `~/.local/bin` to your PATH and restart the shell:

```sh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
```

Prefer another location? The binary is self-contained; build it anywhere and move it onto your PATH:

```sh
go build -o wombat ./cmd/wombat && mv wombat /usr/local/bin/
```

To uninstall, delete the binary (`rm ~/.local/bin/wombat`) and, if you want, `~/.wombat/`.

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

## The TUI

Type a message and press `enter`. Responses stream in; tool calls appear as they run; reasoning-capable models show their thinking above the answer.

| key | action |
|---|---|
| `enter` | send |
| `alt+enter` / `ctrl+j` | newline |
| `esc` | interrupt the current run |
| `pgup` / `pgdn`, `ctrl+u` / `ctrl+d` | scroll the transcript |
| `up` / `down` | scroll one line when the input is empty |
| `ctrl+v` | attach an image from the clipboard |
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
| `/paste` | attach an image from the clipboard |
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
| `wombat config set <key> <value>` | set `provider`, `model`, `max_steps`, or a permission |
| `wombat version` | print the version |

Flags for `run` and `resume`: `-m <model>`, `-p <provider>`, `--yolo` (auto-approve tools), `-c` (continue latest), `--quiet`, and for `run` also `--image`, `--video`, `--audio`, and `--file` (repeatable).

Line REPL commands: `/model`, `/models`, `/provider`, `/new`, `/compact`, `/cost`, `/status`, `/yolo`, `/help`, `/exit`.

## Attachments

Send images, video, audio, and documents to any model that supports them. Attach files by:

- typing `@path/to/file` inside a message (`@"path with spaces"` when needed)
- using `/attach <path>` in the TUI — pending files appear above the input; `/detach [n|all]` removes them
- pressing `ctrl+v` or running `/paste` to grab an image from the system clipboard (macOS, or Linux with `wl-paste`/`xclip`)
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
| `bash` | bash | 120s timeout, output truncated head and tail |
| `glob` | read | `**` patterns; skips `.git` and `node_modules` |
| `grep` | read | pure-Go regular-expression search |

Permissions are set per class to `ask` (default), `allow`, or `deny`. Read is always allowed. In non-interactive runs, `ask` behaves as `deny` unless `--yolo` is passed.

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

These are guardrails, not a sandbox. Permission prompts remain the hard gate; `--yolo` and `permissions: allow` bypass them, so review diffs before committing.

## Context management

Long sessions stay under the model window with two tiers:

1. **Prune** — old tool output is replaced with short placeholders. The last two user turns and roughly 40k tokens of recent tool output are protected.
2. **Summarize** — if the history is still too large, older messages are condensed into a structured summary while the most recent ~15k tokens stay verbatim.

Token usage, cache hits, and compaction events are reported live. Prompt caching works on Anthropic and OpenAI.

## Sessions

Sessions are append-only JSONL files under `~/.wombat/sessions/<project-hash>/`, keyed to the working directory. Every run is resumable with `wombat resume` or `/sessions` inside the TUI; attachments live beside the session in `.attachments/`. Deleting a session file removes its history.

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
  "mouse": false
}
```

`mouse` defaults to false so terminal text selection works; `/mouse` toggles it and saves the choice.

## Examples

`examples/` groups reference tasks and outputs by genre — design, scraping, browser use, data visualization, and more. See [examples/README.md](examples/README.md) for the full list, including the Nash equilibrium visual, a Billboard chart, and an SVG cat.

## Architecture

```text
cmd/wombat        entry point
internal/cli      REPL, headless run, setup wizard, doctor, clipboard helpers
internal/agent    agent loop, system prompt, hooks, context compaction
internal/llm      provider adapters (OpenAI SSE, Responses, Anthropic, Ollama, Google)
internal/tools    read/write/edit/bash/glob/grep
internal/tui      Bubble Tea chat UI, sessions, models, usage, doctor
internal/session  JSONL session persistence and attachment store
internal/config   config load/save, presets, key resolution
internal/tokens   token estimation and model context windows
internal/clip     clipboard image readers
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

## Wombat
<img width="2872" height="676" alt="image" src="https://github.com/user-attachments/assets/ea6d7dda-05b3-47e3-be67-f79af2133db7" />



[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Version](https://img.shields.io/badge/version-0.1.0-blue)](https://github.com/ramsterr/Wombat/releases)
[![Lightweight](https://img.shields.io/badge/lightweight-~12%20MB-brightgreen)](#)
[![Single binary](https://img.shields.io/badge/single-binary-important)](#)
[![Dependencies](https://img.shields.io/badge/runtime%20deps-zero-success)](#)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](#)

A lightweight coding agent for the terminal. One static binary, fast startup, small memory footprint.

Wombat runs an agentic loop against any OpenAI-compatible API, Anthropic, or a local model server (Ollama, LM Studio, llama.cpp, vLLM, MLX), with tools for reading, writing, editing, searching files, and running shell commands. Running `wombat` with no arguments opens a full-screen chat TUI: streaming replies, inline tool calls and permission prompts, a session browser with live transcript previews, a filterable model picker, a health-check dashboard, and a usage view with activity and tool-call charts.

## Install

Requires Go 1.26+. No runtime dependencies — the result is a single static binary (~9 MB).

```sh
git clone https://github.com/ramsterr/Wombat.git
cd Wombat

mkdir -p ~/.local/bin
go build -o ~/.local/bin/wombat ./cmd/wombat
```

Builds are **silent on success** — no output is the expected result. Verify:

```sh
wombat version        # prints: wombat 0.1.0
```

If you get `command not found`, `~/.local/bin` isn't on your PATH yet. Add it to `~/.zshrc` (or `~/.bashrc`) and restart your shell:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Prefer somewhere else? The binary is self-contained — build it anywhere and move it to any directory on your PATH:

```sh
go build -o wombat ./cmd/wombat && mv wombat /usr/local/bin/
```

To uninstall, simply delete the binary (`rm ~/.local/bin/wombat`). Config lives in `~/.wombat/` if you want to remove that too.

## Quickstart

```
wombat                 # full-screen chat TUI
wombat chat            # same TUI (line REPL when stdin is piped)
wombat setup           # wizard: detect local servers, pick a provider and model
wombat run "add tests for the parser and run them"
wombat doctor          # verify config and connectivity
```

The TUI is chat-first: type a message and press `enter` (`alt+enter` adds a newline), `esc` interrupts a running turn, the mouse wheel or `pgup`/`pgdn`/`↑`/`↓` (empty input) scrolls, `ctrl+c` quits. Tool calls stream inline; `bash`, `write`, and `edit` ask for permission: `y` allow, `a` always, `n` deny, or `f` to deny with instructions the model will see. Slash commands open everything in place: `/help`, `/models`, `/sessions`, `/usage`, `/doctor`, `/model <name>`, `/provider <name>`, `/new`, `/compact`, `/cost`, `/status`, `/yolo`, `/setup`, `/exit`. On a fresh install the setup wizard runs once, then the chat opens. Everything works without a TTY too: `wombat chat`, `wombat run`, and piped input fall back to the line REPL.

## Commands

| command | what it does |
|---|---|
| `wombat` | full-screen chat TUI: streaming chat, inline tool calls, permission prompts, sessions/models/usage/doctor via slash commands |
| `wombat chat` | same TUI; line REPL when stdin is not a TTY |
| `wombat resume [id\|last]` | resume a session in the TUI |
| `wombat run "<task>"` | one-shot headless run (exit code reflects failure) |
| `wombat run -c "<task>"` | continue the latest session headless |
| `wombat setup` | wizard: provider, API key, model selection |
| `wombat models [provider]` | list models |
| `wombat sessions` | list sessions for this directory |
| `wombat doctor` | config, permissions, and connectivity checks |
| `wombat config` | show config (API keys redacted) |
| `wombat config set <key> <value>` | `provider`, `model`, `max_steps`, `permissions.bash\|write\|edit` |
| `wombat version` | version |

Flags for `run`/`resume`: `-m <model>`, `-p <provider>`, `--yolo` (auto-approve tools), `-c` (continue latest), `--quiet`, plus `--image`, `--video`, `--audio`, and `--file` for `run`.

REPL slash commands: `/model`, `/models`, `/provider`, `/new`, `/compact`, `/cost`, `/status`, `/yolo`, `/help`, `/exit`.

## Attachments

Send images, video, audio, and documents to any model that supports them. Attach in one of three ways:

- type `@path/to/file` inside a message (`@"path with spaces"` when needed)
- `/attach <path>` in the chat TUI (pending chips show above the input; `/detach [n|all]`)
- `wombat run --image shot.png --file report.pdf "what changed?"`

Files are copied into the session store at `~/.wombat/sessions/<project-hash>/.attachments/` and referenced from the transcript, so history and `wombat resume` keep working. Each file is capped at 20 MB; text-like files are folded into the prompt, everything else is sent as native media. Gemini inlines at most about 14 MB per request, so shrink or split large files.

| provider | images | video | audio | documents |
|---|---|---|---|---|
| Anthropic / Claude | yes (png, jpeg, gif, webp) | no | no | PDF, text |
| OpenAI Chat Completions (`gpt-4o`, compatible servers) | yes | no | wav, mp3 (audio models) | text only |
| OpenAI Responses (`gpt-4.1`, `gpt-5`, o-series) | yes | no | audio models | PDF, text |
| Google Gemini | yes | yes | yes | PDF and most documents |
| Ollama | vision models only | no | no | text only |
| OpenCode Zen / Go | follows the routed protocol above | | | |

If the selected model does not accept an attachment the provider returns an explicit error; switch to a multimodal model and resend.

## Providers

Presets: `openai`, `anthropic`, `openrouter`, `deepseek`, `groq`, `xai`, `mistral`, plus local `ollama`, `lmstudio`, `llamacpp`, `vllm`, `mlx` — and any custom OpenAI-compatible endpoint.

API keys resolve in order: config file → preset env var (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, …) → `<PROVIDER>_API_KEY`. Local servers need no key.

### OpenCode Zen and Go

`opencode` ([Zen](https://opencode.ai/zen), pay-as-you-go) and `opencode-go` ([Go](https://opencode.ai/go), subscription) are supported natively. Each model is served on a different protocol, and Wombat routes automatically:

| family | protocol |
|---|---|
| GLM, Kimi, DeepSeek, MiMo, LongCat, Qwen Max, … | OpenAI chat completions |
| Claude, MiniMax, Qwen Plus/Flash | Anthropic messages |
| GPT, Grok, Muse | OpenAI Responses API |
| Gemini | Google generateContent |

Unknown models start on chat completions and fall back across protocols if the gateway reports a protocol mismatch; the winning route is cached per model. Wombat sends the `User-Agent` and `x-opencode-session` headers the Go gateway expects for routing and prompt caching — the same session ID is reused across turns, so cache hits show up in `/cost`.

If the OpenCode app is installed on the same machine, `wombat setup` detects its stored credentials and offers to reuse them — no key to paste. Note: `*-free` models are restricted to the OpenCode app itself and cannot be used from third-party clients.

## Tools

| tool | class | notes |
|---|---|---|
| `read` | read | line-numbered, offset/limit, 8 MB cap |
| `write` | write | creates parent dirs |
| `edit` | edit | exact-string replace; must match uniquely unless `replace_all` |
| `bash` | bash | 120s timeout, output truncated head+tail |
| `glob` | read | `**` patterns, skips `.git`/`node_modules` |
| `grep` | read | pure-Go regex search |

Permissions per class: `ask` (default), `allow`, `deny`. Read is always allowed. In non-interactive runs, `ask` behaves as `deny` unless `--yolo`.

## Context management

Long sessions stay under the model window with two tiers:

1. **Prune** — old tool outputs are replaced by short placeholders (protects the last two user turns and ~40k tokens of recent tool output).
2. **Summarize** — when still too large, older history is condensed into a structured summary; the most recent ~15k tokens stay verbatim.

Token usage, cache hits, and compaction events are reported live. Works with prompt caching on Anthropic and OpenAI.

## Sessions

Sessions are append-only JSONL files under `~/.wombat/sessions/<project-hash>/`, keyed to the working directory. Every run is resumable; `wombat sessions` lists them.

## Configuration

`~/.wombat/config.json` (0600). Overrides: `WOMBAT_HOME`, `WOMBAT_CONFIG`, `WOMBAT_PROVIDER`, `WOMBAT_MODEL`.

```json
{
  "provider": "ollama",
  "model": "qwen3:14b",
  "providers": {
    "ollama": {"kind": "ollama", "base_url": "http://127.0.0.1:11434", "num_ctx": 32768}
  },
  "permissions": {"bash": "ask", "write": "ask", "edit": "ask"},
  "max_steps": 64
}
```

## Architecture

```
cmd/wombat        entry point
internal/cli      REPL, headless run, setup wizard, doctor
internal/agent    agent loop, system prompt, context compaction
internal/llm      provider adapters (OpenAI SSE, Anthropic, Ollama NDJSON)
internal/tools    read/write/edit/bash/glob/grep
internal/session  JSONL session persistence
internal/config   config load/save, presets, key resolution
internal/tokens   token estimation and model context windows
```

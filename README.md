## Wombat
<img width="3648" height="1104" alt="image" src="https://github.com/user-attachments/assets/02f8af15-d3c5-45e5-ad70-c83b8af7fa19" />

A lightweight coding agent for the terminal. One static binary, fast startup, small memory footprint.

Wombat runs an agentic loop against any OpenAI-compatible API, Anthropic, or a local model server (Ollama, LM Studio, llama.cpp, vLLM, MLX), with tools for reading, writing, editing, searching files, and running shell commands. Running `wombat` with no arguments opens a full-screen terminal UI: the wombat mascot, a chat button, a session browser with live transcript previews, a filterable model picker, a health-check dashboard, and a usage view with activity and tool-call charts.

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
wombat                 # full-screen TUI: chat, sessions, models, usage, doctor
wombat chat            # jump straight into a session
wombat setup           # wizard: detect local servers, pick a provider and model
wombat run "add tests for the parser and run them"
wombat doctor          # verify config and connectivity
```

The TUI is keyboard-driven: `↑`/`↓` to move, `enter` to open, `esc` to go back, `q` to quit. `c` starts a chat, `s` sessions, `m` models, `u` usage, `d` doctor, `p` configure. On a fresh install the Configure entry is selected — run it once and you are ready to chat. Everything works without a TTY too: `wombat chat`, `wombat run`, and piped input behave as before.

## Commands

| command | what it does |
|---|---|
| `wombat` | full-screen TUI: chat, resume, sessions (transcript previews), models (filter + switch), usage charts, doctor; on a fresh install the configure entry is pre-selected |
| `wombat chat` | interactive REPL directly (new session; skips the TUI) |
| `wombat resume [id\|last]` | resume a session |
| `wombat run "<task>"` | one-shot headless run (exit code reflects failure) |
| `wombat run -c "<task>"` | continue the latest session headless |
| `wombat setup` | wizard: provider, API key, model selection |
| `wombat models [provider]` | list models |
| `wombat sessions` | list sessions for this directory |
| `wombat doctor` | config, permissions, and connectivity checks |
| `wombat config` | show config (API keys redacted) |
| `wombat config set <key> <value>` | `provider`, `model`, `max_steps`, `permissions.bash\|write\|edit` |
| `wombat version` | version |

Flags for `run`/`resume`: `-m <model>`, `-p <provider>`, `--yolo` (auto-approve tools), `-c` (continue latest), `--quiet`.

REPL slash commands: `/model`, `/models`, `/provider`, `/new`, `/compact`, `/cost`, `/status`, `/yolo`, `/help`, `/exit`.

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

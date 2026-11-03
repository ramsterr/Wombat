# Wombat terminal frontend

This is the opentui + SolidJS interface used by `wombat` (the same TUI stack
opencode uses). The Go process stays responsible for the agent, tools,
sessions, permissions, and previews; this app is the terminal frontend.

## Build

Requires Node 26.4 or newer (opentui uses `node:ffi`).

```bash
cd tui
npm install
npm run build
```

The build writes `dist/app.mjs` and stages the native renderer in `native/`.
`wombat` finds both automatically when run from the repository.

## Run

```bash
./wombat
```

`wombat` requires this frontend. When the bundle or a compatible Node runtime
is missing it prints the `update.sh` command to install it instead of opening
a different interface.

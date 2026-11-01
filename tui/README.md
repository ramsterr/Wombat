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

`wombat` falls back to the original Bubble Tea interface when the frontend is
not built or Node is too old. To force the classic interface:

```bash
WOMBAT_CLASSIC=1 ./wombat
```

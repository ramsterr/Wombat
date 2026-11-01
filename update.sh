#!/bin/sh
# Update Wombat to the latest main, including the opentui frontend.
set -eu

REPO="${WOMBAT_REPO:-https://github.com/ramsterr/Wombat.git}"
SRC="${WOMBAT_SRC:-$HOME/.wombat/src}"
BIN_DIR="${WOMBAT_BIN_DIR:-$HOME/.local/bin}"
WOMBAT_DIR="${WOMBAT_HOME:-$HOME/.wombat}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "error: $1 is required" >&2
    exit 1
  }
}

need git
need go
need node
need npm

NODE_RAW="$(node -v 2>/dev/null || true)"
NODE_VER="${NODE_RAW#v}"
NODE_MAJOR="${NODE_VER%%.*}"
NODE_REST="${NODE_VER#*.}"
NODE_MINOR="${NODE_REST%%.*}"
if [ "${NODE_MAJOR:-0}" -lt 26 ] || { [ "${NODE_MAJOR:-0}" -eq 26 ] && [ "${NODE_MINOR:-0}" -lt 4 ]; }; then
  echo "error: Node 26.4 or newer is required (found ${NODE_RAW:-none})" >&2
  exit 1
fi

echo "updating wombat from main..."
if [ -d "$SRC/.git" ]; then
  git -C "$SRC" fetch --depth 1 origin main
  git -C "$SRC" reset --hard FETCH_HEAD
else
  rm -rf "$SRC"
  mkdir -p "$(dirname "$SRC")"
  git clone --depth 1 --branch main "$REPO" "$SRC"
fi

echo "building the opentui frontend..."
(
  cd "$SRC/tui"
  npm install --no-audit --no-fund --cache "${TMPDIR:-/tmp}/wombat-npm-cache"
  npm run build
)

echo "building wombat..."
mkdir -p "$BIN_DIR" "$WOMBAT_DIR/ui" "$WOMBAT_DIR/native"
(
  cd "$SRC"
  go build -o "$BIN_DIR/wombat" ./cmd/wombat
)

cp "$SRC/tui/dist/app.mjs" "$WOMBAT_DIR/ui/app.mjs"
cp "$SRC"/tui/native/* "$WOMBAT_DIR/native/"

echo "updated: $BIN_DIR/wombat"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "warning: $BIN_DIR is not on PATH; add it to use \`wombat\` from anywhere." ;;
esac
echo "run: wombat"
echo "if your shell still resolves an old binary, run: hash -r"

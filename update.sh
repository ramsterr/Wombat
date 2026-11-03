#!/bin/sh
# Update Wombat to the latest main, including the opentui frontend.
set -eu

REPO="${WOMBAT_REPO:-https://github.com/ramsterr/Wombat.git}"
REF="${WOMBAT_REF:-main}"
SRC="${WOMBAT_SRC:-$HOME/.wombat/src}"
BIN_DIR="${WOMBAT_BIN_DIR:-$HOME/.local/bin}"
WOMBAT_DIR="${WOMBAT_HOME:-$HOME/.wombat}"
UI_DIR="$WOMBAT_DIR/ui"
NATIVE_DIR="$WOMBAT_DIR/native"

die() {
  echo "error: $*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required but was not found"
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

echo "updating wombat from $REF..."
if [ -d "$SRC/.git" ]; then
  git -C "$SRC" remote set-url origin "$REPO"
  git -C "$SRC" fetch --depth 1 "$REPO" "$REF"
  git -C "$SRC" reset --hard FETCH_HEAD
else
  rm -rf "$SRC"
  mkdir -p "$(dirname "$SRC")"
  git clone --depth 1 --branch "$REF" "$REPO" "$SRC"
fi
COMMIT="$(git -C "$SRC" rev-parse --short HEAD)"
echo "source: $REF @ $COMMIT"

echo "building the opentui frontend..."
(
  cd "$SRC/tui"
  npm install --no-audit --no-fund --cache "${TMPDIR:-/tmp}/wombat-npm-cache"
  npm run build
)
[ -f "$SRC/tui/dist/app.mjs" ] || die "the UI build did not produce tui/dist/app.mjs"

case "$(uname -s)" in
  Darwin) OTUI_LIB="libopentui.dylib" ;;
  Linux) OTUI_LIB="libopentui.so" ;;
  MINGW*|MSYS*|CYGWIN*) OTUI_LIB="opentui.dll" ;;
  *) die "unsupported operating system: $(uname -s)" ;;
esac
[ -f "$SRC/tui/native/$OTUI_LIB" ] || die "the UI build did not produce tui/native/$OTUI_LIB"

echo "building wombat..."
mkdir -p "$BIN_DIR" "$UI_DIR" "$NATIVE_DIR"
(
  cd "$SRC"
  go build -o "$BIN_DIR/wombat" ./cmd/wombat
)

cp "$SRC/tui/dist/app.mjs" "$UI_DIR/app.mjs"
cp "$SRC/tui/native/$OTUI_LIB" "$NATIVE_DIR/$OTUI_LIB"
[ -f "$UI_DIR/app.mjs" ] || die "could not install $UI_DIR/app.mjs"
[ -f "$NATIVE_DIR/$OTUI_LIB" ] || die "could not install $NATIVE_DIR/$OTUI_LIB"

echo "updated: $BIN_DIR/wombat ($REF @ $COMMIT)"
"$BIN_DIR/wombat" version
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "warning: $BIN_DIR is not on PATH; add it to use \`wombat\` from anywhere." ;;
esac
CURRENT="$(command -v wombat 2>/dev/null || true)"
if [ -n "$CURRENT" ] && [ "$CURRENT" != "$BIN_DIR/wombat" ]; then
  echo "error: 'wombat' resolves to $CURRENT, not $BIN_DIR/wombat." >&2
  echo "       remove that old copy or put $BIN_DIR before it on PATH, then run: hash -r" >&2
  exit 1
fi
echo "run: wombat"
echo "if your shell still resolves an old binary, run: hash -r"

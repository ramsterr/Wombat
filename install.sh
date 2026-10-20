#!/bin/sh
# wombat installer
#
#   curl -fsSL https://raw.githubusercontent.com/ramsterr/Wombat/main/install.sh | bash
#
# Environment:
#   VERSION=0.2.0   install a specific version
#   PREFIX=~/.local/bin  where to install the binary
#   REPO=ramsterr/Wombat   source repository
#   BASE_URL=...           override the download base (used by tests)
set -eu

REPO="${REPO:-ramsterr/Wombat}"
PREFIX="${PREFIX:-$HOME/.local/bin}"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required but was not found"
}

detect_os() {
  os="$(uname -s)"
  case "$os" in
    Darwin) echo "darwin" ;;
    Linux) echo "linux" ;;
    MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
    *) die "unsupported operating system: $os" ;;
  esac
}

detect_arch() {
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *) die "unsupported architecture: $arch" ;;
  esac
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die "need sha256sum or shasum to verify the download"
  fi
}

latest_version() {
  # Prefer the rolling VERSION file published with the stable release, then
  # fall back to the GitHub API.
  if command -v curl >/dev/null 2>&1; then
    v="$(curl -fsSL "https://github.com/$REPO/releases/download/stable/VERSION.txt" 2>/dev/null || true)"
    if [ -n "$v" ]; then
      printf '%s' "$v" | tr -d ' \t\r\n'
      return 0
    fi
    v="$(curl -fsSL "https://api.github.com/repos/$REPO/releases" 2>/dev/null |
      grep '"tag_name"' |
      grep -v '"stable"' |
      head -n 1 |
      sed -e 's/.*"tag_name"[[:space:]]*:[[:space:]]*"//' -e 's/".*//' -e 's/^v//')"
    if [ -n "$v" ]; then
      printf '%s' "$v"
      return 0
    fi
  fi
  return 1
}

need uname
need tar
OS="$(detect_os)"
ARCH="$(detect_arch)"

VERSION="${VERSION:-}"
if [ -z "$VERSION" ]; then
  VERSION="$(latest_version || true)"
  [ -n "$VERSION" ] || die "could not determine the latest version; set VERSION=<tag> and retry"
fi

case "$OS" in
  windows)
    EXT="zip"
    BIN_NAME="wombat.exe"
    ;;
  *)
    EXT="tar.gz"
    BIN_NAME="wombat"
    ;;
esac

ASSET="wombat_${VERSION}_${OS}_${ARCH}.${EXT}"
BASE="${BASE_URL:-https://github.com/$REPO/releases/download}/v${VERSION}"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

need curl

say "downloading ${ASSET}"
curl -fsSL "${BASE}/${ASSET}" -o "$TMP/$ASSET"
curl -fsSL "${BASE}/checksums.txt" -o "$TMP/checksums.txt"

WANT="$(awk -v f="$ASSET" '$2 == f || $2 == "*"f {print $1}' "$TMP/checksums.txt" | head -n 1)"
[ -n "$WANT" ] || die "no checksum for $ASSET in checksums.txt"
GOT="$(sha256_of "$TMP/$ASSET")"
[ "$WANT" = "$GOT" ] || die "checksum mismatch for $ASSET (want $WANT, got $GOT)"

say "extracting"
if [ "$EXT" = "zip" ]; then
  need unzip
  ( cd "$TMP" && unzip -oq "$ASSET" )
else
  ( cd "$TMP" && tar -xzf "$ASSET" )
fi
BIN="$TMP/$BIN_NAME"
[ -f "$BIN" ] || die "archive did not contain the wombat binary"

mkdir -p "$PREFIX"
say "installing to $PREFIX/$BIN_NAME"
cp "$BIN" "$PREFIX/$BIN_NAME.new"
chmod +x "$PREFIX/$BIN_NAME.new" 2>/dev/null || true
mv "$PREFIX/$BIN_NAME.new" "$PREFIX/$BIN_NAME"

case ":$PATH:" in
  *":$PREFIX:"*) ;;
  *) say ""; say "wombat installed, but $PREFIX is not in your PATH."; say "Add it with: export PATH=\"$PREFIX:\$PATH\"" ;;
esac

say ""
"$PREFIX/$BIN_NAME" version

# Warn if a different wombat comes first on PATH, otherwise the new binary
# is installed but the old one keeps running.
CURRENT="$(command -v wombat 2>/dev/null || true)"
if [ -n "$CURRENT" ] && [ "$CURRENT" != "$PREFIX/$BIN_NAME" ]; then
  say ""
  say "warning: 'wombat' on your PATH is $CURRENT, not $PREFIX/$BIN_NAME."
  say "         remove the old copy (for example: rm \"$CURRENT\") so the new one is used."
fi
say ""
say "Update later with: wombat upgrade"

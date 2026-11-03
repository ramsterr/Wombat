#!/bin/sh
# wombat installer
#
#   curl -fsSL https://raw.githubusercontent.com/ramsterr/Wombat/main/install.sh | bash
#
# Installs the current main branch, including the opentui frontend. There is
# no release-binary path any more: a tagged binary does not carry the UI
# assets, so it would open the removed classic interface.
#
# Environment:
#   PREFIX=~/.local/bin  where to install the binary
#   REPO=ramsterr/Wombat   source repository
set -eu

REPO="${REPO:-ramsterr/Wombat}"

if [ -n "${VERSION:-}" ]; then
  echo "error: VERSION installs are no longer supported; Wombat installs current main with its new UI" >&2
  exit 1
fi

if [ -n "${PREFIX:-}" ]; then
  WOMBAT_BIN_DIR="$PREFIX"
  export WOMBAT_BIN_DIR
fi
WOMBAT_REPO="${WOMBAT_REPO:-https://github.com/$REPO.git}"
export WOMBAT_REPO

UPDATE_URL="${WOMBAT_UPDATE_URL:-https://raw.githubusercontent.com/$REPO/main/update.sh}"
exec sh -c 'curl -fsSL "$1" | sh' _ "$UPDATE_URL"

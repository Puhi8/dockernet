#!/usr/bin/env bash
set -euo pipefail

REPO="${DOCKERNET_REPO:-yourname/dockernet-go}"
BRANCH="${DOCKERNET_BRANCH:-main}"
BINARY="dockernet"

INSTALL_PATH="${DOCKERNET_INSTALL_PATH:-/usr/local/bin/dockernet}"
CONFIG_PATH="${DOCKERNET_CONFIG_PATH:-$HOME/.dockernet.conf}"

if [[ "$REPO" == "yourname/dockernet-go" ]]; then
    echo "Set DOCKERNET_REPO to your GitHub repo (example: owner/dockernet-go)."
    exit 1
fi

TARGET_DIR="$(dirname "$INSTALL_PATH")"
if [[ ! -w "$TARGET_DIR" ]]; then
    echo "No write permission for $TARGET_DIR."
    echo "Set DOCKERNET_INSTALL_PATH to a writable location or run with elevated permissions."
    exit 1
fi

BIN_URL="https://raw.githubusercontent.com/$REPO/$BRANCH/$BINARY"
CFG_URL="https://raw.githubusercontent.com/$REPO/$BRANCH/dockernet.conf.example"
TMP_BIN="$(mktemp)"
trap 'rm -f "$TMP_BIN"' EXIT

echo "Installing dockernet from $REPO@$BRANCH..."
curl -fsSL "$BIN_URL" -o "$TMP_BIN"
install -m 0755 "$TMP_BIN" "$INSTALL_PATH"

if [ ! -f "$CONFIG_PATH" ]; then
    curl -fsSL "$CFG_URL" -o "$CONFIG_PATH"
    echo "Created default config at $CONFIG_PATH"
fi

echo "Done. Run: dockernet scan <folder>"

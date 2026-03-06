#!/usr/bin/env bash
set -euo pipefail

BINARY_NAME="dockernet"
DEFAULT_INSTALL_DIR="/usr/local/bin"

if [[ ! -f "go.mod" || ! -f "dockernet.conf.example" ]]; then
    echo "Run this script from the project root (go.mod + dockernet.conf.example expected)."
    exit 1
fi

if ! command -v go >/dev/null 2>&1; then
    echo "Go is required but not found in PATH."
    exit 1
fi

# Ensure Go build cache is writable; fall back to /tmp when needed.
if [[ -z "${GOCACHE:-}" ]]; then
    GOCACHE="${XDG_CACHE_HOME:-$HOME/.cache}/go-build"
fi
if ! mkdir -p "$GOCACHE" 2>/dev/null || ! touch "$GOCACHE/.dockernet-write-test" 2>/dev/null; then
    GOCACHE="/tmp/dockernet-go-build"
    mkdir -p "$GOCACHE"
fi
rm -f "$GOCACHE/.dockernet-write-test" 2>/dev/null || true
export GOCACHE

INSTALL_PATH="${DOCKERNET_INSTALL_PATH:-${DOCKERNET_INSTALL_DIR:-$DEFAULT_INSTALL_DIR}/$BINARY_NAME}"
CONFIG_PATH="${DOCKERNET_CONFIG_PATH:-$HOME/.dockernet.conf}"

prompt_install_path() {
    local default_path="$1"
    local input=""

    if [[ -t 0 ]]; then
        printf "Install binary location (path or directory) [%s]: " "$default_path" >&2
        read -r input || true
        input="$(printf '%s' "$input" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
    fi

    if [[ -z "$input" ]]; then
        printf '%s\n' "$default_path"
        return
    fi

    if [[ "$input" == */ || -d "$input" ]]; then
        printf '%s/%s\n' "${input%/}" "$BINARY_NAME"
        return
    fi

    printf '%s\n' "$input"
}

INSTALL_PATH="$(prompt_install_path "$INSTALL_PATH")"

TMP_DIR="$(mktemp -d)"
TMP_BIN="$TMP_DIR/$BINARY_NAME"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Building $BINARY_NAME..."
go build -o "$TMP_BIN" .

INSTALL_DIR="$(dirname "$INSTALL_PATH")"
NEED_SUDO=0
if [[ -d "$INSTALL_DIR" ]]; then
    if [[ ! -w "$INSTALL_DIR" ]]; then
        NEED_SUDO=1
    fi
else
    PARENT_DIR="$(dirname "$INSTALL_DIR")"
    if [[ ! -w "$PARENT_DIR" ]]; then
        NEED_SUDO=1
    fi
fi

run_install() {
    mkdir -p "$INSTALL_DIR"
    install -m 0755 "$TMP_BIN" "$INSTALL_PATH"
}

if [[ "$NEED_SUDO" -eq 1 ]]; then
    if ! command -v sudo >/dev/null 2>&1; then
        echo "No write permission for $INSTALL_DIR and sudo is not available."
        echo "Set DOCKERNET_INSTALL_PATH to a writable location."
        exit 1
    fi
    echo "Installing to $INSTALL_PATH (using sudo)..."
    sudo mkdir -p "$INSTALL_DIR"
    sudo install -m 0755 "$TMP_BIN" "$INSTALL_PATH"
else
    echo "Installing to $INSTALL_PATH..."
    run_install
fi

if [[ ! -f "$CONFIG_PATH" ]]; then
    echo "Creating default config at $CONFIG_PATH..."
    mkdir -p "$(dirname "$CONFIG_PATH")"
    cp dockernet.conf.example "$CONFIG_PATH"
else
    echo "Config already exists at $CONFIG_PATH (leaving unchanged)."
fi

echo "Done."
echo "Try: dockernet ls"

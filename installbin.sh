#!/usr/bin/env bash
set -euo pipefail

APP="dockernet"
REPO="${REPO:-Puhi8/dockernet}"
INSTALL_PATH="${DOCKERNET_INSTALL_PATH:-/usr/local/bin/$APP}"
CONFIG_PATH="${DOCKERNET_CONFIG_PATH:-$HOME/.dockernet.conf}"
REQUESTED_VERSION="${1:-${DOCKERNET_VERSION:-latest}}"

require_cmd() {
    local cmd="$1"
    if ! command -v "$cmd" >/dev/null 2>&1; then
        echo "Missing required command: $cmd"
        exit 1
    fi
}

resolve_latest_version() {
    local version=""

    if command -v gh >/dev/null 2>&1; then
        version="$(gh release view --repo "$REPO" --json tagName -q .tagName 2>/dev/null || true)"
    fi

    if [[ -z "$version" ]]; then
        version="$(
            curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
                | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' \
                | head -n1
        )"
    fi

    if [[ -z "$version" ]]; then
        echo "Could not determine latest release for $REPO."
        exit 1
    fi

    printf '%s\n' "$version"
}

detect_os() {
    case "$(uname -s)" in
        Linux) printf 'linux\n' ;;
        Darwin) printf 'darwin\n' ;;
        *)
            echo "Unsupported OS: $(uname -s)"
            exit 1
            ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) printf 'amd64\n' ;;
        aarch64|arm64) printf 'arm64\n' ;;
        *)
            echo "Unsupported architecture: $(uname -m)"
            exit 1
            ;;
    esac
}

require_cmd curl
require_cmd tar
require_cmd install

if [[ "$REQUESTED_VERSION" == "latest" ]]; then
    echo "Detecting latest version..."
    VERSION="$(resolve_latest_version)"
else
    VERSION="$REQUESTED_VERSION"
fi

OS="$(detect_os)"
ARCH="$(detect_arch)"
BINARY_NAME="${APP}-${OS}-${ARCH}"
ARCHIVE_NAME="${BINARY_NAME}.tar.gz"

ARCHIVE_URL="https://github.com/$REPO/releases/download/$VERSION/$ARCHIVE_NAME"
CHECKSUMS_URL="https://github.com/$REPO/releases/download/$VERSION/checksums.txt"
CONFIG_URL="https://raw.githubusercontent.com/$REPO/$VERSION/dockernet.conf.example"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

ARCHIVE_PATH="$TMP_DIR/$ARCHIVE_NAME"
CHECKSUMS_PATH="$TMP_DIR/checksums.txt"

echo "Downloading $ARCHIVE_URL..."
curl -fL --retry 3 --retry-delay 1 -o "$ARCHIVE_PATH" "$ARCHIVE_URL"
curl -fL --retry 3 --retry-delay 1 -o "$CHECKSUMS_PATH" "$CHECKSUMS_URL"

echo "Verifying checksum..."
EXPECTED_SUM="$(
    awk -v f="$ARCHIVE_NAME" \
        '$2==f || $2==("./" f) || $NF==f || $NF==("./" f) {print $1; exit}' \
        "$CHECKSUMS_PATH"
)"

if [[ -z "$EXPECTED_SUM" ]]; then
    echo "Could not find checksum entry for $ARCHIVE_NAME."
    exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL_SUM="$(sha256sum "$ARCHIVE_PATH" | awk '{print $1}')"
else
    require_cmd shasum
    ACTUAL_SUM="$(shasum -a 256 "$ARCHIVE_PATH" | awk '{print $1}')"
fi

if [[ "$EXPECTED_SUM" != "$ACTUAL_SUM" ]]; then
    echo "Checksum mismatch for $ARCHIVE_NAME."
    exit 1
fi

echo "Extracting $ARCHIVE_NAME..."
tar -xzf "$ARCHIVE_PATH" -C "$TMP_DIR"

SOURCE_BIN="$TMP_DIR/$BINARY_NAME"
if [[ ! -f "$SOURCE_BIN" ]]; then
    echo "Archive did not contain expected binary: $BINARY_NAME"
    exit 1
fi

INSTALL_DIR="$(dirname "$INSTALL_PATH")"
NEED_SUDO=0
if [[ -d "$INSTALL_DIR" ]]; then
    [[ -w "$INSTALL_DIR" ]] || NEED_SUDO=1
else
    PARENT_DIR="$(dirname "$INSTALL_DIR")"
    [[ -w "$PARENT_DIR" ]] || NEED_SUDO=1
fi

echo "Installing to $INSTALL_PATH..."
if [[ "$NEED_SUDO" -eq 1 ]]; then
    require_cmd sudo
    sudo mkdir -p "$INSTALL_DIR"
    sudo install -m 0755 "$SOURCE_BIN" "$INSTALL_PATH"
else
    mkdir -p "$INSTALL_DIR"
    install -m 0755 "$SOURCE_BIN" "$INSTALL_PATH"
fi

if [[ ! -f "$CONFIG_PATH" ]]; then
    echo "Creating default config at $CONFIG_PATH..."
    mkdir -p "$(dirname "$CONFIG_PATH")"
    curl -fL --retry 3 --retry-delay 1 -o "$CONFIG_PATH" "$CONFIG_URL"
else
    echo "Config already exists at $CONFIG_PATH (leaving unchanged)."
fi

echo "Installed $APP $VERSION"
echo "Try: $APP ls"

#!/usr/bin/env bash
set -euo pipefail

APP="dockernet"
REPO="${REPO:-Puhi8/dockernet}"
REMOTE="${GIT_REMOTE:-github}"
VERSION="${1:-}"

usage() {
    echo "Usage: ./release.sh v1.2.3"
    echo "Optional env: REPO=owner/name GIT_REMOTE=github"
}

require_cmd() {
    local cmd="$1"
    if ! command -v "$cmd" >/dev/null 2>&1; then
        echo "Missing required command: $cmd"
        exit 1
    fi
}

if [[ -z "$VERSION" ]]; then
    usage
    exit 1
fi

if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]]; then
    echo "Version must look like v1.2.3 (or pre-release like v1.2.3-rc.1)."
    exit 1
fi

require_cmd go
require_cmd git
require_cmd gh
require_cmd tar
require_cmd zip

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "This script must run inside a git repository."
    exit 1
fi

if [[ -n "$(git status --porcelain)" ]]; then
    echo "Working tree is not clean. Commit/stash before releasing."
    exit 1
fi

if ! git remote get-url "$REMOTE" >/dev/null 2>&1; then
    if git remote get-url origin >/dev/null 2>&1; then
        REMOTE="origin"
    else
        echo "No usable git remote found (expected '$REMOTE' or 'origin')."
        exit 1
    fi
fi

if git rev-parse "$VERSION" >/dev/null 2>&1; then
    echo "Tag '$VERSION' already exists locally."
    exit 1
fi

if git ls-remote --tags "$REMOTE" "refs/tags/$VERSION" | grep -q "refs/tags/$VERSION"; then
    echo "Tag '$VERSION' already exists on remote '$REMOTE'."
    exit 1
fi

echo "Releasing $APP $VERSION via remote '$REMOTE'..."

rm -rf dist
mkdir -p dist

targets=(
    "linux amd64"
    "linux arm64"
    "darwin amd64"
    "darwin arm64"
    "windows amd64"
    "windows arm64"
)

echo "Building binaries..."
for target in "${targets[@]}"; do
    read -r goos goarch <<<"$target"
    bin="dist/${APP}-${goos}-${goarch}"
    if [[ "$goos" == "windows" ]]; then
        bin="${bin}.exe"
    fi
    echo "  - $goos/$goarch -> $(basename "$bin")"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags="-s -w" -o "$bin" .
done

echo "Packaging artifacts..."
(
    cd dist
    for target in "${targets[@]}"; do
        read -r goos goarch <<<"$target"
        base="${APP}-${goos}-${goarch}"
        if [[ "$goos" == "windows" ]]; then
            zip -q "${base}.zip" "${base}.exe"
        else
            tar -czf "${base}.tar.gz" "${base}"
        fi
    done

    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum ./*.tar.gz ./*.zip > checksums.txt
    else
        shasum -a 256 ./*.tar.gz ./*.zip > checksums.txt
    fi
)

echo "Creating annotated tag '$VERSION'..."
git tag -a "$VERSION" -m "$APP $VERSION"

echo "Pushing tag to '$REMOTE'..."
git push "$REMOTE" "$VERSION"

echo "Creating GitHub release assets..."
gh release create "$VERSION" \
    dist/*.tar.gz \
    dist/*.zip \
    dist/checksums.txt \
    --repo "$REPO" \
    --title "$APP $VERSION" \
    --generate-notes

echo "Release $VERSION created."

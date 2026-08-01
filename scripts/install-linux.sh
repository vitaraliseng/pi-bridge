#!/usr/bin/env bash
# Install latest pi-bridge release for this Linux machine (amd64/arm64).
set -euo pipefail

download() {
  # $1 url  $2 dest
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1"
  else
    echo "need curl or wget" >&2
    exit 1
  fi
}

fetch() {
  # print body of URL to stdout
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$1"
  else
    echo "need curl or wget" >&2
    exit 1
  fi
}
REPO="${PI_BRIDGE_REPO:-vitaraliseng/pi-bridge}"
PREFIX="${PI_BRIDGE_PREFIX:-$HOME/.local}"
BIN_DIR="$PREFIX/bin"
mkdir -p "$BIN_DIR"

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) goreleaser_arch=x86_64 ;;
  aarch64|arm64) goreleaser_arch=arm64 ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac

if [ "$(uname -s)" != "Linux" ]; then
  echo "this script is for Linux" >&2
  exit 1
fi

tag="${1:-latest}"
if [ "$tag" = "latest" ]; then
  api="https://api.github.com/repos/${REPO}/releases/latest"
else
  api="https://api.github.com/repos/${REPO}/releases/tags/${tag}"
fi

echo "Resolving ${tag} from ${REPO}..."
json=$(fetch "$api")
version=$(printf '%s' "$json" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
version_num=${version#v}
asset="pi-bridge_${version_num}_linux_${goreleaser_arch}.tar.gz"
url=$(printf '%s' "$json" | sed -n "s/.*\"browser_download_url\": *\"\\([^\"]*${asset}\\)\".*/\\1/p" | head -1)
if [ -z "$url" ]; then
  echo "could not find asset $asset in release $version" >&2
  echo "available assets:" >&2
  printf '%s' "$json" | sed -n 's/.*"name": *"\([^"]*pi-bridge[^"]*\)".*/\1/p' >&2 || true
  exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading $url"
download "$url" "$tmp/$asset"
tar -xzf "$tmp/$asset" -C "$tmp"
install -m 755 "$tmp/pi-bridge" "$BIN_DIR/pi-bridge"
echo "Installed $version -> $BIN_DIR/pi-bridge"
"$BIN_DIR/pi-bridge" version 2>/dev/null || "$BIN_DIR/pi-bridge" help | head -5

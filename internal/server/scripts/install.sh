#!/bin/sh
# Lanesh installer for macOS and Linux.
#
#   curl -fsSL {{.ServerURL}}/install.sh | sh
#   curl -fsSL {{.ServerURL}}/install.sh | sh -s -- join <lane-url> --key <join-key>
#
# Arguments after `--` are passed to lanesh once it is installed.
set -eu

VERSION="${LANESH_VERSION:-{{.Version}}}"
BASE="${LANESH_DOWNLOAD_BASE:-https://github.com/jtsang4/lanesh/releases/download}"
LANESH_DIR="${LANESH_HOME:-$HOME/.lanesh}"

fail() { echo "lanesh install: $*" >&2; exit 1; }

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "unsupported OS $(uname -s); see https://github.com/jtsang4/lanesh/releases" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported architecture $(uname -m)" ;;
esac

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
else
  fail "curl or wget is required"
fi

if [ "$VERSION" = "latest" ]; then
  command -v curl >/dev/null 2>&1 || fail "set LANESH_VERSION when curl is not available"
  url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/jtsang4/lanesh/releases/latest)
  VERSION="${url##*/tag/v}"
  [ -n "$VERSION" ] && [ "$VERSION" != "$url" ] || fail "could not determine the latest release"
fi

asset="lanesh_${VERSION}_${os}_${arch}"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "Downloading lanesh v$VERSION ($os/$arch)..."
fetch "$BASE/v$VERSION/$asset" "$tmp/lanesh" || fail "download failed: $BASE/v$VERSION/$asset"
fetch "$BASE/v$VERSION/checksums.txt" "$tmp/checksums.txt" || fail "download failed: checksums.txt"

expected=$(awk -v f="$asset" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$expected" ] || fail "no checksum for $asset"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/lanesh" | awk '{ print $1 }')
else
  actual=$(shasum -a 256 "$tmp/lanesh" | awk '{ print $1 }')
fi
[ "$expected" = "$actual" ] || fail "checksum mismatch for $asset"

mkdir -p "$LANESH_DIR/bin"
chmod 0755 "$tmp/lanesh"
mv -f "$tmp/lanesh" "$LANESH_DIR/bin/lanesh"
echo "Installed $LANESH_DIR/bin/lanesh"

case ":$PATH:" in
  *":$LANESH_DIR/bin:"*) ;;
  *) echo "Add it to your PATH:  export PATH=\"$LANESH_DIR/bin:\$PATH\"" ;;
esac

if [ "$#" -gt 0 ]; then
  exec "$LANESH_DIR/bin/lanesh" "$@"
fi

#!/usr/bin/env bash
# Runs the server-rendered install script (install.sh, or install.ps1 on
# Windows) against a local fake release and checks the installed binary.
#
#   scripts/install-test.sh
#
# Used by CI on Linux, macOS and Windows (Git Bash).
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
VERSION=9.9.9
native() { if command -v cygpath >/dev/null 2>&1; then cygpath -w "$1"; else echo "$1"; fi; }

os=$(go env GOOS); arch=$(go env GOARCH); ext=""
[ "$os" = windows ] && ext=.exe
asset="lanesh_${VERSION}_${os}_${arch}${ext}"

mkdir -p "$WORK/rel/v$VERSION"
cd "$ROOT"
go build -ldflags "-X github.com/jtsang4/lanesh/internal/version.Version=$VERSION" -o "$WORK/rel/v$VERSION/$asset" ./cmd/lanesh
go build -o "$WORK/server$ext" ./cmd/lanesh
(cd "$WORK/rel/v$VERSION" && if command -v sha256sum >/dev/null; then sha256sum "$asset"; else shasum -a 256 "$asset"; fi | sed 's/ \*/  /' >checksums.txt)

python3 -m http.server 18300 --directory "$(native "$WORK/rel")" >/dev/null 2>&1 &
HTTP_PID=$!
"$WORK/server$ext" server --listen 127.0.0.1:18301 --data-dir "$(native "$WORK/data")" --admin-token lsa_install >/dev/null 2>&1 &
SERVER_PID=$!
trap 'kill $HTTP_PID $SERVER_PID 2>/dev/null || true' EXIT
for _ in $(seq 1 50); do curl -fs http://127.0.0.1:18301/healthz >/dev/null && curl -fs http://127.0.0.1:18300/ >/dev/null && break; sleep 0.2; done

export LANESH_VERSION=$VERSION LANESH_DOWNLOAD_BASE=http://127.0.0.1:18300
export LANESH_HOME
LANESH_HOME=$(native "$WORK/home")

if [ "$os" = windows ]; then
  out=$(powershell -NoProfile -ExecutionPolicy Bypass -Command \
    "& ([scriptblock]::Create((Invoke-RestMethod http://127.0.0.1:18301/install.ps1))) version --short")
else
  out=$(curl -fsSL http://127.0.0.1:18301/install.sh | sh -s -- version --short)
fi
echo "$out"
echo "$out" | tail -n 1 | tr -d '\r' | grep -qx "$VERSION"
"$WORK/home/bin/lanesh$ext" version --short | tr -d '\r' | grep -qx "$VERSION"
echo "install script OK"

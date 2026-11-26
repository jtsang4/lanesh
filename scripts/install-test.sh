#!/usr/bin/env bash
# Runs the server-rendered install script (install.sh, or install.ps1 on
# Windows) against a local fake release and checks the installed binary and
# PATH / symlink integration inside isolated temporary HOME directories.
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
trap 'kill $HTTP_PID $SERVER_PID 2>/dev/null || true; wait $HTTP_PID $SERVER_PID 2>/dev/null || true; rm -rf "$WORK" 2>/dev/null || true' EXIT
for _ in $(seq 1 50); do curl -fs http://127.0.0.1:18301/healthz >/dev/null && curl -fs http://127.0.0.1:18300/ >/dev/null && break; sleep 0.2; done

export LANESH_VERSION=$VERSION LANESH_DOWNLOAD_BASE=http://127.0.0.1:18300

if [ "$os" = windows ]; then
  export LANESH_HOME
  LANESH_HOME=$(native "$WORK/home")
  before_user_path=$(powershell -NoProfile -Command "[Environment]::GetEnvironmentVariable('Path', 'User')")
  out=$(powershell -NoProfile -ExecutionPolicy Bypass -Command \
    "& ([scriptblock]::Create((Invoke-RestMethod http://127.0.0.1:18301/install.ps1))) version --short")
  echo "$out"
  echo "$out" | tail -n 1 | tr -d '\r' | grep -qx "$VERSION"
  "$WORK/home/bin/lanesh$ext" version --short | tr -d '\r' | grep -qx "$VERSION"
  after_user_path=$(powershell -NoProfile -Command "[Environment]::GetEnvironmentVariable('Path', 'User')")
  [ "$before_user_path" = "$after_user_path" ] || { echo "custom LANESH_HOME modified Windows User PATH" >&2; exit 1; }
  echo "install script OK"
  exit 0
fi

# 1. Custom LANESH_HOME must install cleanly without touching ~/.local/bin or shell profiles.
H1="$WORK/h1"
mkdir -p "$H1/.local/bin"
out=$(env -i HOME="$H1" SHELL=/bin/zsh PATH="$H1/.local/bin:/usr/bin:/bin" \
  LANESH_VERSION="$VERSION" LANESH_DOWNLOAD_BASE="$LANESH_DOWNLOAD_BASE" \
  LANESH_HOME="$WORK/custom-home" \
  sh -c 'curl -fsSL http://127.0.0.1:18301/install.sh | sh -s -- version --short')
echo "$out"
echo "$out" | tail -n 1 | tr -d '\r' | grep -qx "$VERSION"
"$WORK/custom-home/bin/lanesh" version --short | tr -d '\r' | grep -qx "$VERSION"
[ ! -e "$H1/.local/bin/lanesh" ] && [ ! -L "$H1/.local/bin/lanesh" ] || { echo "custom LANESH_HOME created symlink" >&2; exit 1; }
[ ! -e "$H1/.zshrc" ] || { echo "custom LANESH_HOME modified .zshrc" >&2; exit 1; }

# 2. Default LANESH_HOME with ~/.local/bin on PATH creates a working symlink immediately.
H2="$WORK/h2"
mkdir -p "$H2"
out=$(env -i HOME="$H2" SHELL=/bin/zsh PATH="$H2/.local/bin:/usr/bin:/bin" \
  LANESH_VERSION="$VERSION" LANESH_DOWNLOAD_BASE="$LANESH_DOWNLOAD_BASE" \
  sh -c 'curl -fsSL http://127.0.0.1:18301/install.sh | sh')
echo "$out"
echo "$out" | grep -Fq "Linked $H2/.local/bin/lanesh -> $H2/.lanesh/bin/lanesh"
[ -L "$H2/.local/bin/lanesh" ] || { echo "expected symlink at $H2/.local/bin/lanesh" >&2; exit 1; }
[ "$(readlink "$H2/.local/bin/lanesh")" = "$H2/.lanesh/bin/lanesh" ] || { echo "wrong symlink target" >&2; exit 1; }
got=$(env -i HOME="$H2" PATH="$H2/.local/bin:/usr/bin:/bin" sh -c 'lanesh version --short')
[ "$got" = "$VERSION" ] || { echo "lanesh via symlink returned $got, want $VERSION" >&2; exit 1; }
[ ! -e "$H2/.zshrc" ] || { echo "must not touch .zshrc when symlink succeeded" >&2; exit 1; }

# 3. Conflict protection: foreign file at ~/.local/bin/lanesh is never overwritten; falls back to ~/bin and warns of shadow.
H3="$WORK/h3"
mkdir -p "$H3/.local/bin" "$H3/bin"
printf '#!/bin/sh\necho foreign\n' >"$H3/.local/bin/lanesh"
chmod 0755 "$H3/.local/bin/lanesh"
out=$(env -i HOME="$H3" SHELL=/bin/zsh PATH="$H3/.local/bin:$H3/bin:/usr/bin:/bin" \
  LANESH_VERSION="$VERSION" LANESH_DOWNLOAD_BASE="$LANESH_DOWNLOAD_BASE" \
  sh -c 'curl -fsSL http://127.0.0.1:18301/install.sh | sh')
echo "$out"
grep -Fq "foreign" "$H3/.local/bin/lanesh" || { echo "foreign file was overwritten" >&2; exit 1; }
[ -L "$H3/bin/lanesh" ] || { echo "expected fallback symlink at $H3/bin/lanesh" >&2; exit 1; }
echo "$out" | grep -Fq "may shadow" || { echo "expected shadow warning" >&2; exit 1; }

# 4. Shell profile fallback (zsh + idempotency + fish) when no writable PATH dir is available.
H4="$WORK/h4"
mkdir -p "$H4"
printf '# existing rc' >"$H4/.zshrc"
out=$(env -i HOME="$H4" SHELL=/bin/zsh PATH="/usr/bin:/bin" \
  LANESH_VERSION="$VERSION" LANESH_DOWNLOAD_BASE="$LANESH_DOWNLOAD_BASE" \
  sh -c 'curl -fsSL http://127.0.0.1:18301/install.sh | sh')
echo "$out"
echo "$out" | grep -Fq "Added ~/.lanesh/bin to ~/.zshrc"
grep -Fq 'export PATH="$HOME/.lanesh/bin:$PATH"' "$H4/.zshrc" || { echo ".zshrc missing PATH export" >&2; exit 1; }
got=$(env -i HOME="$H4" PATH="/usr/bin:/bin" sh -c '. "$HOME/.zshrc" && lanesh version --short')
[ "$got" = "$VERSION" ] || { echo "lanesh after sourcing .zshrc returned $got, want $VERSION" >&2; exit 1; }
# Running a second time must be idempotent (no duplicate entry).
env -i HOME="$H4" SHELL=/bin/zsh PATH="/usr/bin:/bin" \
  LANESH_VERSION="$VERSION" LANESH_DOWNLOAD_BASE="$LANESH_DOWNLOAD_BASE" \
  sh -c 'curl -fsSL http://127.0.0.1:18301/install.sh | sh' >/dev/null
count=$(grep -Fc '.lanesh/bin' "$H4/.zshrc")
[ "$count" -eq 1 ] || { echo ".zshrc had $count entries after second install, want 1" >&2; exit 1; }

# Fish shell profile fallback.
H4F="$WORK/h4-fish"
mkdir -p "$H4F"
env -i HOME="$H4F" SHELL=/usr/bin/fish XDG_CONFIG_HOME="$H4F/.config" PATH="/usr/bin:/bin" \
  LANESH_VERSION="$VERSION" LANESH_DOWNLOAD_BASE="$LANESH_DOWNLOAD_BASE" \
  sh -c 'curl -fsSL http://127.0.0.1:18301/install.sh | sh' >/dev/null
grep -Fq 'set -gx PATH "$HOME/.lanesh/bin" $PATH' "$H4F/.config/fish/config.fish" || { echo "config.fish missing PATH entry" >&2; exit 1; }

# 5. LANESH_NO_MODIFY_PATH=1 skips both symlinks and shell profiles.
H5="$WORK/h5"
mkdir -p "$H5/.local/bin"
out=$(env -i HOME="$H5" SHELL=/bin/zsh PATH="$H5/.local/bin:/usr/bin:/bin" \
  LANESH_NO_MODIFY_PATH=1 \
  LANESH_VERSION="$VERSION" LANESH_DOWNLOAD_BASE="$LANESH_DOWNLOAD_BASE" \
  sh -c 'curl -fsSL http://127.0.0.1:18301/install.sh | sh')
echo "$out"
[ ! -e "$H5/.local/bin/lanesh" ] && [ ! -L "$H5/.local/bin/lanesh" ] || { echo "LANESH_NO_MODIFY_PATH created symlink" >&2; exit 1; }
[ ! -e "$H5/.zshrc" ] || { echo "LANESH_NO_MODIFY_PATH modified .zshrc" >&2; exit 1; }
echo "$out" | grep -Fq "Add it to your PATH" || { echo "expected manual PATH hint" >&2; exit 1; }

echo "install script OK"

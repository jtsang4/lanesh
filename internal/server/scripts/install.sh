#!/bin/sh
# Lanesh installer for macOS and Linux.
#
#   curl -fsSL {{.ServerURL}}/install.sh | sh
#   curl -fsSL {{.ServerURL}}/install.sh | sh -s -- join <lane-url> --key <join-key>
#
# Arguments after `--` are passed to lanesh once it is installed.
# Set LANESH_NO_MODIFY_PATH=1 to skip creating a PATH symlink or editing shell profiles.
set -eu

VERSION="${LANESH_VERSION:-{{.Version}}}"
BASE="${LANESH_DOWNLOAD_BASE:-https://github.com/jtsang4/lanesh/releases/download}"
DEFAULT_HOME="$HOME/.lanesh"
LANESH_DIR="${LANESH_HOME:-$DEFAULT_HOME}"

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

BIN_DIR="$LANESH_DIR/bin"
BIN="$BIN_DIR/lanesh"
mkdir -p "$BIN_DIR"
chmod 0755 "$tmp/lanesh"
mv -f "$tmp/lanesh" "$BIN"
echo "Installed $BIN"

path_has() {
  case ":$PATH:" in
    *":$1:"* | *":$1/:"*) return 0 ;;
    *) return 1 ;;
  esac
}

is_homebrew_dir() {
  [ "$1" = "/opt/homebrew/bin" ] && return 0
  [ -e "$1/brew" ] && return 0
  [ "$1" = "/usr/local/bin" ] && [ -d "/usr/local/Homebrew" ] && return 0
  return 1
}

find_persistent_lanesh() {
  old_ifs=$IFS
  IFS=:
  set -f
  for d in $PATH; do
    [ -n "$d" ] || continue
    case "$d/" in
      */node_modules/.bin/* | */_npx/* | */pnpm-dlx/* | */dlx-* | */bunx-* | */npm-cache/*) continue ;;
    esac
    if [ -x "$d/lanesh" ] && [ ! -d "$d/lanesh" ]; then
      set +f
      IFS=$old_ifs
      printf '%s\n' "$d/lanesh"
      return 0
    fi
  done
  set +f
  IFS=$old_ifs
  return 1
}

is_npm_launcher() {
  f=$1
  if [ -L "$f" ]; then
    t=$(readlink "$f" 2>/dev/null || true)
    case "$t" in
      */lanesh/bin/lanesh.js) return 0 ;;
    esac
  fi
  if [ -f "$f" ]; then
    sz=$(wc -c < "$f" 2>/dev/null | tr -d '[:space:]' || echo 99999)
    if [ -n "$sz" ] && [ "$sz" -le 4096 ] && grep -Fq "lanesh/bin/lanesh.js" "$f" 2>/dev/null; then
      return 0
    fi
  fi
  return 1
}

already_exposed() {
  path_has "$BIN_DIR" && return 0
  existing=$(find_persistent_lanesh || true)
  [ -n "$existing" ] || return 1
  if [ "$existing" -ef "$BIN" ] 2>/dev/null; then
    return 0
  fi
  if [ -L "$existing" ]; then
    target=$(readlink "$existing" 2>/dev/null || true)
    case "$target" in
      "$BIN" | */.lanesh/bin/lanesh)
        [ -e "$existing" ] && return 0
        ;;
    esac
  fi
  if is_npm_launcher "$existing"; then
    return 0
  fi
  return 1
}

try_symlink() {
  for dir in "$HOME/.local/bin" "$HOME/bin" "/usr/local/bin"; do
    path_has "$dir" || continue
    is_homebrew_dir "$dir" && continue
    case "$dir" in
      "$HOME"/*)
        mkdir -p "$dir" 2>/dev/null || continue
        ;;
      *)
        [ -d "$dir" ] || continue
        ;;
    esac
    [ -w "$dir" ] || continue
    link="$dir/lanesh"
    if [ -L "$link" ]; then
      cur=$(readlink "$link" 2>/dev/null || true)
      case "$cur" in
        "$BIN" | */.lanesh/bin/lanesh) ;;
        *) continue ;;
      esac
    elif [ -e "$link" ]; then
      continue
    fi
    tmp_link="$dir/.lanesh.tmp.$$"
    rm -f "$tmp_link" 2>/dev/null || true
    if ln -s "$BIN" "$tmp_link" 2>/dev/null && mv -f "$tmp_link" "$link" 2>/dev/null; then
      echo "Linked $link -> $BIN"
      found=$(find_persistent_lanesh || true)
      if [ -n "$found" ] && [ "$found" != "$link" ] && ! [ "$found" -ef "$BIN" ] 2>/dev/null; then
        echo "Note: $found appears earlier in PATH and may shadow $link."
      fi
      return 0
    fi
    rm -f "$tmp_link" 2>/dev/null || true
  done
  return 1
}

profile_has_lanesh() {
  [ -f "$1" ] || return 1
  awk -v bin="$BIN_DIR" '
    /^[[:space:]]*#/ { next }
    index($0, ".lanesh/bin") > 0 || index($0, bin) > 0 { found = 1; exit }
    END { exit(found ? 0 : 1) }
  ' "$1"
}

display_path() {
  case "$1" in
    "$HOME"/*) printf '~/%s' "${1#"$HOME"/}" ;;
    *) printf '%s' "$1" ;;
  esac
}

add_to_profile() {
  subcmd=${1:-}
  shell_name=$(basename "${SHELL:-sh}")
  case "$shell_name" in
    zsh)
      rc="${ZDOTDIR:-$HOME}/.zshrc"
      line='export PATH="$HOME/.lanesh/bin:$PATH"'
      reload_op="source"
      ;;
    bash)
      if [ "$os" = "darwin" ]; then
        rc="$HOME/.bash_profile"
      else
        rc="$HOME/.bashrc"
      fi
      line='export PATH="$HOME/.lanesh/bin:$PATH"'
      reload_op="source"
      ;;
    fish)
      rc="${XDG_CONFIG_HOME:-$HOME/.config}/fish/config.fish"
      line='set -gx PATH "$HOME/.lanesh/bin" $PATH'
      reload_op="source"
      ;;
    *)
      rc="$HOME/.profile"
      line='export PATH="$HOME/.lanesh/bin:$PATH"'
      reload_op="."
      ;;
  esac

  disp_rc=$(display_path "$rc")
  case "$disp_rc" in
    *" "*) reload="$reload_op \"$disp_rc\"" ;;
    *) reload="$reload_op $disp_rc" ;;
  esac
  # join and update print the reload reminder at the end of their own output.
  defer_hint=0
  case "$subcmd" in
    join | update) defer_hint=1 ;;
  esac

  if profile_has_lanesh "$rc"; then
    if [ "$defer_hint" = 0 ]; then
      echo "Open a new terminal or run: $reload"
    fi
    return 0
  fi

  rc_dir=$(dirname "$rc")
  if mkdir -p "$rc_dir" 2>/dev/null; then
    if [ -s "$rc" ]; then
      if [ -n "$(tail -c 1 "$rc" 2>/dev/null || true)" ]; then
        printf '\n' >> "$rc" 2>/dev/null || return 1
      fi
      printf '\n# Added by Lanesh\n%s\n' "$line" >> "$rc" 2>/dev/null || return 1
    else
      printf '# Added by Lanesh\n%s\n' "$line" >> "$rc" 2>/dev/null || return 1
    fi
    echo "Added $(display_path "$BIN_DIR") to $disp_rc"
    if [ "$defer_hint" = 0 ]; then
      echo "Open a new terminal or run: $reload"
    fi
    return 0
  fi
  return 1
}

modify_path=1
case "${LANESH_NO_MODIFY_PATH:-}" in
  "" | 0 | false | FALSE) ;;
  *) modify_path=0 ;;
esac
[ "${LANESH_DIR%/}" != "${DEFAULT_HOME%/}" ] && modify_path=0

if ! already_exposed; then
  if [ "$modify_path" = 1 ]; then
    if ! try_symlink; then
      if ! add_to_profile "${1:-}"; then
        echo "Add it to your PATH:  export PATH=\"$BIN_DIR:\$PATH\""
      fi
    fi
  elif [ "$#" -eq 0 ]; then
    echo "Add it to your PATH:  export PATH=\"$BIN_DIR:\$PATH\""
  fi
fi

if [ "$#" -gt 0 ]; then
  exec "$BIN" "$@"
fi

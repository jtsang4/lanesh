#!/bin/sh
# Starts a local Lanesh server with two demo machines for UI development.
#
#   scripts/dev.sh          # build, start, seed; Ctrl-C stops everything
#
# Server: http://localhost:8080  admin token: lsa_dev_token
# Data lives in ${LANESH_DEV_DIR:-/tmp/lanesh-dev} and is recreated on each run.
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
DIR="${LANESH_DEV_DIR:-/tmp/lanesh-dev}"
PORT="${LANESH_DEV_PORT:-8080}"
URL="http://localhost:$PORT"
TOKEN=lsa_dev_token

rm -rf "$DIR"
mkdir -p "$DIR"
cd "$ROOT"
go build -o "$DIR/lanesh" ./cmd/lanesh
BIN="$DIR/lanesh"

pids=""
cleanup() { for p in $pids; do kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT INT TERM

LANESH_LISTEN=":$PORT" LANESH_DATA_DIR="$DIR/server" LANESH_ADMIN_TOKEN=$TOKEN LANESH_PUBLIC_URL="$URL" \
  "$BIN" server >"$DIR/server.log" 2>&1 &
pids="$pids $!"
for _ in $(seq 1 50); do curl -fs "$URL/healthz" >/dev/null 2>&1 && break; sleep 0.1; done

api() { curl -fsS -X "$1" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' ${3:+-d "$3"} "$URL/api/v1$2"; }
field() { sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"; }

lane=$(api POST /lanes '{"name":"Studio"}' | field id)
api POST /lanes '{"name":"Personal"}' >/dev/null
join=$(api POST "/lanes/$lane/keys" '{"kind":"join","name":"bootstrap"}' | field token)
api POST "/lanes/$lane/keys" '{"kind":"access","name":"claude-code"}' >/dev/null
api POST "/lanes/$lane/keys" '{"kind":"access","name":"codex","expires_in_days":30}' >/dev/null

skill() { # home name description
  d="$1/.agents/skills/$2"; mkdir -p "$d/scripts" "$d/references"
  printf -- '---\nname: %s\ndescription: %s\n---\n\n# %s\n\n%s\n\n## Usage\n\n```sh\nscripts/run.sh --target staging\n```\n\n- Reads `references/notes.md` for context\n- Writes a report to `out/`\n' "$2" "$3" "$2" "$3" >"$d/SKILL.md"
  printf '#!/bin/sh\necho "running %s"\n' "$2" >"$d/scripts/run.sh"
  printf '# Notes\n\nBackground for %s.\n' "$2" >"$d/references/notes.md"
}

machine() { # name skills...
  name=$1; shift
  home="$DIR/$name"; mkdir -p "$home"
  for s in "$@"; do skill "$home" "$s" "$(printf '%s' "$s" | sed 's/-/ /g'): automates the $s workflow end to end."; done
  HOME="$home" LANESH_HOME="$home/.lanesh" "$BIN" join "$URL/lanes/$lane" --key "$join" --name "$name" --no-service >/dev/null
  HOME="$home" LANESH_HOME="$home/.lanesh" "$BIN" daemon >"$DIR/$name.log" 2>&1 &
  pids="$pids $!"
}

machine macbook-pro deploy-preview release-notes pdf-tools figma-export
machine build-box deploy-preview docker-images gpu-benchmarks
machine nas backup-verify

sleep 2
echo "Lanesh dev server: $URL  (admin token: $TOKEN)"
echo "Lane: $URL/lanes/$lane"
echo "Logs: $DIR/*.log. Press Ctrl-C to stop."
wait

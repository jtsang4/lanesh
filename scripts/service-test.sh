#!/usr/bin/env bash
# Exercises the background service lifecycle against a local server:
# join (install + start) -> connected -> restart -> stop -> start -> leave (uninstall).
#
#   scripts/service-test.sh path/to/lanesh
#
# Used by CI on Linux, macOS and Windows (Git Bash).
set -euo pipefail

BIN=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
PORT=18080
URL="http://127.0.0.1:$PORT"
TOKEN=lsa_service_test
WORK=$(mktemp -d)

native() { if command -v cygpath >/dev/null 2>&1; then cygpath -w "$1"; else echo "$1"; fi; }
export LANESH_HOME
LANESH_HOME=$(native "$WORK/home")

"$BIN" server --listen "127.0.0.1:$PORT" --data-dir "$(native "$WORK/data")" --admin-token "$TOKEN" >"$WORK/server.log" 2>&1 &
SERVER_PID=$!

dump() {
  echo "::group::diagnostics"
  "$BIN" status || true
  echo "--- daemon log"; cat "$WORK/home/logs/daemon.log" 2>/dev/null || true
  echo "--- server log"; cat "$WORK/server.log" || true
  echo "::endgroup::"
}
cleanup() {
  status=$?
  [ $status -ne 0 ] && dump
  "$BIN" leave ci >/dev/null 2>&1 || true
  kill "$SERVER_PID" 2>/dev/null || true
  exit $status
}
trap cleanup EXIT

for _ in $(seq 1 50); do curl -fs "$URL/healthz" >/dev/null && break; sleep 0.2; done

api() { curl -fsS -X "$1" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' ${3:+-d "$3"} "$URL/api/v1$2"; }
field() { sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"; }

LANE=$(api POST /lanes '{"name":"ci"}' | field id)
JOIN=$(api POST "/lanes/$LANE/keys" '{"kind":"join","name":"ci"}' | field token)

wait_online() { # want: true|false
  for _ in $(seq 1 90); do
    if api GET "/lanes/$LANE/machines" | grep -q "\"online\":$1"; then return 0; fi
    sleep 1
  done
  echo "machine did not become online=$1" >&2
  return 1
}

echo "== join"
"$BIN" join "$URL/lanes/$LANE" --key "$JOIN" --name ci-runner
wait_online true
"$BIN" status

echo "== restart"
"$BIN" restart
sleep 2
wait_online true

echo "== stop"
"$BIN" stop
wait_online false

echo "== start"
"$BIN" start
wait_online true

echo "== leave"
"$BIN" leave ci
"$BIN" status | grep -q "not installed"
api GET "/lanes/$LANE/machines" | grep -q '^\[\]'
echo "service lifecycle OK"

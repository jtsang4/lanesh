#!/bin/sh
# Starts a local Lanesh server with three demo machines and sample projects for UI development.
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
PUBLIC_URL="${LANESH_PUBLIC_URL:-$URL}"
TOKEN=lsa_dev_token

rm -rf "$DIR"
mkdir -p "$DIR"
cd "$ROOT"
if [ -n "${LANESH_DEV_VERSION:-}" ]; then
  go build -ldflags "-X github.com/jtsang4/lanesh/internal/version.Version=$LANESH_DEV_VERSION" -o "$DIR/lanesh" ./cmd/lanesh
else
  go build -o "$DIR/lanesh" ./cmd/lanesh
fi
BIN="$DIR/lanesh"

pids=""
cleanup() { for p in $pids; do kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT INT TERM

LANESH_LISTEN=":$PORT" LANESH_DATA_DIR="$DIR/server" LANESH_ADMIN_TOKEN=$TOKEN LANESH_PUBLIC_URL="$PUBLIC_URL" \
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

skill() { # home dir_rel name description body_cmd
  d="$1/$2/$3"; mkdir -p "$d/scripts" "$d/references"
  printf -- '---\nname: %s\ndescription: %s\n---\n\n# %s\n\n%s\n\n## Usage\n\n```sh\n%s\n```\n\n- Reads `references/notes.md` for options and conventions\n- Writes artifacts to `out/`\n' \
    "$3" "$4" "$3" "$4" "$5" >"$d/SKILL.md"
  printf '#!/bin/sh\necho "running %s"\n' "$3" >"$d/scripts/run.sh"
  printf '# Notes\n\nReference notes for %s.\n' "$3" >"$d/references/notes.md"
}

git_repo() { # dir branch
  mkdir -p "$1"
  if command -v git >/dev/null 2>&1; then
    (
      cd "$1"
      git init -q -b "$2"
      git -c user.name=dev -c user.email=dev@example.com commit -q --allow-empty -m "init"
    )
  fi
}

join_machine() { # name
  name=$1
  home="$DIR/$name"
  HOME="$home" LANESH_HOME="$home/.lanesh" "$BIN" join "$URL/lanes/$lane" --key "$join" --name "$name" --no-service >/dev/null
  HOME="$home" LANESH_HOME="$home/.lanesh" "$BIN" daemon >"$DIR/$name.log" 2>&1 &
  pids="$pids $!"
}

# macbook-pro
mbp="$DIR/macbook-pro"
mkdir -p "$mbp"
skill "$mbp" .agents/skills deploy-preview "Build and publish a preview deployment from the current branch." "scripts/run.sh --target staging"
skill "$mbp" .agents/skills figma-export "Export icons and component frames from Figma into local assets." "scripts/run.sh --file design-system"
skill "$mbp" .agents/skills pdf-tools "Extract tables, split pages and convert Markdown reports to PDF." "scripts/run.sh report.md --out out/report.pdf"
skill "$mbp" .agents/skills release-notes "Draft changelog entries from merged pull requests and tag history." "scripts/run.sh --since v1.4.0"
# Shadowed lower-priority copy in ~/.claude/skills
skill "$mbp" .claude/skills pdf-tools "Older local copy shadowed by ~/.agents/skills/pdf-tools." "scripts/run.sh report.md"
git_repo "$mbp/code/storefront" main
printf '# Storefront\n\nFollow the conventions in docs/style.md.\n' >"$mbp/code/storefront/AGENTS.md"
ln -s AGENTS.md "$mbp/code/storefront/CLAUDE.md"
join_machine macbook-pro

# build-box
box="$DIR/build-box"
mkdir -p "$box"
skill "$box" .agents/skills deploy-preview "Build and publish a preview deployment from the current branch." "scripts/run.sh --target staging --linux"
skill "$box" .agents/skills docker-images "Build multi-arch container images and push them to the registry." "scripts/run.sh --platform linux/amd64,linux/arm64"
skill "$box" .agents/skills gpu-benchmarks "Run inference latency and throughput benchmarks on CUDA GPUs." "scripts/run.sh --model qwen-32b --batch 8"
git_repo "$box/builds/storefront" staging
printf '# Storefront build box\n\nIntegration tests require Docker Compose.\n' >"$box/builds/storefront/AGENTS.md"
git_repo "$box/work/inference-eval" feat/fp8-kv
printf '# Inference eval\n\nPin CUDA_VISIBLE_DEVICES before launching sweeps.\n' >"$box/work/inference-eval/AGENTS.md"
join_machine build-box

# nas
nas="$DIR/nas"
mkdir -p "$nas"
skill "$nas" .agents/skills backup-verify "Check nightly ZFS snapshots and verify restore checksums." "scripts/run.sh --pool tank"
join_machine nas

mbp_id=$(tr -d '\n' <"$mbp/.lanesh/machine-id")
box_id=$(tr -d '\n' <"$box/.lanesh/machine-id")

# Seed sample projects with revision history
storefront_id=$(api POST "/lanes/$lane/projects" \
  "{\"name\":\"storefront\",\"locations\":[{\"machine_id\":\"$mbp_id\",\"path\":\"~/code/storefront\"}],\"context\":\"# Storefront\\n\\nCustomer-facing web app and checkout API.\"}" | field id)
api PATCH "/lanes/$lane/projects/$storefront_id" \
  "{\"locations\":[{\"machine_id\":\"$mbp_id\",\"path\":\"~/code/storefront\"},{\"machine_id\":\"$box_id\",\"path\":\"~/builds/storefront\"}]}" >/dev/null
api PATCH "/lanes/$lane/projects/$storefront_id" \
  "{\"context\":\"# Storefront\\n\\nCustomer-facing web app and checkout API, deployed from \`build-box\` and developed locally on \`macbook-pro\`.\\n\\n## Rules\\n\\n- Create a linked git worktree for each feature branch; leave \`main\` clean.\\n- Run \`pnpm test\` locally and integration suites on \`build-box\`.\\n- Use the \`deploy-preview\` skill on \`build-box\` for staging builds.\\n\\n## Status\\n\\nCheckout v2 is in staging; Apple Pay webhook retries are under load test.\"}" >/dev/null

api POST "/lanes/$lane/projects" \
  "{\"name\":\"inference-eval\",\"locations\":[{\"machine_id\":\"$box_id\",\"path\":\"~/work/inference-eval\"}],\"context\":\"# Inference eval\\n\\nLatency and throughput benchmark harness for local model serving on \`build-box\`.\\n\\n## Rules\\n\\n- Check GPU utilization with \`nvidia-smi\` before starting a sweep.\\n- Archive result JSON under \`results/\` and compare against the \`main\` baseline.\"}" >/dev/null

sleep 2
echo "Lanesh dev server: $URL  (admin token: $TOKEN)"
echo "Lane: $URL/lanes/$lane"
echo "Logs: $DIR/*.log. Press Ctrl-C to stop."
wait

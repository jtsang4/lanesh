#!/usr/bin/env bash
# Signs one or more macOS Mach-O binaries with Developer ID Application
# (Hardened Runtime + secure timestamp + stable identifier for macOS TCC)
# and optionally notarizes them together in a single Apple Notary submission.
#
#   scripts/sign-macos.sh <binary> [binary...]
#
# Environment:
#   MACOS_SIGN_IDENTIFIER  codesign identifier (default: com.skemind.lanesh)
#   MACOS_SIGN_IDENTITY    Developer ID Application identity; unset picks the
#                          keychain's sole Developer ID Application identity.
#   MACOS_NOTARY_KEY       path to App Store Connect Team API Key (.p8);
#                          requires MACOS_NOTARY_KEY_ID and MACOS_NOTARY_ISSUER_ID.
#   MACOS_NOTARY_PROFILE   keychain profile from `notarytool store-credentials`
#                          (alternative to MACOS_NOTARY_KEY*).
set -euo pipefail

if [ $# -lt 1 ]; then
  echo "usage: $0 <binary> [binary...]" >&2
  exit 2
fi

for f in "$@"; do
  if [ ! -f "$f" ]; then
    echo "binary not found: $f" >&2
    exit 1
  fi
done

pick_identity() {
  if [ -n "${MACOS_SIGN_IDENTITY:-}" ]; then
    printf '%s\n' "$MACOS_SIGN_IDENTITY"
    return
  fi
  local found
  found="$(security find-identity -v -p codesigning 2>/dev/null \
    | sed -nE 's/^ *[0-9]+\) [0-9A-F]{40} "(Developer ID Application: .*)"$/\1/p' | sort -u)"
  case "$(printf '%s' "$found" | grep -c . || true)" in
    0)
      echo "no Developer ID Application identity found in keychain (set MACOS_SIGN_IDENTITY)" >&2
      return 1 ;;
    1)
      echo "==> signing with keychain identity: $found" >&2
      printf '%s\n' "$found" ;;
    *)
      {
        echo "multiple Developer ID Application identities found; set MACOS_SIGN_IDENTITY:"
        printf '%s\n' "$found" | while IFS= read -r i; do echo "  MACOS_SIGN_IDENTITY=\"$i\""; done
      } >&2
      return 1 ;;
  esac
}

validate_notary() {
  local has_profile=0 has_any_key=0
  [ -n "${MACOS_NOTARY_PROFILE:-}" ] && has_profile=1
  if [ -n "${MACOS_NOTARY_KEY:-}" ] || [ -n "${MACOS_NOTARY_KEY_ID:-}" ] || [ -n "${MACOS_NOTARY_ISSUER_ID:-}" ]; then
    has_any_key=1
  fi
  if [ "$has_profile" -eq 1 ] && [ "$has_any_key" -eq 1 ]; then
    echo "set either MACOS_NOTARY_PROFILE or MACOS_NOTARY_KEY/KEY_ID/ISSUER_ID, not both" >&2
    return 1
  fi
  if [ "$has_any_key" -eq 1 ]; then
    if [ -z "${MACOS_NOTARY_KEY:-}" ] || [ -z "${MACOS_NOTARY_KEY_ID:-}" ] || [ -z "${MACOS_NOTARY_ISSUER_ID:-}" ]; then
      echo "notarization with an API key requires MACOS_NOTARY_KEY, MACOS_NOTARY_KEY_ID, and MACOS_NOTARY_ISSUER_ID" >&2
      return 1
    fi
    if [ ! -f "$MACOS_NOTARY_KEY" ]; then
      echo "MACOS_NOTARY_KEY does not point to a file: $MACOS_NOTARY_KEY" >&2
      return 1
    fi
  fi
  if [ "$has_profile" -eq 1 ] || [ "$has_any_key" -eq 1 ]; then
    notarize=1
  fi
}

identifier="${MACOS_SIGN_IDENTIFIER:-com.skemind.lanesh}"
identity="$(pick_identity)"
notarize=0
validate_notary

for bin in "$@"; do
  chmod 755 "$bin"
  xattr -c "$bin" 2>/dev/null || true
  codesign --force --timestamp --options runtime \
    -i "$identifier" \
    -s "$identity" \
    "$bin"
  codesign --verify --strict --verbose=2 "$bin"
  echo "==> signed $(basename "$bin"): $(codesign -dv "$bin" 2>&1 | grep -E '^(Identifier|TeamIdentifier|Flags)' | tr '\n' ' ')"
done

if [ "$notarize" -eq 1 ]; then
  work="$(mktemp -d)"
  trap 'rm -rf "$work"' EXIT
  stage="$work/notary"
  mkdir -p "$stage"
  idx=0
  for bin in "$@"; do
    idx=$((idx + 1))
    cp "$bin" "$stage/${idx}-$(basename "$bin")"
  done
  zip="$work/lanesh-notary.zip"
  ditto -c -k "$stage" "$zip"

  declare -a notary_args
  if [ -n "${MACOS_NOTARY_PROFILE:-}" ]; then
    notary_args=(--keychain-profile "$MACOS_NOTARY_PROFILE")
  else
    notary_args=(--key "$MACOS_NOTARY_KEY" --key-id "$MACOS_NOTARY_KEY_ID" --issuer "$MACOS_NOTARY_ISSUER_ID")
  fi

  report="$work/notary.json"
  echo "==> notarizing $# binary(ies)"
  rc=0
  xcrun notarytool submit "$zip" "${notary_args[@]}" --wait --output-format json >"$report" || rc=$?

  parsed="$(python3 - "$report" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print("\t")
    raise SystemExit(0)
print(f"{data.get('status', '')}\t{data.get('id', '')}")
PY
)"
  IFS=$'\t' read -r status sub_id <<< "$parsed"
  if [ "$rc" -ne 0 ] || [ "$status" != "Accepted" ]; then
    echo "notarization failed (exit=$rc status=${status:-unknown} id=${sub_id:-none})" >&2
    cat "$report" >&2
    if [ -n "$sub_id" ]; then
      echo "==> notarization log ($sub_id)" >&2
      xcrun notarytool log "$sub_id" "${notary_args[@]}" >&2 || true
    fi
    exit 1
  fi
  echo "==> notarization accepted ($sub_id)"
fi

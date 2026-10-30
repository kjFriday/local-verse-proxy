#!/usr/bin/env bash
# Mint a scoped key for one seat. The master key stays in seats.env and
# is never printed.
#
#   ./mint-key.sh <seat> [--models a,b] [--budget USD] [--duration 30d]
#   ./mint-key.sh --revoke <key>
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
env_file="${SEATS_ENV:-$here/seats.env}"
[[ -f "$env_file" ]] || { echo "missing $env_file" >&2; exit 1; }
set -a; source "$env_file"; set +a

: "${LITELLM_MASTER_KEY:?not set in $env_file}"
url="${PROXY_URL:-http://localhost:4000}"

usage() { sed -n '2,6p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }

# Send the master key through a header file on stdin, so it never shows
# up in ps or shell history.
post() {
  curl -sS --fail-with-body -X POST "$url$1" \
    -H @- -H 'Content-Type: application/json' -d "$2" \
    <<< "Authorization: Bearer $LITELLM_MASTER_KEY"
}

if [[ "${1:-}" == "--revoke" ]]; then
  [[ -n "${2:-}" ]] || usage
  post /key/delete "$(jq -nc --arg k "$2" '{keys: [$k]}')" >/dev/null
  echo "revoked"
  exit 0
fi

seat="${1:-}"
[[ -n "$seat" && "$seat" != -* ]] || usage
shift

models="${DEFAULT_MODELS:-local}"
budget="${DEFAULT_BUDGET:-5}"
duration="${DEFAULT_DURATION:-30d}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --models)   models="$2";   shift 2 ;;
    --budget)   budget="$2";   shift 2 ;;
    --duration) duration="$2"; shift 2 ;;
    *) usage ;;
  esac
done

body="$(jq -nc \
  --arg seat "$seat" --arg models "$models" \
  --argjson budget "$budget" --arg duration "$duration" \
  '{key_alias: $seat,
    models: ($models | split(",")),
    max_budget: $budget,
    duration: $duration,
    metadata: {seat: $seat}}')"

post /key/generate "$body" | jq -r '.key'

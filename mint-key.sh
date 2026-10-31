#!/usr/bin/env bash
# Mint a scoped key for one seat. The master key stays in seats.env and
# is never printed.
#
#   ./mint-key.sh <seat> [--models a,b] [--budget USD] [--duration 30d]
#                        [--budget-duration 1d]
#   ./mint-key.sh --revoke <key>
#   ./mint-key.sh --revoke-seat <seat>
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
env_file="${SEATS_ENV:-$here/seats.env}"
[[ -f "$env_file" ]] || { echo "missing $env_file" >&2; exit 1; }
# Plain source, no export: secrets stay out of child process envs.
source "$env_file"

: "${LITELLM_MASTER_KEY:?not set in $env_file}"
if [[ "$LITELLM_MASTER_KEY${LITELLM_SALT_KEY:-}" == *change-me* ]]; then
  echo "replace the change-me values in $env_file first" >&2
  exit 1
fi
url="${PROXY_URL:-http://localhost:4000}"

usage() { sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }
die() { echo "$*" >&2; exit 2; }

# Send the master key through a header file on stdin, so it never shows
# up in ps or shell history. On failure, print LiteLLM's error message.
post() {
  local resp
  if ! resp="$(curl -sS --fail-with-body -X POST "$url$1" \
      -H @- -H 'Content-Type: application/json' -d "$2" \
      <<< "Authorization: Bearer $LITELLM_MASTER_KEY")"; then
    jq -r '.error.message // .detail // .' <<< "$resp" >&2 2>/dev/null \
      || echo "$resp" >&2
    exit 1
  fi
  printf '%s' "$resp"
}

case "${1:-}" in
  --revoke)
    [[ -n "${2:-}" ]] || usage
    post /key/delete "$(jq -nc --arg k "$2" '{keys: [$k]}')" >/dev/null
    echo "revoked"
    exit 0 ;;
  --revoke-seat)
    [[ -n "${2:-}" ]] || usage
    post /key/delete "$(jq -nc --arg a "$2" '{key_aliases: [$a]}')" \
      >/dev/null
    echo "revoked seat $2"
    exit 0 ;;
esac

seat="${1:-}"
[[ -n "$seat" && "$seat" != -* ]] || usage
shift

models="${DEFAULT_MODELS:-local}"
budget="${DEFAULT_BUDGET:-5}"
duration="${DEFAULT_DURATION:-30d}"
bdur=""  # empty: the budget is a lifetime total, as before

while [[ $# -gt 0 ]]; do
  [[ $# -ge 2 ]] || usage
  case "$1" in
    --models)   models="$2";   shift 2 ;;
    --budget)   budget="$2";   shift 2 ;;
    --duration) duration="$2"; shift 2 ;;
    --budget-duration) bdur="$2"; shift 2 ;;
    *) usage ;;
  esac
done

[[ "$budget" =~ ^[0-9]+([.][0-9]+)?$ ]] \
  || die "--budget must be a number of US dollars, like 5 or 2.50"
# LiteLLM units; note "m" is minutes and "mo" is months.
[[ "$duration" =~ ^[0-9]+(s|m|h|d|w|mo)$ ]] \
  || die "--duration must be a number and a unit, like 30d, 12h or 2w"
# What LiteLLM accepts: a whole number and s, m, h, d or w; or 1mo
# (LiteLLM refuses 2mo and up).
[[ -z "$bdur" || "$bdur" =~ ^([1-9][0-9]*[smhdw]|1mo)$ ]] \
  || die "--budget-duration must look like 30m, 12h, 1d, 1w or 1mo"

body="$(jq -nc \
  --arg seat "$seat" --arg models "$models" \
  --argjson budget "$budget" --arg duration "$duration" \
  --arg bdur "$bdur" \
  '{key_alias: $seat,
    models: ($models | split(",") | map(gsub("^\\s+|\\s+$"; ""))
                     | map(select(length > 0))),
    max_budget: $budget,
    duration: $duration,
    metadata: {seat: $seat}}
   + (if $bdur == "" then {} else {budget_duration: $bdur} end)')"

# An empty list means "all models" to LiteLLM. Never mint that.
[[ "$(jq '.models | length' <<< "$body")" -gt 0 ]] \
  || die "--models must name at least one model"

post /key/generate "$body" | jq -er '.key'

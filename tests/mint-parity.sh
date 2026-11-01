#!/usr/bin/env bash
# Parity test: runs mint-key.sh and the Go mint-key side by side
# against a real, running proxy, over a table of cases, and compares
# what each prints (stdout, stderr) and its exit code. For each key
# made, it also asks the proxy what the key allows, and compares that.
# Then it checks that Front Desk and the office game read both alike,
# and that Go's Front Desk and office game (lvp) say what Python's do
# (go run ./tests/parity/callers).
#
#   tests/mint-parity.sh [path/to/go/mint-key]
#
# The path may be the mint-key program or a two-line wrapper that runs
# `lvp mint-key "$@"`. LVP names the lvp to test the callers with;
# without it, it is built (Go needed).
#
# Run it on the proxy machine, with the proxy up and seats.env filled
# in. It needs bash, curl, jq and python3 (3.11 or newer); with no
# path given it builds the Go mint-key, which needs Go. It mints and
# revokes keys for seats named parity-<number>-..., all gone at the
# end. Keys show as sk-<KEY>; the master key is never printed.
#
# The Go version differs on purpose in a few places (README.md, "The
# Go version"). Before comparing, both outputs are put in one shape:
#   - "curl: (22) The requested URL returned error: 400" and "the
#     proxy returned error: 400 (Bad Request)..." become "HTTP 400"
#   - curl's (7), (52) and (56) lines, and "Failed to connect to the
#     proxy..." with the hint line after it, become "NO ANSWER"
#   - exit code 4 (jq's, after a failed mint) counts as 1
#   - the usage text becomes "USAGE" (it names ./mint-key.sh or
#     mint-key)
# Cases marked "differs" are known, documented differences: both
# sides are shown, and they do not fail the test.
set -uo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
# A path given as ./mint-key means from where you ran this.
go_bin="${1:-}"
[[ -z "$go_bin" || "$go_bin" == /* ]] || go_bin="$PWD/$go_bin"
cd "$repo" || exit 1
sh="$repo/mint-key.sh"
env_main="${SEATS_ENV:-$repo/seats.env}"
[[ -f "$env_main" ]] || { echo "missing $env_main" >&2; exit 1; }

tmp="$(mktemp -d)"
pids=()
seats=()
cleanup() {
  for s in "${seats[@]}"; do
    SEATS_ENV="$env_main" "$sh" --revoke-seat "$s" >/dev/null 2>&1
  done
  for p in "${pids[@]}"; do kill "$p" 2>/dev/null; done
  rm -rf "$tmp"
}
trap cleanup EXIT

if [[ -z "$go_bin" ]]; then
  go_bin="$tmp/mint-key"
  go build -o "$go_bin" ./cmd/mint-key || exit 1
fi
[[ -x "$go_bin" ]] || { echo "not a program: $go_bin" >&2; exit 1; }

# --- Settings files, one per situation -------------------------------
url="$(set +u; source "$env_main"
  echo "${PROXY_URL:-http://localhost:4000}")"
free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0))
print(s.getsockname()[1])'
}
variant() {  # variant NAME SED-SCRIPT: a copy of seats.env, edited
  sed -E "$2" "$env_main" > "$tmp/$1.env"
  chmod 600 "$tmp/$1.env"
}
set_url() { variant "$1" "s#^PROXY_URL=.*#PROXY_URL=$2#"; }

variant main ''
variant badkey \
  's/^LITELLM_MASTER_KEY=.*/LITELLM_MASTER_KEY=sk-parity-wrong-1234/'
variant nomaster 's/^LITELLM_MASTER_KEY=.*/LITELLM_MASTER_KEY=/'
variant changeme 's/^LITELLM_SALT_KEY=.*/LITELLM_SALT_KEY=sk-change-me/'
# The same settings, written with export, quotes and comments.
variant styled \
  "s/^([A-Z_]+)=(.*)$/export \\1='\\2'  # a comment/; s/^# .*/  &/"
variant crlf 's/$/\r/'
# Two settings on one line, and an export of a name set above.
variant several ''
printf 'DEFAULT_BUDGET=3 DEFAULT_DURATION=1h\nexport PROXY_URL\n' \
  >> "$tmp/several.env"
set_url down "http://127.0.0.1:$(free_port)"
set_url noscheme "${url#*://}"
set_url slash "$url/"

# A server that hangs up at once, and one that answers 501 in HTML.
hang_port="$(free_port)"
python3 -c '
import socket, sys
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", int(sys.argv[1]))); s.listen(8)
while True:
    c, _ = s.accept(); c.close()' "$hang_port" &
pids+=($!)
set_url hangup "http://127.0.0.1:$hang_port"
html_port="$(free_port)"
mkdir "$tmp/www"
python3 -m http.server --bind 127.0.0.1 --directory "$tmp/www" \
  "$html_port" >/dev/null 2>&1 &
pids+=($!)
set_url notjson "http://127.0.0.1:$html_port"
sleep 1

# --- Running and comparing -------------------------------------------
# GET from the proxy with the master key, sent on stdin as in
# mint-key.sh, so it is not on a command line.
api_get() {
  ( set +u; source "$env_main"
    curl -sS "${PROXY_URL:-http://localhost:4000}$1" -H @- \
      <<< "Authorization: Bearer $LITELLM_MASTER_KEY" )
}

# What a key allows, without the key itself or times of day.
key_facts() {
  api_get "/key/info?key=$1" | jq -c '.info | {key_alias, models,
    max_budget, budget_duration, metadata,
    life_minutes: (if .expires == null then null else
      (((.expires | sub("\\.[0-9]+"; "") | sub("\\+00:00$"; "Z")
         | fromdateiso8601)
        - (.created_at | sub("\\.[0-9]+"; "") | sub("\\+00:00$"; "Z")
         | fromdateiso8601)) / 60 | round) end)}'
}

norm() {
  if head -1 "$1" | grep -q '^Mint a scoped key for one seat\.'; then
    echo USAGE
    return
  fi
  sed -E \
    -e 's/sk-[A-Za-z0-9_-]{8,}/sk-<KEY>/g' \
    -e 's/[0-9a-f]{64}/<hash>/g' \
    -e 's/^curl: \(22\) The requested URL .* error: ([0-9]+)$/HTTP \1/' \
    -e 's/^the proxy returned error: ([0-9]+)( .*)?$/HTTP \1/' \
    -e 's/^curl: \((7|52|56)\) .*/NO ANSWER/' \
    -e 's/^Failed to connect to the proxy at .*/NO ANSWER/' \
    -e '/^Is it running\? Check with: docker compose ps\./d' \
    -e 's/^.*mint-key\.sh: line [0-9]+: //' \
    "$1"
}

n=0 fails=0 known=0
KEY=""
setup_none() { :; }
setup_taken() { SEATS_ENV="$env_main" "$sh" "$seat" >/dev/null; }
setup_key() { KEY="$(SEATS_ENV="$env_main" "$sh" "$seat")"; }

# one IMPL SIDE VARIANT ARGS...: run once, keep stdout, stderr, exit.
one() {
  local impl=$1 side=$2 v=$3; shift 3
  local args=() a envf="$tmp/$v.env"
  [[ $v == missing ]] && envf="$tmp/no-such.env"
  for a in "$@"; do args+=("${a//@KEY@/$KEY}"); done
  SEATS_ENV="$envf" "$impl" "${args[@]}" \
    > "$tmp/$side.out" 2> "$tmp/$side.err"
  echo $? > "$tmp/$side.rc"
  : > "$tmp/$side.facts"
  local k; k="$(cat "$tmp/$side.out")"
  if [[ $(cat "$tmp/$side.rc") == 0 && $k == sk-* ]]; then
    key_facts "$k" > "$tmp/$side.facts"
  fi
  SEATS_ENV="$env_main" "$sh" --revoke-seat "$seat" >/dev/null 2>&1
}

# check EXPECT VARIANT SETUP ARGS...: EXPECT is "same" or "differs".
# In ARGS, SEAT is this case's seat name and @KEY@ a key from setup.
check() {
  local expect=$1 v=$2 setup=$3; shift 3
  n=$((n + 1))
  seat="parity-$$-$n"
  seats+=("$seat")
  local args=() a
  for a in "$@"; do args+=("${a//SEAT/$seat}"); done
  "setup_$setup"; one "$sh" a "$v" "${args[@]}"
  "setup_$setup"; one "$go_bin" b "$v" "${args[@]}"
  local ra rb
  ra="$(cat "$tmp/a.rc")" rb="$(cat "$tmp/b.rc")"
  [[ $ra == 4 ]] && ra=1
  local same=1
  [[ $ra == "$rb" ]] || same=0
  for f in out err; do
    cmp -s <(norm "$tmp/a.$f") <(norm "$tmp/b.$f") || same=0
  done
  cmp -s "$tmp/a.facts" "$tmp/b.facts" || same=0
  local label="$v: ${args[*]}"
  label="${label//$seat/SEAT}"
  if [[ $same == 1 ]]; then
    printf 'same     %2d  %s  -> exit %s\n' "$n" "$label" "$rb"
    [[ $expect == differs ]] && known=$((known + 1))
    return
  fi
  if [[ $expect == differs ]]; then
    printf 'differs  %2d  %s  (known, documented)\n' "$n" "$label"
    known=$((known + 1))
  else
    printf 'FAIL     %2d  %s\n' "$n" "$label"
    fails=$((fails + 1))
  fi
  for s in a b; do
    [[ $s == a ]] && echo "  mint-key.sh: exit $(cat "$tmp/a.rc")" \
      || echo "  Go mint-key: exit $(cat "$tmp/b.rc")"
    norm "$tmp/$s.out" | sed 's/^/    out| /'
    norm "$tmp/$s.err" | sed 's/^/    err| /'
    sed 's/^/    key| /' "$tmp/$s.facts"
  done
}

echo "mint-key.sh vs $go_bin, proxy at $url"
echo "-- usage"
check same main none
check same main none --help
check same main none -x
check same main none SEAT --models
check same main none SEAT --bogus 1
check same main none SEAT extra
check same main none --revoke
check same main none --revoke-seat
echo "-- values"
check same main none SEAT --budget abc
check same main none SEAT --budget '$20'
check same main none SEAT --budget 1e3
check same main none SEAT --duration 30
check same main none SEAT --duration 1y
check same main none SEAT --budget-duration 2mo
check same main none SEAT --budget-duration 0d
check same main none SEAT --models ' , '
check same main none SEAT --models ''
check same main none SEAT --models '' --duration x --budget x
echo "-- seats.env"
check same missing none SEAT
check same nomaster none SEAT
check same changeme none --revoke-seat SEAT
check same styled none SEAT --budget 1
check same several none SEAT
check same noscheme none SEAT --budget 1
echo "-- minting and revoking"
check same main none SEAT
check same main none SEAT --models 'local, claude' --budget 2.50 \
  --duration 1h --budget-duration 1d
check same main none SEAT --budget 007 --duration 1mo \
  --budget-duration 1mo
check same main none SEAT --budget 0 --models nope --duration 30m
check same main none SEAT --budget-duration ''
check same main key --revoke-seat SEAT
check same main key --revoke @KEY@
check same main key --revoke-seat SEAT ignored-extra
echo "-- errors from the proxy"
check same main taken SEAT
check same main none --revoke-seat SEAT
check same main none --revoke sk-parity-no-such-key
check same badkey none SEAT
check same badkey none --revoke-seat SEAT
check same down none SEAT
check same down none --revoke k
check same hangup none SEAT
check same notjson none SEAT
echo "-- known differences"
check differs slash none SEAT --budget 1
check differs crlf none SEAT --budget 1

# --- Callers: Front Desk and the office game -------------------------
echo "-- callers"
seats+=("parity-$$-desk" "parity-$$-game" "parity-$$-g-g1"
  "parity-$$-desk2")
python3 - "$sh" "$go_bin" "$tmp" "$env_main" "$$" <<'PY' || fails=$((fails+1))
import subprocess, sys, types
from pathlib import Path
sh, go, tmp, env_main, pid = sys.argv[1:]
sys.path[:0] = ["gui", "game"]
import frontdesk, office
frontdesk.log = lambda msg: None

def desk(impl, envf):
    """What Front Desk's App would do, with mint-key at impl."""
    app = types.SimpleNamespace(
        mint=Path(impl), docker=False, tool="mint-key.sh",
        settings=types.SimpleNamespace(path=Path(envf),
                                       scrub=lambda t: t))
    run = lambda args: frontdesk.App.run_mint(app, args)
    seat = f"parity-{pid}-desk"
    said = []
    ok = run([seat, "--models", "local", "--budget", "1",
              "--duration", "1h"])
    said.append(bool(ok.returncode == 0 and
                     frontdesk.KEY_RE.fullmatch(ok.stdout.strip())))
    for args in ([seat], ["--revoke-seat", seat],
                 ["--revoke-seat", seat]):
        r = run(args)
        said.append("ok" if r.returncode == 0 else
                    frontdesk.App.plain_error(app, r.stderr))
    for v in ("badkey", "down", "hangup", "changeme"):
        app.settings.path = Path(f"{tmp}/{v}.env")
        r = run(["--revoke-seat", seat])
        said.append(frontdesk.App.plain_error(app, r.stderr))
    return said

def game(impl):
    """The office game's own mint() and revoke(), with impl."""
    office.MINT = Path(impl)
    office.KEYS = Path(tmp) / "keys"
    rules = {"mana": {"daily": 10, "usd": 0.01},
             "office": {"models": ["local"], "key_life": "1h"},
             "day": {"length": "1d"}}
    seat = f"parity-{pid}-game"
    return [office.mint(seat, rules),
            office.mint(seat, rules) is not None,
            office.revoke(seat), office.revoke(seat)]

def clearer(x, y):
    """Known: Front Desk words Go's message, but not curl's raw one
    (curl: (56), say, which it has no words for)."""
    return isinstance(x, str) and x.startswith("mint-key.sh said: ") \
        and isinstance(y, str) and not y.startswith("mint-key.sh said")

fails = 0
for name, fn in (("Front Desk", desk), ("office game", game)):
    args = (env_main,) if fn is desk else ()
    a, b = fn(sh, *args), fn(go, *args)
    bad = [x != y and not clearer(x, y) for x, y in zip(a, b)]
    fails += any(bad)
    print("FAIL    " if any(bad) else "same    ", name)
    for x, y, wrong in zip(a, b, bad):
        mark = "  " if x == y else "!=" if wrong else "~~"
        print("   ", mark, repr(x)[:70])
        if x != y:
            print("       Go:", repr(y)[:70],
                  "" if wrong else "(known: clearer)")
sys.exit(1 if fails else 0)
PY

echo "-- callers, Go (lvp desk and lvp game)"
lvp="${LVP:-}"
[[ -z "$lvp" || "$lvp" == /* ]] || lvp="$PWD/$lvp"
if [[ -z "$lvp" ]]; then
  lvp="$tmp/lvp"
  go build -o "$lvp" ./cmd/lvp || fails=$((fails+1))
fi
go run ./tests/parity/callers "$lvp" "$tmp" "$env_main" "$$" \
  || fails=$((fails+1))

echo "-- $n cases: $((n - fails - known)) same, $known known" \
  "differences, $fails failed"
[[ $fails == 0 ]]

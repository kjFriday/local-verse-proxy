# Performance: Python and Go Front Desk, A/B

Measured on 2026-11-01 in the sandbox (Linux amd64, 4 CPUs, 15 GB),
one session (fix round P1), on stack `lvp-p1` (LiteLLM v1.106.6 built
from PyPI and Postgres 16 in Docker, the practice model `local`
served by a fake Ollama) with proxy port 4231. All three desks ran on
the host, in local mode, in the same proxy folder: Python
`gui/frontdesk.py` (py-reference, Python 3.13) on port 8131, Go `lvp
desk` (this tree, go1.26.0) on 8031, and the Go desk from before the
fix (the commit before it) on 8031 in its place, for the "before"
column. 200 cards (seeded straight in LiteLLM, as seed.py made them
with mint-key.sh), 202 tally rows, and a year of history: 146,001
daily spend rows (200 seats x 365 days x 2 models, written into
`LiteLLM_DailyUserSpend` with SQL), as the reference's 146k.

The tests are `tests/e2e/perf` (ports of load.py, mint_ab.py,
startup.py and sse_bench2.py, and Appendix A's keep-alive rate, 50
page loads at once and static total), and for the tally rows
`tests/reference/perf/tally_bench.py` against Python with its Go
twin, `TestTallyBench` in `internal/frontdesk/desk` (e2e tag).
Nothing else measured while these ran (the other helper waited on a
lock file); 60 s of quiet before each stream run.

| Measure                                | Python   | Go       | Target met |
|----------------------------------------|----------|----------|------------|
| Start to first page, p50 (2)           | 119 ms   | 18 ms    | yes        |
| Start to first state, p50 (2)          | 276 ms   | 183 ms   | yes        |
| Memory 5 s after start (2)             | 29.7 MB  | 20.1 MB  | yes        |
| Memory, 20 open pages, idle            | 32.7 MB  | 18.5 MB  | yes        |
| Memory, 20 open pages, traffic         | 33.8 MB  | 21.0 MB  | yes        |
| Page load, 6 GETs, 1 viewer, p50       | 2.35 ms  | 1.58 ms  | yes        |
| Page load, 20 viewers, p50             | 44.26 ms | 6.58 ms  | yes        |
| `/api/state` 200 gzip, 1 viewer, p50   | 0.25 ms  | 0.22 ms  | yes        |
| `/api/state` 200 gzip, 20 viewers, p50 | 3.95 ms  | 0.70 ms  | yes        |
| `/api/state` 304, 20 viewers, p50      | 3.72 ms  | 0.37 ms  | yes        |
| Mint, p50, no load                     | 73.8 ms  | 41.9 ms  | yes        |
| Mint, p50, 20 viewers                  | 108.5 ms | 54.3 ms  | yes        |
| Cancel, p50, no load                   | 56.0 ms  | 30.8 ms  | yes        |
| Cancel, p50, 20 viewers                | 79.2 ms  | 37.2 ms  | yes        |
| Mint, p50 (mint_ab, 20 cycles)         | 69.1 ms  | 39.9 ms  | yes        |
| Page wrong right after mint or cancel  | 0 of 40  | 0 of 40  | yes        |
| Live stream per page, traffic (1)      | 3.78 MB/h| 3.78 MB/h| yes        |
| Live stream per page, idle             | 0.01 MB/h| 0.01 MB/h| yes        |
| Front Desk CPU, 20 pages, idle         | 0.49 %   | 0.31 %   | yes        |
| Front Desk CPU, 20 pages, traffic      | 0.68 %   | 0.37 %   | yes        |
| Front Desk CPU, 1 page, idle           | 0.33 %   | 0.33 %   | yes        |
| Front Desk CPU, 1 page, traffic        | 0.36 %   | 0.33 %   | yes        |
| Tally today, p50                       | 2.75 ms  | 2.80 ms  | yes (3)    |
| History, p50                           | 177 ms   | 190 ms   | yes (3)    |
| psql start, once                       | 155 ms   | 148 ms   | yes        |
| Whole check, p50                       | 7.41 ms  | 4.40 ms  | yes        |
| Watching cost, Postgres                | 0.96 %   | 0.93 %   | yes        |
| Watching cost, LiteLLM                 | 1.15 %   | 1.12 %   | yes        |
| Static files zipped, total             | 18,848 B | 18,886 B | yes (4)    |
| `/api/state` zipped, 200 cards         | 7,620 B  | 7,539 B  | yes        |
| Keep-alive requests a second, static   | 4,823    | 24,777   | yes        |
| Keep-alive requests a second, 304      | 4,212    | 31,401   | yes        |
| 50 page loads at once, worst of 5      | 162 ms   | 23 ms    | yes, no    |
|                                        |          |          | stall      |

(1) Round 1. In round 2 Go's viewers saw 14 changes in the 90 s
window and Python's 13 (each change is one state fetch): 4.50 against
4.23 MB/h, the same per change, within the 10% the spec allows.

(2) Not run again in this session: from C4's A/B the same day (stack
`lvp-c4`, perf-results before this round). The fix does not touch
start-up.

(3) Same SQL text in both (C1's and C2's goldens hold it), so the
time is Postgres's: its own CPU per history query moved between 180
and 216 ms from run to run, for both desks alike. Tally today is the
median of 3 rounds of 40 (Python 2.60-3.09, Go 2.72-2.81 ms). History
is the median of 9 rounds of 10, taken in turn, Python then Go (each
round's p50: Python 162-197, Go 161-222 ms): Go +7%, inside the 10%.

(4) Go zips the same files at the same level with Go's own gzip: 38
bytes more (0.2%), as Appendix A expected ("18,553, measured", against
18,520 for files that have changed since).

The rows that did not run here are the security suite's (login
flood, legit user while guessing, stalled connections): gate-a.md
G-4, same numbers in both.

## The CPU fix

Before the fix, Go's Front Desk used more CPU than Python's while
pages were open. In C4's session the numbers were 0.84% of a core
against 0.51% (20 pages, idle), 0.98% against 0.71% (traffic), and
with one page 0.87% against 0.30%. This session reproduces it, with
the binary from before the fix in the same session:

| Front Desk CPU, % of a core | Python    | Go before | Go after  |
|-----------------------------|-----------|-----------|-----------|
| 20 pages, idle              | 0.49 0.58 | 0.82      | 0.31 0.33 |
| 20 pages, traffic           | 0.68 0.70 | 0.89      | 0.37 0.41 |
| 1 page, idle                | 0.33 0.31 | 0.76      | 0.33 0.29 |
| 1 page, traffic             | 0.36 0.37 | 0.86      | 0.33 0.34 |

Two rounds each where two numbers are shown (the old binary was run
once). Each comes from /proc over 45 s (idle) and 90 s (about 4,000
chat calls) windows, by `TestStreams` with E2E_VIEWERS 20 and 1.

What was slow: the 2-second check, not the streams. A benchmark of
one check against `fakelitellm` with 200 cards and a 200-seat tally
(`BenchmarkCheck` in `internal/frontdesk/desk`) showed where the time
went:

- **JSON decoding, about 80% of a check that reads the cards.**
  `pyjson.Decode` read each `/key/list` page (100 cards, about half a
  megabyte) with `json.Decoder.Token`. That runs a whole `Decode` for
  every number and string, and makes and drops an error text after
  each one (`stateEndTop`, then `scanner.error`, then `quoteChar`).
  The repeated-key check went through every earlier key for each of
  a card's 60 or so fields.
- **Rebuilding, serialising and hashing the whole answer on every
  check**, also when nothing it is made from had changed. The card
  sort lower-cased both names on every comparison, and the card ids
  (HMAC) were made again for every card on every check.
- **Garbage**: about 3 MB allocated per check, 10 MB with the cards.
  This came from growing slices and builders, a 512-byte start for
  each half-megabyte body, and the tally rows' objects. The garbage
  collector's share followed.

What changed (no byte the desk sends; every golden and tests/parity
pass):

- `pyjson.Decode` reads well-formed JSON in one pass. Plain strings
  are taken as they are; any string with an escape or non-ASCII
  bytes is still unquoted by encoding/json. Big objects find repeated
  keys with a map. Anything it is not sure of (an error, nesting past
  512) goes to the old token reader, so values and error texts stay
  what they were. `TestDecodeSame` and `FuzzDecode` compare the two
  readers.
- The desk skips building the answer again when nothing that build
  reads has changed: the same card list, tally, models, notes,
  settings, game seats, secret and proxy state, and no card's expiry
  time passed since. It then only notes the time of the check, as an
  unchanged answer did before. `TestSameAnswer` covers it (a card
  that expires with nothing else changing, and a clock set back).
- Card ids are remembered by token. The sort keys are lower-cased
  once. The tally rows are made at their size. The answer is written
  into a buffer the size of the last one. The proxy's answers are
  read into a buffer of their Content-Length (`litellm.Client.Raw`).

`BenchmarkCheck`, process CPU per check (cpu-ms/op), three runs each:

| Check                          | Before         | After        |
|--------------------------------|----------------|--------------|
| As Run makes them (cards every | 12.0-13.7 ms   | 3.2-3.5 ms   |
| 5th, tally every 2nd or 3rd)   |                |              |
| One that reads the cards       | 41.7-43.9 ms   | 9.3-10.1 ms  |
| Allocated per check, mixed     | 3.0 MB         | 0.75 MB      |

On the live stack the check (`TestTallyBench`) is now 4.1-4.9 ms of
wall time at p50, against 7.2-7.6 ms for Python's.

## How to run it again

```sh
# The stack up; both desks in local mode in the proxy folder, with
# its COMPOSE_PROJECT_NAME and COMPOSE_FILE exported; then:
export E2E_PROXY_URL=http://localhost:4231 E2E_SEATS_ENV=seats.env
export E2E_KEYS_FILE=perf-keys.json E2E_DESK_DIR=$PWD
E2E_SEED=1 go test -tags e2e -run TestSeed ./tests/e2e/perf
# Then each desk in turn, Python and Go:
E2E_PORT=8131 E2E_PID=<python pid> E2E_OUT=py.txt \
  E2E_DESK_CMD="python3 -B gui/frontdesk.py" \
  go test -tags e2e -timeout 30m ./tests/e2e/perf
E2E_PORT=8031 E2E_PID=<lvp pid> E2E_OUT=go.txt \
  E2E_DESK_CMD="lvp desk" go test -tags e2e -timeout 30m ./tests/e2e/perf
# One open page: E2E_VIEWERS=1 with -run TestStreams.
# The tally rows, in the proxy folder (the year of history first):
python3 -B tests/reference/perf/tally_bench.py $PWD $PWD/gui
E2E_REPO=$PWD go test -tags e2e -count=1 -v -run TestTallyBench \
  ./internal/frontdesk/desk
# The check on its own, no stack needed:
go test -run X -bench Check ./internal/frontdesk/desk
```

Delete `perf-keys.json` (the 200 seat keys) when done.

# Performance: Python and Go Front Desk, A/B

Measured on 2026-11-01 in the sandbox (Linux amd64, 4 CPUs, 15 GB),
one session, on stack `lvp-c4` (LiteLLM v1.106.6 and Postgres 16 in
Docker, practice models from the image's fake-ollama). Both desks ran
on the host at once, in local mode, in the same proxy folder: Python
`gui/frontdesk.py` (py-reference, Python 3.13) on port 8123, Go `lvp
desk` (this tree, go1.26.0) on 8023. 209 cards (200 seeded seats made
straight in LiteLLM, as seed.py made them with mint-key.sh, and the
rest of the session's), 216 seats in the tally. Not a year of history:
the spend logs held this session's chat calls only (a few thousand).

The tests are `tests/e2e/perf` (ports of load.py, mint_ab.py,
startup.py and sse_bench2.py). Each side ran twice; the table shows
the second, clean round (nothing else running; round 1 overlapped a
build of 12 binaries for part of Python's run). Round 1 agreed within
noise. The stream rows come from a third run of TestStreams alone,
with 90 s of quiet before each side, after a viewer was fixed to
reopen its fetch connection as a browser does.

| Measure                                | Python   | Go       | Target met |
|----------------------------------------|----------|----------|------------|
| Start to first page, p50               | 119 ms   | 18 ms    | yes        |
| Start to first state, p50              | 276 ms   | 183 ms   | yes        |
| Memory 5 s after start                 | 29.7 MB  | 20.1 MB  | yes        |
| Memory, 20 open pages, idle            | 40.4 MB  | 27.0 MB  | yes        |
| Memory, 20 open pages, traffic         | 42.1 MB  | 27.6 MB  | yes        |
| Page load, 6 GETs, 1 viewer, p50       | 1.75 ms  | 1.35 ms  | yes        |
| Page load, 20 viewers, p50             | 35.70 ms | 5.98 ms  | yes        |
| `/api/state` 200 gzip, 1 viewer, p50   | 0.19 ms  | 0.19 ms  | yes        |
| `/api/state` 200 gzip, 20 viewers, p50 | 3.12 ms  | 0.64 ms  | yes        |
| `/api/state` 304, 20 viewers, p50      | 3.05 ms  | 0.30 ms  | yes        |
| Mint, p50, no load                     | 59.8 ms  | 47.9 ms  | yes        |
| Mint, p50, 20 viewers                  | 82.0 ms  | 51.8 ms  | yes        |
| Cancel, p50, no load                   | 46.7 ms  | 31.6 ms  | yes        |
| Cancel, p50, 20 viewers                | 60.5 ms  | 36.5 ms  | yes        |
| Mint, p50 (mint_ab, 20 cycles)         | 68.6 ms  | 43.0 ms  | yes        |
| Page wrong right after mint or cancel  | 0 of 40  | 0 of 40  | yes        |
| Live stream per page, traffic          | 8.31 MB/h| 8.65 MB/h| yes (1)    |
| Live stream per page, idle             | 0.01 MB/h| 0.01 MB/h| yes        |
| Front Desk CPU, 20 pages, idle         | 0.51 %   | 0.84 %   | NO         |
| Front Desk CPU, 20 pages, traffic      | 0.71 %   | 0.98 %   | NO         |
| `/api/state` zipped, 209 cards         | 10,435 B | 10,232 B | yes        |
| `/api/state` 304s a second, 20 viewers | 5,413    | 40,330   | yes        |

(1) Within the 10% the spec allows: Go's viewers saw 19 changes in
the 90 s window, Python's 18 (each change is one state fetch, about
11 KB); per change the bytes are the same or fewer (the state zips
smaller in Go).

## The one target missed: CPU

Go's Front Desk uses more CPU than Python's while pages are open:
0.84% of a core against 0.51% idle, 0.98% against 0.71% with chat
traffic (20 pages; measured from /proc over 45 and 90 s windows,
twice, the same both times). It is not the streams: with one open
page Go used 0.87% and Python 0.30%; with twenty, 0.95% and 0.52%. So
the cost is in the checks that run every 2 s while someone watches
(the key list of 209 cards, the tally, building and zipping the state,
its ETag), about 17 ms of CPU a check in Go against about 6 ms in
Python. Appendix A's target ("Front Desk CPU, 20 pages, traffic: <=
Python") is not met. This is C2's code (internal/frontdesk/desk);
the measurement and the test are here for whoever fixes it. Gate A's
G-7 stays open until then.

## Not measured here

- Tally today and history (same SQL in both: C1's and C2's goldens
  hold the text), psql start, the watching cost on Postgres and
  LiteLLM: the real stack here is shared with other runs, so these
  were not timed.
- 50 page loads at once, keep-alive requests a second as Appendix A
  words them: the 20-viewer rows above are the same kind of load.
- The login flood and stalled-connection rows are the security suite
  (`tests/e2e/security`, gate-a.md G-4): same numbers in both (40
  checked, 160 refused; 32 held per device, 256 in all).

## How to run it again

```sh
# both desks up in local mode, in the proxy folder, then:
export E2E_PROXY_URL=http://localhost:4223 E2E_SEATS_ENV=seats.env
export E2E_KEYS_FILE=perf-keys.json E2E_DESK_DIR=$PWD
E2E_SEED=1 E2E_PORT=8023 go test -tags e2e -run TestSeed ./tests/e2e/perf
E2E_PORT=8123 E2E_PID=<python pid> E2E_OUT=py.txt \
  E2E_DESK_CMD="python3 -B gui/frontdesk.py" \
  go test -tags e2e -timeout 30m ./tests/e2e/perf
E2E_PORT=8023 E2E_PID=<lvp pid> E2E_OUT=go.txt \
  E2E_DESK_CMD="lvp desk" go test -tags e2e -timeout 30m ./tests/e2e/perf
```

Delete `perf-keys.json` (the 200 seat keys) when done.

# Gate A: is Go ready? (spec 17)

Written by chunk C4 on 2026-11-01, from runs in the sandbox it was
built in (Linux amd64, Docker, no Windows, no macOS, no GitHub
Actions, no PyPI or vuln.go.dev). Each check says one of:

- **passed here**: it ran here, as the spec words it, and passed;
- **passed here, in part**: the parts named ran and passed; the rest
  did not run (said which, and where it will);
- **failed**: it ran and did not pass;
- **CI only, not run here**: it needs a runner, a system or a network
  this sandbox does not have.

Nothing below is "passed" that did not run. Logs named here stayed in
the sandbox's scratch folder; the numbers are copied in.

The stack: `lvp-c4`, LiteLLM v1.106.6 on port 4223, Postgres 16, the
practice overlay `game/compose.game.yaml` with the image's
fake-ollama; Front Desk in Docker on 8023, then on the host: Go `lvp
desk` on 8023 and Python `gui/frontdesk.py` on 8123. Torn down after
(`down -v`), its seats.env, keys and password files deleted.

## Summary

| Gate | What | Result |
|------|------|--------|
| G-1  | CI green | passed in CI (ci and parity, ce72611) |
| G-2  | Every golden suite | passed here |
| G-3  | Twin harness, 3 runs, 4 modes | passed here, in part (no --docker) |
| G-4  | Security suite as Python's | passed here, in part (12 not run) |
| G-5  | Secret sweep, zero hits | passed here |
| G-6  | Page contract; browser | passed here, in part (11 not on image) |
| G-7  | Appendix A targets | failed (CPU); 9 rows not measured |
| G-8  | Game: week, cross-play, quests, rescore | passed here |
| G-9  | Image | passed here, in part (arm64 not run) |
| G-10 | Release dry run | passed here, in part (Linux only) |
| G-11 | mint-parity.sh, shim and lvp mint-key | passed here |
| G-12 | Hand runs (MANUAL.md) | not done |
| G-13 | Ledger complete | passed here, in part (no --docker twin) |

Gate A is **not** met: G-4 (scripts not ported), G-6 (scripts not run
against the image), G-7 (Front Desk CPU, and rows not measured) and
G-12 (hand runs) are open, and parts of G-3, G-9, G-10 and G-13 run
only in CI or where PyPI is reachable.

## After verification (fix round F1)

Verifiers found these; each is fixed in the tree, with a test, or
relabelled here:

- Front Desk wrote its answer under a write clock started when the
  request came, so a mint or revoke slower than 10 s closed the
  connection with nothing sent, and the minted key was never shown.
  The clock now starts when the answer is ready (web App.send);
  TestSlowMintAnswers and TestMintLateAfterWriteWait (ledger H-4).
- A viewer who hung up mid-mint stopped mint-key, and the log blamed
  the proxy. Mint and revoke now run to their end, as Python's did;
  TestHangUpFinishesMint (frontdesk.md, kept equal).
- Hosts with spaces around them, or bytes outside ASCII: ledgered
  (H-8, H-1), in the golden and the twin harness.
- The game: an empty [quests.sizes], weights not adding up to a
  finite number above 0, chars as [high, low] and counts past the
  32-bit range are refused at load with the line (game.md O-1
  addendum, base.md O-1); fake-ollama cuts a leading // as Python
  3.12 does and skips a UTF-8 byte order mark (game.md P-1, P-3).
- G-4, G-6, G-7 and G-13 said "passed" or "every row" where parts
  did not run: relabelled below.

Then the lead (fix round F2):

- C1's TestStubs, which hung on the real fake-ollama, is now
  TestCommandHelp (desk, game and fake-ollama --help).
- Host-mode mint gives up at mint-key's 25 s, where Python waited to
  the 30 s limit: ledgered as frontdesk.md F-25.
- Edits made outside a chunk's own files are accepted by the lead:
  internal/rules/check.go and base.md O-1 (C1's, by F1), README.md
  and gui/README.md (C5's, by C4, for the Dockerfile move), and the
  stale lines in game/README.md and gui/requirements-docker.txt.

## G-1 CI green

**passed in CI** on ce72611: ci (14 jobs: six systems with Go 1.26
and 1.27, checks with -race, pg) and parity (twin harness, image).
It took four fix rounds after the C2-C4 commit, all in tests but
one (the desk's tally flag, frontdesk.md F-13). What ran here too:

- `gofmt -l .`: empty. `go vet ./...` for linux, windows, darwin: ok;
  also with `-tags e2e` and `-tags parity` for tests/.
- `go mod tidy -diff`: no change.
- `internal/archtest` (imports, widths, invisible characters, model
  ids, secrets): pass.
- `go test -race -count=1 ./...`: every package passes (after the
  lead's F2 fix to C1's stale TestStubs, now TestCommandHelp).
- `go test -c` for windows/amd64, windows/arm64, darwin/arm64,
  linux/arm64, every package, and tests/ with `-tags e2e` and `-tags
  parity`: 180 test builds, none failed. Windows and macOS runs: CI
  only.
- arm64 FMA scan (go tool objdump of the arm64 test binaries of
  tests/parity, tests/e2e/* and the callers program): no FMADD,
  FMSUB, FNMADD or FNMSUB in their functions.
- Six release builds of lvp (`CGO_ENABLED=0 -trimpath -s -w`):
  linux/amd64 13.44 MB, linux/arm64 12.71, darwin/amd64 13.48,
  darwin/arm64 12.85, windows/amd64 13.62, windows/arm64 12.70.
- govulncheck: vuln.go.dev is blocked here: CI only.

## G-2 Every golden suite

**passed here.** The goldens are read by each package's tests (C1:
pyfmt, pyrand, rules, psql; C2: frontdesk_core, _http, _gate, _games;
C3: office_play, office_score, fakeollama). All pass in the
whole-tree run (G-1); none is skipped on Linux. Windows and macOS runs
of the same tests: CI only.

## G-3 Twin harness

**passed here, in part.** `go test -tags parity ./tests/parity` with
PARITY_RUNS=3, twice by C4, and once more in fix round F1 on the
final code, with two Host cases added (H-8, H-1):

| Mode   | Cases | Same | Known (listed) | Unlisted |
|--------|------:|-----:|---------------:|---------:|
| local  | 231   | 212  | 19             | 0        |
| login  | 242   | 223  | 19             | 0        |
| lan    | 243   | 224  | 19             | 0        |

The same in each of the three runs. fake-ollama: 10 cases, 10 the same
(shapes, types, prompt token counts; reply lengths are random by
design, P-4), three runs.

The 19 known differences, each in expected.json with its ledger IDs:
`reset_cmd` (F-6); a Host with userinfo (H-3); a Host with a space
and a tab after it (H-8: Go 200, Python 421); a Host with a byte
outside ASCII (H-1: net/http's 400); no Host on HTTP/1.0
(H-5) and on HTTP/1.1 (H-1); Content-Length -1 and abc (H-1); NaN and
UTF-16 bodies (H-6); the six 304 answers' headers (H-7); the SSE
answer's Server header (F-24); the two log lines of a failed mint
(F-3, F-11).

Not run here: **`--docker` mode** (Python's side needs psycopg, and
PyPI is blocked here). parity.yml runs all four modes, three times,
with Postgres and psycopg by hash. Until it is green there, G-3 is not
met in full.

## G-4 Security suite

**passed here, in part.** What ran passed;
twelve scripts spec 16.5 names were not ported or run (listed at
the end of this section). `tests/e2e/security` (the reference scripts ported to
Go) ran against Python and against Go, both with `--lan` on the real
stack (loopback aliases as devices), and against Go in Docker:

- Lock (lock_vb): every line of both outputs equals the recorded
  `lock4163.out` (timings and Retry-After counts aside): the five
  messages, 429 with Retry-After, the right password refused while
  locked, a fresh device let in, 300 tries from a locked device all
  429 while a fresh one logs in, 200 wrong from 40 devices: 40
  checked and 160 refused with Retry-After 1, the legit user in at
  once under a sustained attack.
- Caps (cap_vb), against `cap4163.out`: one device holds 32 of 400
  stalled connections; 20 other devices load the page at once, 20/20;
  the same device is refused; two addresses hold 64; eight hold 256
  and a ninth is refused; all slots come back. One line differs, as
  ledgered: after 13 s of dripping one header byte every 4 s, Python
  still held the 32 connections, Go had closed them (H-4: 10 s for
  the whole request).
- Host, cookie, CSRF and Origin, bodies (sec_vb): Python and Go gave
  the same lines but three, all ledgered (no Host header: H-1;
  Content-Length -1 and abc: H-1).
- Odd requests write no log line (fuzz_vb's cases; a lone-surrogate
  password is a wrong password and is logged, in both); 30 streams
  reset hard, then 30 more all served (misc_vb).
- In Docker (every request one device): Host, cookie, CSRF, bodies,
  streams and the lock lines as above.

Not ported or run as scripts: typo_vb, rst_vb, rst2_vb, nest_vb,
surr_vb, logflood, refill_vb, game_vb, inputs, rawhost, sec_fix and
lock2. Spec 16.5's method (run against Python, then against Go)
needs no recorded output, so "nothing recorded" does not excuse
them: G-4 is open until they are ported and run side by side. Their
requests are partly covered elsewhere: the twin harness sends the
body, nesting, surrogate, Host, mint and login cases; C2's goldens
(frontdesk_http.py: inputs, refill_vb, game_vb's checks) and the
Gate goldens (window_vb, window2_vb, clock_vb, clock2_vb, whose
traces they replay). Not covered by any test here: logflood's log
rate bound, and rst/rst2's freed slots after hard resets beyond
misc_vb's 30 streams.

## G-5 Secret sweep

**passed here.** Zero hits. Swept: every answer the security suite
got (headers and bodies) in all three runs, each desk's whole log
(Python and Go on the host, Go in Docker), every test log and output
of the session (30 files), for the master key, the salt key, the
database password, FRONTDESK_PASSWORD and the 200 seeded seat keys.
Also: FRONTDESK_PASSWORD was empty in the proxy and db containers
(G-9), and the twin harness's mint answers are masked and compared.

## G-6 Page contract and browser scripts

**passed here, in part.** Spec 16.5 asks for every browser script
against Go in host mode and in --docker. In --docker only six ran:
the scripts that do not log in cannot run against a desk that has a
password, and --docker always has one. Not run against the image:
race, race2, race3, keepit, keepit2, closerace, restart_fix,
down_mint, fallback, evil2_vb and lan_restart. To close it: log in
once through /api/login and hand the cookie to Playwright
(storageState or context.addCookies), then run them on the image.

- Page contract (`tests/parity/page_test.go`, every CI run): 57 fields
  app.js reads, with their JSON types, checked against Go's answers in
  local, --login, --lan and --docker: pass. The scan of app.js finds
  no read of a field the table leaves out.
- Browser (`tests/e2e/browser`, Playwright, the reference scripts as
  written), against Go and against Python:
  - Go in Docker: xss_vb, xss3_vb, xss4_vb, tally_xss, text_vb,
    evil_vb: pass; xss3_vb's lines equal the recorded `xss4163.out`
    (the card count aside; the tally's two hostile names compared as
    a set, as which spent more is chance).
  - Go and Python on the host, --lan with a password: the same six
    and lan_restart: pass.
  - Go and Python on the host, local: evil2_vb, race2, race3,
    fallback, keepit, keepit2, race, closerace, restart_fix,
    down_mint: pass (race3 on real game cards, after `lvp game
    setup`).
- Not run: the perf round's other page scripts (ux_fix, lan_fix,
  states_fix, focus2, push_cost, push_ablate, final_shots, browser,
  cross, lockout, noname, refill, term_mint, xss.js, down2): spec
  16.5 does not name them. The other site evil_vb and evil2_vb open
  was not kept with the scripts; the test serves one that does what
  they look for (fetch, frame, form post).

## G-7 Appendix A targets

**failed.** tests/e2e/perf-results.md has the table. Of the rows
measured, Go meets or beats Python on all but one: **Front Desk CPU
with 20 open pages**, 0.84% of a core against 0.51% idle, 0.98%
against 0.71% with traffic, the same in every run; with one open
page, 0.87% against 0.30%. The cost is in the 2-second checks while
someone watches, not in the streams. This is C2's code; it needs a
fix before Gate A.

Not measured (so G-7 is not met even with the CPU fixed): tally
today, history, psql start, the watching cost on Postgres and on
LiteLLM, the whole check p50, keep-alive requests a second, 50 page
loads at once, and the static files zipped in total.

## G-8 Game

**passed here.** `tests/e2e/game` on the real stack, 1-minute days:

- A 5-day week by Go only: every day `spend logs: N of N rows`
  (141, 202, 195, 158, 187), each day started 3.8 to 3.9 s after the
  minute (LiteLLM's refill), five 422 `budget_exceeded` refusals for
  the spending personas, a sixth `run` says the week is over.
- `tests/golden/rescore.py`: Python's score_day over the same spend
  rows gave Go's scores and usage rows exactly, all 5 days (35, 52,
  47, 43, 42 scores; 10 usage rows a day).
- Boards: 16 of 16 board commands (board, each --day, each --seat)
  byte-identical between `lvp game board` and `office.py board` on
  the Go-played file.
- Cross-play: Python setup, Go plays 2 days: 13 of 13 boards
  identical; Go setup, Python plays 2 days: 13 of 13 identical.
- Quests: for seed 42, day 1's 100 quests are the same rows.
- Bot traces: C3's goldens (13 single-bot traces), in G-2.

Not run here: the re-mint path with LiteLLM's reset job slowed down
(it needs the proxy restarted with other settings); C3's unit tests
cover it on a fake clock.

## G-9 Image

**passed here, in part.** Built from the root Dockerfile here, with
the pinned Go builder swapped for the local golang:1.26.10-alpine plus
a local module proxy through a buildx named context (containers here
cannot download modules; nothing of that is in the tree):

- amd64: built, run. arm64: built (a cross build, no emulation needed:
  the last stage runs nothing), its /lvp is an ARM aarch64 static
  ELF; **not run** (no arm64 machine or emulator here): CI only.
- User 10001:10001; `ENTRYPOINT ["/lvp"]`, `CMD ["desk", "--docker"]`,
  STOPSIGNAL SIGINT; files: /lvp, the CA bundle,
  THIRD_PARTY_NOTICES.txt, /app/seats.env (the one-line stand-in),
  /app/game, /tmp (1777). No shell, no Python.
- Size: 13.8 MB of files (amd64), 13.1 MB (arm64), 5.9 MB
  compressed; under 20 MB (ledger C-7 on how it is measured).
- No secrets in the context: with stand-in seats.env, .env, *.key and
  office.db files in the tree, the build context listing (parity.yml's
  check, run here by hand) held none; the image holds none.
- `docker compose --profile frontdesk up -d` (the stack's own compose
  file): Front Desk up in Docker, read-only root, cap_drop ALL,
  no-new-privileges, tmpfs /tmp; the Docker banner; login,
  mint, cancel, tally on the page.
- `docker compose run --rm --no-deps frontdesk mint-key laptop` minted
  a key that answered a chat call; `... --revoke-seat laptop` revoked
  it; usage and value errors as before. `docker compose exec
  frontdesk mint-key` no longer works (no shell, no PATH entry):
  `exec frontdesk /lvp mint-key` does; gui/README.md updated (C-3).
- Practice mode on the image's fake-ollama (`command:
  ["fake-ollama"]`, read only, no powers): the game's week (G-8) ran
  on it.
- FRONTDESK_PASSWORD empty in the proxy and db containers (`compose
  exec ... printenv` printed an empty line; docker inspect shows it
  empty), set in frontdesk (C-5).
- On Linux the container cannot read game keys: as uid 10001 with the
  same read-only mount, reading `game/keys/<seat>.key` gave
  "Permission denied" (the folder is 0700, the files 0600).
- `docker compose stop frontdesk`: exit 0 in 0.4 s; SIGTERM: exit 0
  in 0.2 s (C-2).

parity.yml's `image` job does the build for both systems and these
checks on every push: CI only.

## G-10 Release dry run

**passed here, in part.** No fork tag or Actions here. What ran:
release.yml's own `pack` and `try` scripts, read out of the workflow
file, in a scratch git repository (a copy of this tree, tagged there,
never in the project's repository), with programs built here for the
six targets (macOS: the arm64 program in place of the universal one;
no lipo here):

- five archives and SHA256SUMS; packing twice gave the same sums;
- each archive: one folder, lvp, mint-key, THIRD_PARTY_NOTICES.txt,
  the source tree, no tests/ or .github/;
- `try` on the Linux amd64 archive: sum checked, unpacked, `lvp
  version` is `lvp <tag>`, `lvp --help`, both `--version`s: pass.

The dry run found two bugs in release.yml, fixed: the zip path was
taken after a cd, and `./mint-key*` also matched mint-key.sh. Not run
here: the macOS job (lipo), the other five systems' `try`, the image
platform check, `gh release create`: CI only.

## G-11 mint-parity.sh

**passed here.** On the live stack, `tests/mint-parity.sh` with the
shim (`mint-key`) and with a two-line wrapper for `lvp mint-key`: 43
cases, 41 the same, 2 known differences (a trailing slash and CRLF in
PROXY_URL, as before), 0 failed, both times. Its callers: Python's
Front Desk and office game with both mint-keys: the same; the new Go
twin (`go run ./tests/parity/callers`): lvp desk and gui/frontdesk.py
gave the same page words for a mint, a taken seat, a cancel, a
cancel of a gone card, and broken settings (wrong master key, proxy
down, a proxy that hangs up, change-me values); lvp game and office.py
gave the same setup, second setup, reset and second reset (office id
and command names aside, O-8).

## G-12 Hand runs

**not done.** tests/e2e/MANUAL.md lists them (Windows 11, Apple
silicon Mac, Linux, an arm64 board): none of those machines is here.

## G-13 Ledger

**passed here, in part** (the --docker live twin did not run here).
Every difference the twin harness, the security, browser, game and
live suites and mint-parity.sh found
is in `tests/parity/ledger/*.md` with its test (the IDs above), or is
an open problem said here (G-7's CPU, which is not allowed as a
difference). C4's own: container.md (C-1 to C-7) and release.md (R-1
to R-5). LiteLLM's real answers for every route of spec 7.9 were
recorded (`tests/e2e/live/testdata/golden/captures.json`), and the
fake matches them in status, error type and field names (17 steps).
The live twin: both Front Desks on one real proxy gave equal states
at a quiet point (209 cards, ids aside); a mint on either showed on
itself at once and on the other within 10.1 s. Those were both host
desks. **The live twin in --docker (both images side by side, spec
16.5) did not run**: Python's side needs psycopg from PyPI, which
this sandbox cannot reach. It can run in parity.yml or by hand where
PyPI is reachable.

F1 added two ledger entries from verification, H-8 (a Host with
spaces is read trimmed) and the non-ASCII Host in H-1, and widened
O-1 and P-3; see "After verification" above.

## For the sign-off

The user reads the ledger (`tests/parity/ledger/`) and this file.
Before that sign-off is useful: get CI and
parity.yml green, fix or accept the CPU difference, measure the
missing Appendix A rows, port and run the missing security scripts,
run the browser scripts and the live twin against the images, and do
the hand runs.

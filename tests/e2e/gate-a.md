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
| G-1  | CI green | passed in CI (ci and parity, 8df83c1) |
| G-2  | Every golden suite | passed here |
| G-3  | Twin harness, 3 runs, 4 modes | passed here, in part (no --docker) |
| G-4  | Security suite as Python's | passed here (12 more, side by side) |
| G-5  | Secret sweep, zero hits | passed here |
| G-6  | Page contract; browser | passed here (stand-in image) |
| G-7  | Appendix A targets | passed here (CPU fixed, P1) |
| G-8  | Game: week, cross-play, quests, rescore | passed here |
| G-9  | Image | passed here, in part (arm64 not run) |
| G-10 | Release dry run | passed here, in part (Linux only) |
| G-11 | mint-parity.sh, shim and lvp mint-key | passed here |
| G-12 | Hand runs (MANUAL.md) | not done |
| G-13 | Ledger complete | passed here, in part (no --docker twin) |

Gate A is **not** met: G-12 (the hand runs) is open, and parts of
G-3, G-9, G-10 and G-13 run only in CI or where PyPI is reachable
(Python's --docker side needs psycopg). G-4 and G-6 ran on a
stand-in image (the real build stage downloads modules, which this
sandbox cannot). One overlap to know of: for about five minutes
(14:04 to 14:09 UTC on 1 November) G-4's flood and cap tests ran
during G-7's measuring window; G-7's A/B ran both desks under the
same conditions, and its numbers were repeated in a second round.

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

**passed in CI** on 8df83c1: ci (14 jobs: six systems with Go 1.26
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

**passed here.** Every script spec 16.5 names is ported to Go
(`tests/e2e/security`) and ran against Python and against Go. The
first five (eight tests) ran on `lvp-c4` (below); the twelve left
open there were ported
and run in a later round on stack `lvp-s1` (LiteLLM v1.106.6 on
4232, Postgres 16, a fake Ollama; Go `lvp desk` on 8032, Python
`gui/frontdesk.py` on 8132, each desk with its own psql, `--lan` with
loopback aliases as devices), side by side: the same test binary
against Python, then against Go, each line printed (E2E_OUT) and the
two outputs compared with diff (spec 16.5's method). The whole suite
then ran again in one go on each desk (19 passed on both; lock2 and
the leaky mint skip there and ran on their own desks). Every
difference is a ledger entry, or a clock or a speed (below).

The first five, on `lvp-c4`, against Python and Go with `--lan`,
and against Go in Docker:

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

The twelve of the later round (`scripts_test.go`; lock2 and
logflood in `security_test.go`), each passing against both:

- typo_vb (TestTypo): a device that mistyped once gets in while two
  attackers guess from 10 addresses. Python: 50 attacker tries
  checked, the typo device in after 41 s, in each run; Go: the same
  in one run of three, in after 13 s and 15 s in the others. Which
  device wins the one try a second is chance; the rule is the same
  code (`gate.Login` is Python's login line by line).
- surr_vb (TestSurrogate): a lone-surrogate password is a wrong
  password: 4, 3, 2, 1 tries left, then the lock, the same words;
  8 connections for 5 s from the locked device: 0 log lines.
- nest_vb (TestNested): 4000-deep JSON gets 400; 5 s from 8
  connections: 0 log lines.
- rst_vb, rst2_vb (TestResets, TestResets2): 20 resets, then 4
  threads for 5 s, then 22 kinds of reset request (bad lines, 414,
  431, 501, 421, 405, half bodies, pipelined, idle keep-alive): 0 log
  lines each; the address still holds exactly 32 after.
- logflood (TestLogFlood): a locked device for 5 s from 8
  connections: 0 log lines.
- lock2 (TestLock2, on fresh desks): every line equal but the
  Retry-After count and the flood's time: the lock of 5, the old
  lockout of 3 devices, then 200 wrong from 40 devices: 40 checked,
  160 refused with Retry-After 1, the right password at once.
- refill_vb (TestRefill): 23 bad refills refused (field refill), no
  card made; 1d, 1w, 1mo, empty and none minted and echoed; the four
  durations; the cards' states equal (times aside). refill_vb waited
  1.5 s before its last look; the port waits 3.5 s, since both desks
  rebuild the state every 2 s and whether the 1 s card showed as
  expired was chance.
- game_vb (TestGameNames): game-, GAME- and Game- names refused with
  the same words, gamex and my-game minted.
- inputs (TestInputs, local and `--lan`): the 24 field cases and the
  revoke cases give the same codes, fields and words. A budget as a
  number (2.5), " 5 " and 30m are taken by both since; the port
  cancels them at once, as inputs.py did.
- rawhost (TestRawHost, local and `--lan`): 13 Host shapes; equal but
  for the ledgered ones below.
- sec_fix (TestSecFix): the Host table, the tries left, the session
  bound to its device, HEAD and PUT, the psql child without
  FRONTDESK_PASSWORD, 400 stalled connections (368 closed by the cap,
  a page refused, served again 11 s later): equal but for thread
  counts and the ledgered bad-request line. Its last part, which ran
  Python's plain_error on mint-key.sh's stderr, is
  TestMintErrorScrubbed, from outside: a leaky stand-in for the proxy
  answers /key/generate with a key's end and a 64-hex hash; both
  desks show "The proxy did not accept the master key...", and both
  logs have "Received API Key = [hidden]", no end, no hash.

Differences seen, each ledgered (tests/parity/ledger/frontdesk.md):
no Host on HTTP/1.1, two Hosts, a bad request line,
Content-Length abc and -5 (H-1; Python read the first of two Hosts,
so "ip, evil" was served there); an absolute URI with another host
(H-2: Go 421, Python 404); a Host with userinfo (H-3, corrected:
with `--lan` Python served it, 200; Go 400); an HTTP/1.0 status line
(H-5); a Host with a space inside (H-9, new: Go 400, Python 421);
"mint-key said" where Python said "mint-key.sh said" (F-3). Not
behaviour: request counts and times (Go answered 1.5 to 190 times
as many flood requests), thread counts, and once (1 run of 3) Go's cap
refusal read as a reset where Python's read as a closed connection
(both refused; it depends on whether the request bytes were read
before the close).

Against Go in Docker (the image of G-6, every request one device,
its log read through `docker compose logs`): nest_vb, rst_vb,
rst2_vb, refill_vb, game_vb, inputs and rawhost pass, with the same
lines as Go on the host but Docker's own Host message and the
machine's name ("VM") refused, as FRONTDESK_HOSTS was empty; the five
that need devices (typo_vb, surr_vb, sec_fix, lock2, logflood) skip
there, as TestCaps does. Python's --docker could not run here
(psycopg).

## G-5 Secret sweep

**passed here.** Zero hits. Swept: every answer the security suite
got (headers and bodies) in all three runs, each desk's whole log
(Python and Go on the host, Go in Docker), every test log and output
of the session (30 files), for the master key, the salt key, the
database password, FRONTDESK_PASSWORD and the 200 seeded seat keys.
Also: FRONTDESK_PASSWORD was empty in the proxy and db containers
(G-9), and the twin harness's mint answers are masked and compared.

## G-6 Page contract and browser scripts

**passed here.** Spec 16.5 asks for every browser script against Go
in host mode and in --docker: all seventeen passed against the image
(round G4/G6, stack `lvp-s1`), the eleven that do not log in among
them. --docker always has a password, so for those eleven the test
preloads `tests/e2e/browser/login.js` into node (E2E_MODE=docker):
it logs in once through /api/login and hands the cookie to each new
browser context with context.addCookies; the scripts run as written.
restart_fix and down_mint stop and start the desk, which then has no
sessions (lan_restart shows the login screen it asks for): after the
start command, login.js logs in again before the page's next request
leaves, as the person would, and sends the new cookie.

The image was a **stand-in**: the root Dockerfile's stage 2 verbatim,
its stage 1 replaced by a host-built `CGO_ENABLED=0 go build -trimpath
-ldflags "-s -w" -o lvp ./cmd/lvp` (of HEAD) and its notices (`go run
./internal/notices -bin lvp`, 2,477 lines), as containers here cannot
download Go modules; the real stage 1 (the pinned golang:1.27.3
digest, `go mod download`) was not built. Run by compose
(`--profile frontdesk`, read-only root, user 10001), on 8032, the
game folder mounted with `lvp game setup`'s seats; stop and start
were `docker compose stop` and `start frontdesk`.

- On the image: evil2_vb (the other site's fetch and mint blocked, its
  form refused, no card made), race2, race3 (on the game's own
  cards), fallback, keepit, keepit2, race, closerace, restart_fix
  ("Reconnecting", then "Desk open", a mint with no reload), down_mint
  ("Front Desk is not answering", then the same form mints), and
  lan_restart (the login screen after the restart): pass, with the
  six of the first round (xss_vb, xss3_vb, xss4_vb, tally_xss,
  text_vb, evil_vb).
- The same test, the same way (a password, login.js), against Python
  (`gui/frontdesk.py --login`, the host; Python's --docker needs
  psycopg): all seventeen pass, and the lines equal the image's but
  for what the stack held (which cards existed, the key ends, the
  tally's counts, the scroll positions that follow) and one number:
  race2's note height 600 ms after a cancel (224 or 355 px), which
  is the page's own transition caught mid-way; three runs of race2
  alone on the image gave 355, 355, 224.

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

**passed here** (fix round P1, stack `lvp-p1`, one A/B session with
200 cards and a year of history, 146,001 daily rows).
tests/e2e/perf-results.md has the table and the method. Every
Appendix A row was measured, Python and Go in turn, and Go is at most
Python plus 10% on each:

- **Front Desk CPU, fixed.** With 20 open pages and chat traffic Go
  now uses 0.37-0.41% of a core against Python's 0.68-0.70%. Idle it
  uses 0.31-0.33% against 0.49-0.58%. With one page the two are
  equal, 0.29-0.34% against 0.31-0.37%. The binary from before the
  fix, run in the same session, used 0.82-0.89%, as C4 found. The
  cost had been the 2-second check: Go decoded the key list one JSON
  token at a time, and it rebuilt, serialised and hashed the whole
  answer every time. One check took about 12 ms of CPU, 42 ms when it
  read the cards; now about 3.3 ms and 9.5 ms
  (`BenchmarkCheck`). No byte the desk sends changed: every golden
  and tests/parity pass; the new JSON reader is fuzzed against the
  old one.
- **The rows not measured before**, Python then Go: tally today p50
  2.75 and 2.80 ms; history p50 177 and 190 ms (+7%, same SQL;
  Postgres's own time per query moved 180-216 ms in both); psql start
  155 and 148 ms; whole check p50 7.41 and 4.40 ms; watching cost on
  Postgres 0.96% and 0.93%, on LiteLLM 1.15% and 1.12%; keep-alive
  requests a second 4,823 and 24,777 (a static file), 4,212 and
  31,401 (a 304); 50 page loads at once, worst of 5 rounds, 162 and
  23 ms, no stall; static files zipped in total 18,848 and 18,886
  bytes (+0.2%).

Two kinds of rows are counted elsewhere. Start-up and memory 5 s
after start come from C4's A/B the same day; the fix does not touch
start-up. The login flood and stalled-connection rows are G-4's
security suite.

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
live suites and mint-parity.sh found is in `tests/parity/ledger/*.md`
with its test (the IDs above), or is an open problem said here. C4's
own: container.md (C-1 to C-7) and release.md (R-1 to R-5). LiteLLM's
real answers for every route of spec 7.9 were recorded
(`tests/e2e/live/testdata/golden/captures.json`), and the fake matches
them in status, error type and field names (17 steps). The live twin:
both Front Desks on one real proxy gave equal states at a quiet point
(209 cards, ids aside); a mint on either showed on itself at once and
on the other within 10.1 s. Those were both host desks. **The live
twin in --docker (both images side by side, spec 16.5) did not run**:
Python's side needs psycopg from PyPI, which this sandbox cannot
reach. It can run in parity.yml or by hand where PyPI is reachable.

F1 added two ledger entries from verification, H-8 (a Host with
spaces is read trimmed) and the non-ASCII Host in H-1, and widened
O-1 and P-3; see "After verification" above.

## For the sign-off

The user reads the ledger (`tests/parity/ledger/`) and this file.
Done since: CI and parity.yml green, the CPU difference fixed, every
Appendix A row measured, the missing security scripts ported and
run side by side, and the browser scripts run on the (stand-in)
image. Left: the hand runs (MANUAL.md), and the --docker live twin
where PyPI is reachable.

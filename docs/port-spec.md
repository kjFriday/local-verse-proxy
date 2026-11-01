# Go port: the spec

> The design document the Go program `lvp` was built from. Code
> comments and test notes cite its sections ("spec 16.5"). It also
> names working folders from the design phase (`scratchpad/...`,
> `port-design/...`) that are not part of this repo; the material
> kept from them is in `tests/reference/`.

Port Front Desk (`gui/frontdesk.py`, with the `--docker` mode from
`go-port`), the office game (`game/office.py`) and the practice model
server (`game/fake_ollama.py`) to Go, as one program, `lvp`, with one
download per system and a Go-only Docker image. Every behaviour,
safety property and performance number stays, or a ledger entry says
why it changed. The Python files stay as the oracle until the user
signs off, then go.

This is the one document the implementation agents follow. It merges
the three plans (`plan-maintainer.md` as the base, `plan-fidelity.md`
and `plan-beginner.md` grafted in), applies every judge ruling, and
closes the ten gaps the judge named (section 22 maps each one).

## Contents

1. How to use this spec
2. Goals, constraints, scope
3. Decisions
4. What a user gets
5. Layout: commands, packages, import rules
6. Dependencies and Go version
7. Shared packages: the Go interfaces (frozen at C1's M1)
8. Python to Go: the feature map
9. Front Desk in detail
10. Host mode and container mode
11. The office game in detail
12. fake-ollama in detail
13. Windows, macOS and Linux details
14. The image and compose
15. CI and release
16. Tests and the parity proof
17. Gates, and the checks before any Python file is deleted
18. Docs changes
19. The starting ledger
20. Work breakdown: five chunks
21. Risks
22. The judge's gaps, and where each is closed
23. Questions for the user
- Appendix A: performance baseline and targets
- Appendix B: facts verified for this spec

---

## 1. How to use this spec

- **Base.** Start from `main` after `go-port` merges. Before anything
  else, C1 re-reads the merged `gui/frontdesk.py`, `compose.yaml`,
  `gui/Dockerfile`, `gui/README.md` and `README.md`, and if the
  `--docker` contract moved since this spec was written (go-port files
  as of 18:47 today), updates section 10 in a short note at the top
  of `tests/parity/ledger/base.md` before C2 starts.
- **Oracle.** The Python files on `main` right after the merge are the
  behaviour reference. The commit is tagged `py-reference` (the user
  approves the tag). Goldens are captured from that commit only. From
  then on the Python files are frozen: a bug found during the port is
  fixed in both versions (with the user's OK) or written in the
  ledger.
- **Three classes** of every observable thing (from fidelity):
  1. *Identical*: status codes, JSON field names and values, every
     message a person reads (page, terminal, board), cookie flags,
     safety headers, the order of checks, cadences and limits, SQL
     text, the dice for a seed, the board's text.
  2. *Ledgered*: a difference written in `tests/parity/ledger/*.md`
     with what, why, which test shows it, and why it is not weaker
     (section 19).
  3. *Forbidden*: anything weaker on a safety property, slower on a
     measured number, or a new message or behaviour nobody listed.
- Python crash paths (a traceback, a frozen page) are not behaviours
  to copy: each is replaced by the nearest documented behaviour and
  gets a ledger line.
- Messages that name a file that goes away (`mint-key.sh`,
  `python3 game/office.py`) change only by the rename table in 11.9.
- **Style** for every file the port writes: lines of 80 columns or
  less (tabs count 8); gofmt; plain doc comments; plain beginner
  wording in docs; no AI model identifiers in any file; no license
  file or license choice (third-party notices are compliance, not a
  choice: 15.3).

## 2. Goals, constraints, scope

Goals, in the user's words: portability, usability, saleability.

- One download per system: Windows, macOS, Linux; amd64 and arm64.
  Nothing else to install but Docker, for the proxy.
- Front Desk's container image is Go only: no Python.
- Every behaviour, safety property and performance number kept.
- Beginner docs: no Python, bash, curl, jq, openssl, sed or git on
  the beginner path, on any of the three systems.

In scope: `gui/frontdesk.py` (host mode and `--docker`),
`game/office.py`, `game/fake_ollama.py`; reusing go-port's
`cmd/mint-key`, `internal/seats`, `internal/litellm`; the image,
compose files, CI, release, docs; beginner helpers that replace the
shell in the README.

Not in scope: `gui/static/` beyond one small change (9.12); LiteLLM
or Postgres setup; a read-only Postgres role (question 23.9);
publishing images or signing binaries (questions).

## 3. Decisions

Each line: choice, then why. Rulings are the judge's; the rest come
from the plans as noted.

1. **One program, `lvp`, with subcommands** (ruling). `lvp desk`,
   `lvp game ...`, `lvp mint-key ...`, `lvp fake-ollama`, and after
   parity the helpers. One download, one unblock click on macOS and
   Windows, one file to replace; about 11 MB. The name is the user's
   call (question 23.1); it lives in one constant, `cli.Name`.
2. **`lvp mint-key` is byte-compatible with go-port's `mint-key`**,
   and a `mint-key` shim program ships for one release (ruling).
   Existing scripts and `tests/mint-parity.sh` keep working.
3. **The game mints through a child process**: it runs its own
   executable as `lvp mint-key ...` (ruling). office.py's "the game
   never sees the master key" stays true and the child cannot be
   missing. Import rules (5.3) keep the master key's code out of
   `internal/game`.
4. **Front Desk mints in process**, through `internal/mintkey.Exec`,
   and matches on mint-key's own stderr text with a literal port of
   `plain_error` (ruling). Generic failure reads `mint-key said:` in
   both modes, as Python `--docker` already does.
5. **Game state stays `game/office.db` (SQLite)**, via
   `modernc.org/sqlite` behind `internal/gamestate` (ruling). The docs
   name the file and format; Python and Go cross-play on one file,
   the strongest board proof; a mid-week upgrade continues. JSON can
   come later, as a separate decision, behind the same package.
6. **The game reads Postgres one query per `docker compose exec`**,
   exactly as `pg()` (ruling): `BEGIN TRANSACTION READ ONLY`,
   `-v ON_ERROR_STOP=1`, `cannot read the proxy database: <stderr>`.
   No persistent session, no new time limit.
7. **Front Desk host mode keeps one persistent psql** over
   `docker compose exec` (all plans): the db publishes no port.
8. **Front Desk `--docker` talks to Postgres with pgx's `pgconn`
   only** (all plans): pure Go, SCRAM done right, about +1 MB.
9. **SSE: `Transfer-Encoding: identity` plus `Connection: close`**
   (ruling). net/http then sends Python's close-delimited stream, no
   chunks, keeps `r.Context()` hang-up cancellation and per-write
   deadlines. No hijacking, no chunking.
10. **Server timeouts**: `ReadHeaderTimeout`, `ReadTimeout`,
    `IdleTimeout` 10 s, a 10 s deadline on each write (ruling).
    ReadTimeout does not end an SSE stream (verified). Ledger H-4: 10
    s for the whole request, where Python's was per socket read.
11. **Listen on `tcp4`** (ruling), as Python's AF_INET server; plain
    `tcp` on 0.0.0.0 would also open IPv6 LAN addresses.
12. **Clocks**: sessions on the wall clock, lockout windows and
    cadences on the monotonic clock, the answer's age (`fresh()`,
    SSE tick) on the wall clock, the game on the wall clock: exactly
    the clocks Python used (ruling, plus 9.2).
13. **A `FRONTDESK_PASSWORD` with U+FFFD or invalid UTF-8 is refused
    at start** (ruling). Go's JSON turns `"\ud800"` into U+FFFD;
    the guard keeps "a lone-surrogate password is simply wrong".
14. **Go and libraries**: `go 1.26.0` in go.mod; CI on the newest
    1.26 and 1.27 patches; release builds and the image on the newest
    1.27 patch (1.27.3 today); `BurntSushi/toml v1.6.0`,
    `jackc/pgx/v5 v5.11.0` (`pgconn` only), `modernc.org/sqlite
    v1.60.1`, `golang.org/x/sys v0.48.0` (ruling; x/sys is already
    in the graph through sqlite, now used directly for Windows ACLs).
15. **Container-mode contract: maintainer's table**, which matches
    go-port's code (ruling); section 10.
16. **Image `FROM scratch`**, uid 10001, CA certificates copied,
    `STOPSIGNAL SIGINT`, compose `read_only`, `cap_drop: [ALL]`,
    `no-new-privileges`; `ENTRYPOINT ["/lvp"]`, `CMD ["desk",
    "--docker"]`; fake-ollama runs the same image with `command:
    ["fake-ollama"]` (ruling).
17. **`.dockerignore` stays go-port's allow-list**, plus `!go.sum`
    (ruling). Nothing from the repo root is embedded in the binary;
    `lvp init` reads `seats.env.example` from the proxy folder.
18. **One small shared flag parser** (`internal/args`): `--name
    value` or `--name=value`, `-n value` or `-nvalue`, flags after
    positionals, `--` ends flags, exit 2 on misuse (ruling). No
    argparse prefix abbreviations (ledger F-18).
19. **Tally: skip a new query while one still runs** (ruling, D13).
20. **rules.toml: missing or wrongly typed keys give a plain error
    with the line; unknown keys only warn** (ruling).
21. **Beginner helpers** (`init`, `check`, `wait`, `test`, `mint
    --save`, `revoke`, `env`, `game practice on|off`) **come after
    parity is signed off**, in their own chunk (ruling, C5).
22. **One universal macOS archive** (lipo on the macOS runner)
    (ruling).
23. **Release: extend go-port's `release.yml`**, with sorted entries
    and fixed times for reproducible archives (ruling).
24. **`THIRD_PARTY_NOTICES.txt` in every archive and image**,
    generated from the linked modules (ruling).
25. **Python goes in three gates** (A Go ready, B docs switched, C
    removal after the user signs off, `python-final` tag first);
    whether `mint-key.sh` goes is the user's call (ruling).
26. **seats.env in host mode is read by `internal/seats`**, the
    bash-compatible parser mint-key uses (maintainer D9, fidelity
    F-15): one reader for everything (ledger F-1).
27. **Front Desk's FRONTDESK_PASSWORD never reaches the proxy or
    database containers**: compose sets it empty in their
    `environment:`, which beats `env_file` (gap 2; 14.3).
28. **The page gets the reset command from the server** in a new
    `/api/session` field, `reset_cmd`, spelled for this system and
    for how `lvp` was started (gap 4; 9.12).
29. **Windows file safety in one package, `internal/fsx`**: writes by
    temp file and rename, removes with a 2 s retry while another
    program holds the file, owner-only access lists for secrets (gaps
    3 and 7; section 13).
30. **No proxy-env for Front Desk's GETs** (Python's http.client used
    none), env proxies honoured for minting (as mint-key) (fidelity
    F-18).
31. **`OPTIONS *` reaches our handler** (`DisableGeneralOptionsHandler`)
    and gets Python's 501 JSON (maintainer's finding).
32. **Front Desk's proxy client reads up to 16 MiB** of an answer
    (`litellm` default stays 1 MiB) (maintainer's finding).

## 4. What a user gets

### 4.1 Downloads (from C4's release)

| File                                       | For                    |
|--------------------------------------------|------------------------|
| `local-verse-proxy-<tag>-windows-amd64.zip`| most Windows PCs       |
| `local-verse-proxy-<tag>-windows-arm64.zip`| Windows on Arm         |
| `local-verse-proxy-<tag>-macos.tar.gz`     | every Mac (universal)  |
| `local-verse-proxy-<tag>-linux-amd64.tar.gz`| most Linux PCs        |
| `local-verse-proxy-<tag>-linux-arm64.tar.gz`| Raspberry Pi 4/5 etc. |
| `SHA256SUMS`                               | checksums              |

Each archive has one top folder, `local-verse-proxy/`, with `lvp`
(`lvp.exe`), `mint-key` (`mint-key.exe`, this release only),
`THIRD_PARTY_NOTICES.txt`, and the source tree of the tag minus
`.github/` and `tests/` (so `compose.yaml`, `config.yaml`,
`seats.env.example`, `Dockerfile`, `go.mod`, `go.sum`, `cmd/`,
`internal/`, `gui/`, `game/`, docs). The source is there because
compose builds the Front Desk image locally (question 23.2).

### 4.2 Commands

```text
lvp desk [--lan] [--login] [--port N] [--host NAME]... [--seat-url URL]
         [--env FILE] [--docker]
lvp game [--rules FILE] setup
lvp game run [-d N | --days N] [-q | --quiet]
lvp game board [-d N | --day N] [-s NAME | --seat NAME]
lvp game reset
lvp mint-key <seat> [--models a,b] [--budget USD] [--duration 30d]
                    [--budget-duration 1d]
lvp mint-key --revoke <key> | --revoke-seat <seat> | --version
lvp fake-ollama                 (practice model server, in Docker)
lvp version | lvp --version
lvp help [command] | lvp -h | lvp <command> --help
after Gate A (C5):
lvp init [--ollama URL] [--desk-password] [--force]
lvp check
lvp wait [--timeout 3m]
lvp test
lvp mint <seat> [mint-key's options] [--save FILE]
lvp revoke <seat> | --key-file FILE | --key KEY
lvp env [set NAME=value | unset NAME]
lvp game practice on | off
```

Rules for every command: exit 0 done, 1 failed, 2 usage or value
mistake; output ASCII; lines of 80 columns or less; errors end with
what to do. On macOS and Linux the docs say `./lvp`, in PowerShell
`.\lvp`, in cmd `lvp`.

## 5. Layout: commands, packages, import rules

### 5.1 Tree

```text
cmd/
  lvp/main.go          var version = "dev"; os.Exit(cli.Main(...))
  mint-key/main.go     shim for one release: mintkey.Main, same output
gui/
  embed.go             package gui: //go:embed static/*.html
                       static/*.js static/*.css static/*.svg
  static/              unchanged but for app.js's reset line (9.12)
internal/
  cli/                 command table, help, version, Windows
                       double-click pause
  args/                flag parser, Std, Prog (how lvp was typed)
  kit/                 finds the proxy folder (compose.yaml)
  clock/               wall and monotonic clocks, sleep; a fake
  fsx/                 safe writes, removes with retry, private files
  pyfmt/               Python-exact formatting and text rules
  pyrand/              Python's random.Random, bit for bit
  seats/               go-port; + FromEnviron, Names
  litellm/             go-port; + NewWith (Options), Raw
  litellm/fakelitellm/ a LiteLLM v1.106.6 stand-in for tests
  mintkey/             mint-key's logic, moved from cmd/mint-key
  pgread/              Session (psql pipe), Once (psql per query),
                       Direct (pgconn)
  pgread/fakedocker/   a fake `docker` program for tests (main)
  rules/               game/rules.toml: Load (strict), SeatNames
  gamestate/           game/office.db: schema, open, query, claim,
                       read-only seat list
  archtest/            repo-wide tests: imports, widths, model ids,
                       reference scan
  notices/             main: writes THIRD_PARTY_NOTICES.txt
  frontdesk/           Main and wiring
    config/            Settings, Options, Log
    gate/              password, sessions, lockout, CSRF
    desk/              the one poller, cards, tally, game seats
    web/               server, listener caps, routes, SSE, static,
                       login, mint and revoke, Host checks
  game/                the office game (Main, commands, bots, ...)
  fakeollama/          the practice model server (Main, handler)
  setup/               C5's helpers (stubs until then)
tests/
  reference/           imported scripts, recorded outputs, playtest
                       office.db files (C1, step 0)
  golden/              Python capture scripts (until Gate C)
  parity/              ledger/, twin harness, page contract
  e2e/                 real-stack: security, perf, browser, game,
                       live twin, MANUAL.md, gate-a.md
  mint-parity.sh       go-port's, kept
Dockerfile             replaces gui/Dockerfile
.dockerignore          go-port's allow-list, plus !go.sum
```

### 5.2 Entry points

Every command is `func Main(argv []string, std args.Std) int`, so the
table in `internal/cli` is plain data. `cli` routes `game practice`
to `setup.Practice` before calling `game.Main`.

### 5.3 Import rules (enforced by `internal/archtest`)

```text
cmd/lvp      -> internal/cli only
cmd/mint-key -> internal/mintkey only
internal/cli -> frontdesk, game, fakeollama, mintkey, setup, args
frontdesk/*  -> config, gate, desk, web, mintkey, seats, litellm,
                pgread, rules, gamestate, kit, args, clock, fsx,
                pyfmt, gui
game         -> rules, gamestate, pgread, kit, args, clock, fsx,
                pyfmt, pyrand
fakeollama   -> args, pyfmt
setup        -> mintkey, seats, litellm, pgread, kit, args, clock,
                fsx, pyfmt
mintkey      -> seats, litellm
pgread       -> clock, pyfmt
rules        -> pyfmt
gamestate    -> fsx
leaves       -  args, kit, clock, fsx, pyfmt, pyrand, seats,
                litellm, gui (no internal imports)
```

Forbidden on purpose: `game` importing `litellm`, `mintkey` or
`seats` (it never holds the master key; it reads only the
`PROXY_URL=` line of seats.env itself); `frontdesk` importing `game`
or `setup`; anything importing `frontdesk` or `game` but `cli`.
`fakelitellm` and `fakedocker` are imported only from `_test.go`
files (archtest checks this too).

## 6. Dependencies and Go version

`go.mod` after C1:

```text
module github.com/kjFriday/local-verse-proxy

go 1.26.0

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/jackc/pgx/v5 v5.11.0
	golang.org/x/sys v0.48.0
	modernc.org/sqlite v1.60.1
)
```

Indirect: pgpassfile, pgservicefile, x/text (pgx); libc, mathutil,
memory, go-humanize, go-isatty, go-strftime, bigfft (sqlite).
Checked on the module proxy today: pgx v5.11.0 is from 2026-09-07
(needs go 1.25), sqlite v1.60.1 from 2026-09-29 (needs go 1.26),
toml v1.6.0 from 2025-12-18, x/sys v0.48.0 from 2026-08-31; all at
least two weeks old. pgx v5.12.0 (today) and sqlite v1.61.x (10 days
and today) are too new.

Measured for this spec with go1.26.10, `CGO_ENABLED=0 -trimpath
-ldflags "-s -w"`, the exact pin set, all six targets: 9.9 to 10.6
MB, 24 to 32 s cold per target (Appendix B). The real `lvp` adds the
port's own code: expect 11 to 12 MB.

Not used, on purpose: a CLI framework, a router, a logging library,
testify, ncruces SQLite (1.1 s first query), full pgx (+3 MB),
vendoring. Dev tools, not dependencies: `govulncheck` pinned in CI.

## 7. Shared packages: the Go interfaces

C1 freezes these at milestone M1 (bodies may be stubs that return
`errors.New("not yet")`, never panics). After M1 a signature changes
only by a note in `tests/parity/ledger/base.md` and a message to the
chunks that use it. Unexported details are free.

### 7.1 internal/args

```go
// Std is where a command reads and writes.
type Std struct {
    In       io.Reader
    Out, Err io.Writer
}
func OS() Std

// Set is one command's flags.
type Set struct{ /* unexported */ }
func New(cmd, usage string) *Set // cmd like "lvp game run"
func (s *Set) Bool(long, short, help string) *bool
func (s *Set) String(long, short, meta, def, help string) *string
func (s *Set) List(long, short, meta, help string) *[]string
func (s *Set) Int(long, short, meta string, def int,
    check func(text string) (int, error), help string) *int
// Parse returns the positionals. Errors: ErrHelp, or *UsageError.
func (s *Set) Parse(argv []string) ([]string, error)
func (s *Set) Help() string

var ErrHelp = errors.New("help")
type UsageError struct{ Cmd, Usage, Msg string }
// Error is "usage: <Usage>\n<Cmd>: error: <Msg>", as argparse.
func (e *UsageError) Error() string
// Exit prints err and gives the exit code: ErrHelp 0, usage 2.
func Exit(std Std, s *Set, err error) int

// Prog is how to type lvp here, for messages: "./lvp" or ".\lvp"
// when it sits in the current folder, "lvp" when found on PATH,
// else its path (quoted when it has a space).
func Prog() string
func ProgFor(goos, exe, wd string, onPath bool) string
```

An `Int` check error text follows the argument name, as argparse:
`argument -d/--days: '0': use 1 or more`.

### 7.2 internal/kit

```go
const Compose = "compose.yaml"
// Dir is the proxy folder: the current folder if it has
// compose.yaml, else the folder lvp is in, if that has it.
func Dir() (string, error)
func DirFrom(wd, exeDir string) (string, error)
type NotFoundError struct{ Looked []string }
// Error: "cannot find compose.yaml: run lvp in the proxy's folder,
// the one you unpacked or cloned (looked in ...)".
func (e *NotFoundError) Error() string
```

### 7.3 internal/clock

```go
// Clock is the two clocks the Python code used, in seconds.
type Clock interface {
    Wall() float64 // time.time(): jumps with the system clock
    Mono() float64 // time.monotonic(): never goes back
    Sleep(ctx context.Context, seconds float64) error
    After(seconds float64) <-chan struct{}
}
func Real() Clock
type Fake struct{ /* unexported */ }
func NewFake(wall, mono float64) *Fake
func (f *Fake) Advance(seconds float64) // both clocks
func (f *Fake) SetWall(wall float64)    // wall only: a clock set
```

Python's start values against an uptime-sized monotonic clock (`0.0`
and `-EVERY`) become `math.Inf(-1)` in Go, so "long ago" holds from
the first second.

### 7.4 internal/fsx

```go
const RetryFor = 2 * time.Second
// WriteFile writes through a temporary file in the same folder and
// a rename, retried for RetryFor while the target is in use.
// private: owner only (Private) before the rename.
func WriteFile(path string, data []byte, private bool) error
// Remove removes path; a missing file is fine; retried for
// RetryFor while another program has it open (Windows).
func Remove(path string) error
func RemoveGlob(pattern string) error
// Private: mode 0600 (0700 for a folder) on Linux and macOS; on
// Windows a protected access list: the current user and SYSTEM,
// full control, nobody else.
func Private(path string) error
func IsPrivate(path string) (ok bool, why string, err error)
// InUse: Windows' sharing violation, or access denied while open.
func InUse(err error) bool
```

### 7.5 internal/pyfmt

```go
// Format is Python's format(v, spec) for the specs the two programs
// use: fill, align, sign, width, ",", precision, d, f, %, and
// "02.0f" (which can print 60). v: int, int64, float64, string.
func Format(v any, spec string) string
func Round(x float64, ndigits int) float64 // round(x, n)
func RoundInt(x float64) int64             // round(x): half to even
func Repr(f float64) string                // repr(float)
// Str is str() of a decoded JSON or TOML value: int64, float64,
// json.Number (int forms keep Python's int str, "-0" is "0"),
// bool (True, False), nil (None), string, []any, map[string]any.
func Str(v any) string
func ReprString(s string) string // repr(str): quotes and escapes
const Space = "\t\n\v\f\r\x1c\x1d\x1e\x1f \u0085 " +
    "  -     　"
func Strip(s string) string        // str.strip()
func SplitLines(s string) []string // str.splitlines()
func Fields(s string) []string     // str.split()
func Lower(s string) string        // str.lower(): İ, final sigma
func Cut(s string, n int) string   // s[:n], code points
func FloorDiv(a, b float64) float64
func Mod(a, b float64) float64
// ParseISO is datetime.fromisoformat (3.11 rules); naive: no zone.
func ParseISO(s string) (t time.Time, naive bool, err error)
func ISOFormat(t time.Time, naive bool) string
// URLSplitHost is urlsplit("//" + host): hostname (lowercased, no
// brackets, userinfo cut), port, and Python's ValueError cases.
func URLSplitHost(host string) (name string, port int,
    hasPort bool, err error)
func IsIP(s string) bool        // ipaddress.ip_address accepts it
func IsLoopback(s string) bool  // ip_address(s).is_loopback
// StrError is the C library's words: "No such file or directory",
// "Address already in use", "Permission denied", ... On Windows,
// Windows' own words.
func StrError(err error) string
// OSErrorText is str(OSError): "[Errno 2] No such file or
// directory: 'game/rules.toml'".
func OSErrorText(err error, path string) string
```

`Space` is written out here once; the regexps that need Python's
`\s` or `\S` build a class from it (Go's `\s` is ASCII only and has
no `\v`).

### 7.6 internal/pyrand

```go
// Rand is random.Random(seed) for a str seed (version 2 seeding:
// the bytes and their SHA-512, as one big integer).
type Rand struct{ /* MT19937 */ }
func New(seed string) *Rand
func (r *Rand) Random() float64
func (r *Rand) GetRandBits(k int) uint64 // k <= 64
func (r *Rand) RandBelow(n int) int
func (r *Rand) RandInt(a, b int) int     // randint, inclusive
func (r *Rand) Uniform(a, b float64) float64
func (r *Rand) Choice(n int) int         // index into a sequence
func (r *Rand) Choices(weights []float64) int // k=1, an index
```

### 7.7 internal/seats (additions)

```go
// FromEnviron is settings from the environment only (--docker).
// Path is "". Get and Lookup read environ.
func FromEnviron(environ []string) *File
// Names are the names the file sets, in file order.
func (f *File) Names() []string
```

### 7.8 internal/litellm (additions)

```go
type Options struct {
    BaseURL, MasterKey, UserAgent string
    Timeout   time.Duration // whole request; 0: DefaultTimeout
    MaxAnswer int64         // bytes of an answer read; 0: 1 MiB
    EnvProxy  bool          // HTTPS_PROXY etc.; New sets true
}
func NewWith(o Options) (*Client, error)
// Raw GETs path and gives the status and body as they came, with
// or without the master key. err is a transport error only, with
// the master key cut out.
func (c *Client) Raw(ctx context.Context, path string,
    auth bool) (status int, body []byte, err error)
```

`New` keeps its signature and behaviour.

### 7.9 internal/litellm/fakelitellm

```go
type Options struct {
    MasterKey string
    Models    []string // /v1/models ids
    Version   string   // litellm_version in readiness details
}
type Server struct{ URL string /* ... */ }
func Start(o Options) *Server
func (s *Server) Close()
type Key struct {
    Token, Key, Alias, Name string
    Models                  []string
    MaxBudget               *float64
    Spend                   float64
    BudgetDuration          string
    BudgetResetAt, Expires  string
    Created, LastActive     string
    Blocked                 bool
}
func (s *Server) AddKey(k Key) string // returns the token
func (s *Server) Keys() []Key
func (s *Server) Down(on bool)        // refuse every connection
func (s *Server) Fail(path string, status int, body string)
func (s *Server) Delay(path string, d time.Duration)
type Request struct {
    Method, Path string
    Header       http.Header
    Body         []byte
}
func (s *Server) Requests() []Request
```

Routes and bodies copy LiteLLM v1.106.6: first from go-port's
`internal/litellm` test bodies and the probe notes in
`tests/reference/litellm/` (16.0), then checked against real answers
that C4's live run records (16.5): `GET /health/liveliness`,
`/health/readiness/details`, `/v1/models`, `/key/list` (paging,
`size`, `page`, `total_pages`, `key_alias`, `return_full_object`),
`/user/daily/activity`; `POST /key/generate` (400 "already exists"),
`/key/delete` (404 "No keys found"), `/v1/chat/completions` (seat
key; model allowed; spend added per call; an over-budget key gets
v1.106.6's HTTP 422 with `error.type` `budget_exceeded`, on every
route, as captured). 401 for a wrong master key on admin routes.

### 7.10 internal/mintkey

```go
type System struct {
    Env     seats.LookupFunc
    Getwd   func() (string, error)
    Exe     func() (string, error)
    Timeout time.Duration
}
func OS() System
var Version = "dev" // set by the main package
// Main is mint-key exactly: what `lvp mint-key` and the shim run.
func Main(args []string, sys System, stdout, stderr io.Writer) int
// Result is what the mint-key program would print, and its code.
type Result struct {
    Stdout, Stderr string
    Code           int
}
// Exec runs the same rules on a seats.env already read, in this
// process. Front Desk and lvp mint use it.
func Exec(ctx context.Context, file *seats.File, args []string,
    timeout time.Duration) Result
```

go-port's `cmd/mint-key/main_test.go` moves to
`internal/mintkey/mintkey_test.go` with only the package name and
entry point changed; `cmd/mint-key` keeps a test that the shim's
output equals `lvp mint-key`'s for the usage, version and a mint.

### 7.11 internal/pgread

```go
// Error is a reason in plain words, safe to show after a scrub.
type Error struct{ Msg string }
func (e *Error) Error() string

// Querier is Front Desk's tally source: one read-only statement
// whose single value is a JSON object; returns that JSON text.
type Querier interface {
    Query(ctx context.Context, sql string) (json.RawMessage, error)
    Stop()
}

type SessionConfig struct {
    Argv    []string      // default DefaultArgv
    Dir     string        // the proxy folder
    Env     []string      // nil: os.Environ()
    App     string        // application_name
    Timeout time.Duration // per query, 8 s
    Backoff time.Duration // after a failure, 30 s
    Clock   clock.Clock
}
var DefaultArgv = []string{"docker", "compose", "exec", "-T", "db",
    "psql", "-X", "-q", "-A", "-t", "-U", "litellm", "-d",
    "litellm", "-v", "VERBOSITY=terse"}
// Session is one long-lived psql (Front Desk host mode).
type Session struct{ /* unexported */ }
func NewSession(c SessionConfig) *Session
func (s *Session) Query(ctx context.Context,
    sql string) (json.RawMessage, error)
func (s *Session) Stop()

// Direct asks Postgres at url (DATABASE_URL), read only (--docker).
type Direct struct{ /* unexported */ }
func NewDirect(url string, c clock.Clock) *Direct
func (d *Direct) Query(ctx context.Context,
    sql string) (json.RawMessage, error)
func (d *Direct) Stop()

// Once runs one psql for one SELECT, as office.py's pg(): stdin
// "BEGIN TRANSACTION READ ONLY;\nSELECT coalesce(json_agg(q),
// '[]') FROM (<sql>) q;\nCOMMIT;\n". Error text: "cannot read the
// proxy database: <stderr, stripped>". Rows keep json.Number.
func Once(ctx context.Context, dir string,
    sql string) ([]map[string]any, error)
var OnceArgv = []string{"docker", "compose", "exec", "-T", "db",
    "psql", "-U", "litellm", "-d", "litellm", "-X", "-q", "-A",
    "-t", "-v", "ON_ERROR_STOP=1"}
```

Behaviour in 9.5.

### 7.12 internal/rules

```go
type Rules struct {
    Office   Office
    Day      Day
    Mana     Mana
    Quests   Quests
    Score    Score
    Board    Board
    Sim      Sim
    Personas map[string]Persona
    Roster   Roster
}
type Office struct {
    SeatPrefix string   `toml:"seat_prefix"`
    Models     []string `toml:"models"`
    KeyLife    string   `toml:"key_life"`
}
type Day struct {
    Length  string `toml:"length"`
    Close   string `toml:"close"`
    PerWeek Num    `toml:"per_week"`
    Secs    int    `toml:"-"` // from Length
    CloseS  int    `toml:"-"` // from Close
}
type Mana struct {
    USD   Num `toml:"usd"`
    Daily Num `toml:"daily"`
}
type Quests struct {
    PerSeat     int             `toml:"per_seat"`
    RedoShare   float64         `toml:"redo_share"`
    MaxAttempts int             `toml:"max_attempts"`
    Sizes       map[string]Size `toml:"sizes"`
}
type Size struct {
    Weight  float64 `toml:"weight"`
    Value   float64 `toml:"value"`
    Calls   int     `toml:"calls"`
    ParMana float64 `toml:"par_mana"`
}
type Score struct {
    EffMin     float64 `toml:"eff_min"`
    EffMax     float64 `toml:"eff_max"`
    StreakStep float64 `toml:"streak_step"`
    StreakCap  float64 `toml:"streak_cap"`
    FlushWait  string  `toml:"flush_wait"`
}
type Board struct{ Individuals bool `toml:"individuals"` }
type Sim struct {
    Seed     Num     `toml:"seed"`
    ProBoost float64 `toml:"pro_boost"`
}
type Persona struct {
    Pro     float64    `toml:"pro"`
    Chars   [2]int     `toml:"chars"`
    Reply   int        `toml:"reply"`
    PerStep int        `toml:"per_step"`
    Retry   float64    `toml:"retry"`
    Skill   float64    `toml:"skill"`
    Pause   [2]float64 `toml:"pause"`
    Quests  int        `toml:"quests"`
    Bluff   float64    `toml:"bluff"`
    Stream  bool       `toml:"stream"`
}
type Roster struct{ Seats []Seat `toml:"seats"` }
type Seat struct{ Name, Persona, Guild string }

// Num is a TOML number (or string) kept as written, so it prints as
// Python's str() would: 1500 as "1500", 1500.0 as "1500.0".
type Num struct {
    Text  string  // Python str()
    F     float64 // the value; 0 for a string
    IsInt bool
}

// Load reads and checks a rules file. warnings: unknown keys, one
// line each. err text: load_rules' die messages, word for word, or
// for a missing or wrongly typed key "rules.toml line N: <key> ...".
func Load(path string) (r *Rules, warnings []string, err error)
// SeatNames is Front Desk's tolerant read: str(seat_prefix) and
// prefix + str(name) for each roster seat; any error gives "", nil.
func SeatNames(path string) (prefix string, roster []string)
```

### 7.13 internal/gamestate

```go
// Schema is office.py's SCHEMA, byte for byte.
const Schema = "..."
type DB struct{ /* one connection, a mutex */ }
func Create(path string) (*DB, error) // new file, Schema run
func Open(path string) (*DB, error)   // read-write, busy 5 s
// Q runs one statement under the lock, like Office.q: rows of
// int64, float64, string or nil, as SQLite stores them.
func (db *DB) Q(sql string, args ...any) ([]Row, error)
// Claim is Office.claim: under the lock, the open quests of day by
// id, pick(n) chooses one, it becomes "working" for seat.
func (db *DB) Claim(seat string, day int,
    pick func(n int) int) (Row, bool, error)
func (db *DB) Close() error
type Row struct{ /* columns, values */ }
func (r Row) Get(col string) any
func (r Row) Int(col string) int64
func (r Row) Float(col string) float64 // an int64 converts
func (r Row) Str(col string) string
func (r Row) Null(col string) bool
func (r Row) At(i int) any
// ReadSeats is Front Desk's read: SELECT seat FROM players, read
// only (mode=ro), busy 200 ms; the file closed before it returns.
func ReadSeats(path string) ([]string, error)
// Files is office.db* in dir, as Python's glob.
func Files(dir string) ([]string, error)
```

Bound values use the same types Python bound (int64, float64, string,
nil); REAL columns always get float64. Default journal mode (DELETE),
so a reader needs no write access.

### 7.14 internal/gui and the stubs

```go
package gui
//go:embed static/*.html static/*.js static/*.css static/*.svg
var Static embed.FS

package cli
const Name = "lvp"
func Main(argv []string, version string) int

package frontdesk  // owner C2
func Main(argv []string, std args.Std) int
package game       // owner C3
func Main(argv []string, std args.Std) int
package fakeollama // owner C3
func Main(argv []string, std args.Std) int
package setup      // owner C5
const Ready = false // the help lists the helpers once true
func Init(argv []string, std args.Std) int
func Check(argv []string, std args.Std) int
func Wait(argv []string, std args.Std) int
func Test(argv []string, std args.Std) int
func Mint(argv []string, std args.Std) int
func Revoke(argv []string, std args.Std) int
func Env(argv []string, std args.Std) int
func Practice(argv []string, std args.Std) int
```

Each stub file's first line is `// Owner: Cn`; only that owner edits
it after M1.

## 8. Python to Go: the feature map

Every name in the three Python files, and where it goes. Package
paths are under `internal/`.

### 8.1 gui/frontdesk.py

Constants and patterns:

- `FAST`, `KEYS_EVERY`, `TALLY_EVERY`, `HISTORY_EVERY`,
  `FALLBACK_EVERY`, `SLOW_EVERY`, `IDLE_AFTER`, `PING_EVERY` ->
  `frontdesk/desk` timing.go, same names (Go case) and values.
- `MAX_STREAMS`, `MAX_CONNECTIONS`, `MAX_PER_DEVICE`, `READ_TIMEOUT`
  -> `frontdesk/web` limits.go.
- `SESSION_HOURS`, `LOCK_AFTER`, `FAIL_GAP` -> `frontdesk/gate`.
- `SEAT_RE`, `BUDGET_RE`, `DURATION_RE`, `REFILLS`, `KEY_RE`,
  `HOST_RE` -> `web`, anchored `^(?:...)$` (fullmatch; HOST_RE has
  an alternation).
- `SECRET_RE` -> `web`: `sk-[^<Space>]*|[0-9A-Fa-f]{32,}` with
  `pyfmt.Space`.
- `SEAT_PORT` (4000), `LOOPBACK` -> `web` hosts.go.
- `MINT_ENV` -> `frontdesk/config`: the names `--docker` minting
  reads (9.10).
- `TALLY_SQL`, `TODAY_SQL`, `HISTORY_SQL`, `COUNTS` -> `desk`
  tally.go, byte for byte (golden-checked).
- `SECURITY_HEADERS` -> `web` send.go, exact strings.
- `AGAIN` -> `frontdesk` docker.go.

Functions and classes:

- `log` -> `config.Log`: `HH:MM:SS msg` in local time to stderr, one
  write per line, under a mutex.
- `read_env` -> retired: `seats.Load` (ledger F-1).
- `parse_time`, `utc_text`, `_utc_text` -> `pyfmt.ParseISO`,
  `ISOFormat`; a 4096-entry map cache in `desk`.
- `lan_ip` -> `web` hosts.go: UDP "connect" to 192.0.2.1:9, sends
  nothing, empty on error.
- `make_password` -> `gate.MakePassword`: 16 from
  `abcdefghjkmnpqrstuvwxyz23456789` with crypto/rand, dashed in 4s.
- `merge_tally` -> `desk` tally.go.
- `Settings` -> `config.Settings` (9.1).
- `ProxyError`, `MASTER_REFUSED`, `Proxy` -> `desk` proxy.go over
  `litellm.NewWith` (no env proxy, 16 MiB, per-call timeouts).
- `TallyError`, `Psql` -> `pgread.Session`; `Pg` -> `pgread.Direct`.
- `seat_state`, `dollars` -> `desk` cards.go.
- `GameSeats` -> `desk` gameseats.go over `rules.SeatNames` and
  `gamestate.ReadSeats`.
- `Desk` (`run`, `fresh`, `refresh_now`, `refresh`, `tally_query`,
  `set_keys`, `load_game`, `show_change`, `load_slow`, `load_keys`,
  `http_tally`, `build`, `publish`) -> `desk` desk.go, cards.go,
  tally.go (9.2 to 9.4).
- `Refused`, `WrongPassword`, `Locked`, `digest`, `Gate` -> `gate`.
- `App` (`host_ok`, `run_mint`, `plain_error`, the static table,
  `names`, `cookie_name`, `local_hosts`, `tool`) -> `web` hosts.go,
  mint.go, static.go.
- `Server` (`take`, `give_back`, `process_request`, `handle_error`,
  backlog 128) -> `web` listener.go (caps on a tcp4 listener; Go's
  backlog is the system's, at least 128), server.go (ErrorLog
  discarded).
- `Handler` (`setup` TCP_NODELAY, `send`, `fail`, `send_error`,
  `cookie`, `addr`, `host_ok`, `wrong_host`, `do_GET`, `do_HEAD`,
  `do_POST`, `route_get`, `events`, `gone`, `read_json`,
  `route_post`, `login`, `host_name`, `over_network`, `seat_url`,
  `mint`, `revoke`) -> `web` routes.go, send.go, events.go, body.go,
  auth.go, hosts.go, mint.go. TCP_NODELAY is Go's default.
- `docker_settings`, `docker_banner`, `main`, `serve` ->
  `frontdesk` frontdesk.go, docker.go.

### 8.2 game/office.py

- `GAME`, `ROOT`, `DB`, `KEYS`, `MINT` -> `game` paths.go:
  `kit.Dir()`, `<kit>/game`, `office.db`, `keys/`, and the minter's
  argv `[os.Executable(), "mint-key"]`.
- `UNITS`, `WORDS` -> `game` util.go.
- `SCHEMA` -> `gamestate.Schema`.
- `Result` -> `game` score.go struct.
- `die` -> an error type `dieError{msg}`; `game.Main` prints it to
  stderr and exits 1.
- `seconds`, `clock`, `money` -> util.go (pyfmt, `pyfmt.ReprString`
  in the message).
- `load_rules` -> `rules.Load`.
- `proxy_url` -> proxyurl.go: `LVG_PROXY_URL`, else the
  `PROXY_URL=` line of `<kit>/seats.env` only (the last such line,
  value after the first `=`, stripped), else
  `http://localhost:4000`; trailing `/` cut.
- `lit`, `pg`, `seat_state`, `spend_where` -> pg.go over
  `pgread.Once`, SQL verbatim.
- `mint`, `revoke` -> minter.go (11.5).
- `chat` -> chat.go (11.4).
- `Office` (`q`, `meta`, `players`, `claim`) -> office.go over
  `gamestate.DB`.
- `post_quests` -> quests.go; `Bot` -> bot.go.
- `wait_for_day`, `play_day` -> day.go; `wait_for_logs`,
  `score_day`, `streak` -> score.go.
- `day_summary` -> summary.go; `rank_corr`, `scored_days`,
  `heading`, `board`, `seat_board` -> board.go.
- `cmd_setup`, `cmd_run`, `cmd_board`, `cmd_reset`, `positive`,
  `STOPPED`, `main` -> commands.go, game.go.

### 8.3 game/fake_ollama.py

`PORT`, `MODELS`, `WORDS`, `tokens`, `prompt_text`, `filler`, `now`,
`Handler` (`send`, `do_GET`, `do_POST`, `chat`, `log_message`) and
`__main__` -> `fakeollama` server.go and fakeollama.go (section 12).

### 8.4 mint-key.sh and cmd/mint-key

`mint-key.sh` stays as it is until Gate C (its fate is question
23.5). go-port's `cmd/mint-key/main.go` logic (`handle`, `load`,
`apiFail`, `proxyURL`, `fullVersion`, the regexps and usage text)
moves unchanged to `internal/mintkey`; `cmd/mint-key/main.go` becomes
the shim.

## 9. Front Desk in detail

The Python is the reference; this section fixes every point where Go
needs a decision.

### 9.1 Settings (`config`)

- Host mode: `--env`, else `SEATS_ENV`, else `<kit>/seats.env`. No
  proxy folder found: exit 1 with `kit.NotFoundError`'s text (ledger
  F-17). File missing or unreadable: `missing <path>: do README Step
  3 first`, exit 1. A line the seats parser refuses: its
  `seats.SyntaxError` text, exit 1. Empty master key:
  `LITELLM_MASTER_KEY: not set in <absolute path>`, exit 1.
- `--docker`: `FromEnviron(os.Environ())`, read once. No master key:
  `LITELLM_MASTER_KEY is not set. Put it in seats.env, next to
  compose.yaml (README Step 3a), then run: docker compose --profile
  frontdesk up -d`, exit 1.
- Fields as Python: master, db_url, https, host and port from
  `pyfmt.URLSplitHost` of `PROXY_URL` (default
  `http://localhost:4000`; port 443 or 80 when absent), the client's
  base URL from `litellm.ParseBase` (a bad PROXY_URL: exit 1 with
  `PROXY_URL in <path> is not a web address ...`, ledger F-2),
  defaults (`DEFAULT_MODELS` split on `,` and stripped, default
  `local`; `DEFAULT_BUDGET` `5`; `DEFAULT_DURATION` `30d`, read with
  `File.Get`), hidden (every value of 6 or more characters whose
  name is `DATABASE_URL` or contains `KEY`, `PASSWORD`, `SECRET` or
  `TOKEN`: the file's names in host mode, every environment name in
  `--docker`), warnings (`change-me` in master or salt key:
  `seats.env still has change-me values, so mint-key will refuse to
  run. Do README Step 3a.`).
- `ReloadIfChanged`: host mode only; on a modification-time change,
  reload and log `reread seats.env`; any error keeps the old
  settings, silently.
- `Scrub`: replace each hidden value by `[hidden]`, in list order.
- Settings are built before `FRONTDESK_PASSWORD` is removed from the
  environment, so in `--docker` its value is in the hidden list.

### 9.2 The desk (`desk`)

One goroutine, a mutex, and two broadcast channels standing in for
Python's `Condition` and `Event`:

- Loop: wait for a kick (1-slot channel) or 1 s; check if kicked, or
  if (viewers > 0 or mono - seen < IDLE_AFTER) and mono - last >=
  FAST. A panic or error in a check logs `check failed: ` + scrubbed
  text cut to 200 code points, and the loop goes on.
- Counters `starts`, `checks`, `mine` exactly as Python. `Fresh()`:
  mark `seen` (mono); return at once if a body exists and
  wall - checked < FAST + 1; else kick and wait up to 4 s for
  `checks` to change. `RefreshNow()`: `target = starts + 1`, force
  keys, kick, wait up to 5 s for `checks >= target`.
- `refresh()` order: reload settings; start the tally if
  mono - tally_at >= TALLY_EVERY and no tally is running (else no
  tally this check: ledger F-13); liveliness (no master key, 2 s);
  if up: the slow reads (SLOW_EVERY or no models) and the keys
  (forced or KEYS_EVERY; then the game seats); then wait up to 10 s
  for the tally; then publish. Errors exactly as Python: liveliness
  transport error adds `Details: nothing answers at <host>:<port>
  (<PythonClass>).`; a non-200 liveliness means down with no note;
  slow-read and key errors add the ProxyError text or `Could not read
  the key cards: <PythonClass>`.
- Python class names (`desk` errname.go, for http.client): timeout
  `TimeoutError`; refused `ConnectionRefusedError`; DNS `gaierror`;
  closed before a status line `RemoteDisconnected`; reset
  `ConnectionResetError`; bad status line `BadStatusLine`; bad JSON
  or wrong shape `JSONDecodeError`; TLS `SSLCertVerificationError`
  or `SSLError`; otherwise `OSError` (ledger F-14).
- Proxy JSON: `desk` proxy.go `get(path)`: 401 gives MASTER_REFUSED;
  other non-200 gives `The proxy answered <code> to <path before
  ?>.` plus ` Its database may be down: run docker compose ps.` from
  500 up. Decoded with Go's decoder (NaN refused: ledger F-10).
- Tally job: Python's `tally_query` (today; the days before when
  missing, when the day changed or HISTORY_EVERY passed; today again
  if the history's day differs). The history cache is kept even when
  the check stopped waiting. A failed or late job: `why` =
  scrub(error text) cut to 160 code points (a late job's text is "",
  as Python's empty TimeoutError); then if up and FALLBACK_EVERY
  passed: `http_tally(why)`; else the last tally stays.
- `set_keys` overrides, `show_change` (one `/key/list?
  return_full_object=true&key_alias=<quote(seat)>` lookup; on
  failure `RefreshNow`), `load_keys` (pages of 100, at most 50,
  repeats dropped by token), `load_slow`, `http_tally`: line by line.
  `quote` is Python's `urllib.parse.quote` (safe `/`).
- `build`: card fields and order exactly as Python (section 9.3);
  card id = first 16 hex of HMAC-SHA256(per-run 16-byte secret,
  token); the id-to-token map stays private.
- `publish`: compact JSON; ETag `"` + 16 hex of SHA-256 + `"`
  (Python's BLAKE2 values differ; opaque; ledger F-4); only when the
  ETag changed: store, gzip level 6 (no name, mtime 0), version + 1,
  close the `changed` channel; `checked` = wall every time. A marshal
  error (never expected) logs `check failed: ...` and keeps the old.
- Clocks: wall for `checked` and the SSE age; mono for every cadence,
  `seen`, `last`; all "last time" fields start at -Inf.

### 9.3 State JSON (`desk` types.go)

```go
type State struct {
    Desk     Status   `json:"desk"`
    Models   []string `json:"models"`   // never null
    Defaults Defaults `json:"defaults"`
    Cards    []Card   `json:"cards"`    // never null
    Tally    *Tally   `json:"tally"`
}
type Status struct {
    Up      bool     `json:"up"`
    LiteLLM any      `json:"litellm"` // as the proxy said, or null
    DB      any      `json:"db"`
    LAN     bool     `json:"lan"`     // --lan, or always in --docker
    Notes   []string `json:"notes"`   // scrubbed; never null
}
type Defaults struct {
    Models   []string `json:"models"`
    Budget   string   `json:"budget"`
    Duration string   `json:"duration"`
}
type Card struct {
    ID      string  `json:"id"`
    Seat    string  `json:"seat"`
    Ends    string  `json:"ends"`    // last 4 of key_name
    Models  []any   `json:"models"`  // as the proxy said, or []
    Budget  any     `json:"budget"`  // max_budget, or null
    Spent   float64 `json:"spent"`   // dollars(spend)
    Refills any     `json:"refills"` // budget_duration or null
    Resets  *string `json:"resets"`  // utc_text(budget_reset_at)
    Expires any     `json:"expires"`
    Created any     `json:"created"`
    Used    any     `json:"used"`    // last_active
    State   string  `json:"state"`
    Game    bool    `json:"game"`
}
type Tally struct {
    Day    any   `json:"day"`
    Source any   `json:"source"` // "database" or "proxy"
    Why    any   `json:"why"`
    Seats  []Row `json:"seats"`
    Models []Row `json:"models"`
}
type Row struct {
    K      string  `json:"k"`
    Name   string  `json:"name"`
    Spend  float64 `json:"spend"`
    Tokens float64 `json:"tokens"`
    Reqs   float64 `json:"reqs"`
    Fails  float64 `json:"fails"`
    TSpend  float64 `json:"t_spend"`
    TTokens float64 `json:"t_tokens"`
    TReqs   float64 `json:"t_reqs"`
    TFails  float64 `json:"t_fails"`
    Live   *bool   `json:"live,omitempty"` // seats only
}
```

- `seat_state` order: blocked; expired (`expires` parsed, naive as
  UTC, <= now); spent or waiting (`max_budget` not null and spend >=
  it; waiting if `budget_duration`); ok. A time that cannot be parsed
  counts as absent and is logged once per text (ledger F-9: Python's
  check failed and the page froze).
- `dollars`: max(0, `pyfmt.Round(x, 6)`); non-finite becomes 0.
- Cards sort stably by (seat == "", `pyfmt.Lower(seat)`).
- Tally rows: dollars on spend and t_spend; `live` for seats.

### 9.4 Game seats (`desk` gameseats.go)

Port of `GameSeats`: `rules.toml` and `office.db` in the game folder
reread only when their modification time (nanoseconds) changes;
prefix and roster from `rules.SeatNames`; players from
`gamestate.ReadSeats` (any error: try again next time; a missing
file: no players). `Owns(seat)` keeps Python's rule exactly:
`prefix != "" && strings.HasPrefix(Lower(seat), prefix)` or
`Lower(seat)` in the lowered seat set. An unexpected error logs
`could not read the game's seats: ` + text cut to 160.

### 9.5 Read-only Postgres (`pgread`, owned by C1)

**Session** (host mode), a port of `Psql`:

- Started on the first query with `Argv` in `Dir`; stdin a pipe;
  stdout and stderr one pipe; one goroutine splits lines and drops a
  trailing `\r`.
- First writes, byte for byte: `set default_transaction_read_only =
  on;\nset statement_timeout = '5s';\nset application_name =
  '<App>';\n`.
- Each query: `begin read only;\n<strings.TrimSpace(sql)>\ncommit;\n
  \echo __fd_<8 hex>\n`, flushed; collect lines until a line whose
  strip equals the mark, at most `Timeout` (`psql did not answer in
  time`); EOF: the last two lines stripped and joined by a space, or
  `psql stopped`; a line starting `ERROR` or `FATAL`: that line
  stripped; no `{`: `psql gave no answer`; the answer is the joined
  text from the first `{`.
- Any of those errors: kill the process and refuse for `Backoff`
  with `waiting before trying psql again`. A JSON decode error does
  not trigger the back-off (as Python).
- Start failure: `cannot run docker: ` + `pyfmt.StrError(err)` (on
  Linux and macOS `No such file or directory` when docker is
  missing).
- One query at a time (mutex). `Stop` kills.

**Direct** (`--docker`), a port of `Pg`:

- `pgconn.ParseConfig(url)`; `ConnectTimeout` 5 s; `RuntimeParams`
  `default_transaction_read_only=on`, `statement_timeout=5s`,
  `application_name=frontdesk`.
- Each query in its own goroutine with an 8 s wait: simple protocol
  `begin read only;\n<sql>\ncommit;` in one `Exec`; the value is the
  second result's first row, first column; NULL or not a JSON object:
  `the database gave no answer`. The connection is kept between
  queries; a query that times out leaves its goroutine to close its
  own connection (as Python's box).
- Messages: `DATABASE_URL is not set in seats.env`, `the database
  did not answer in time`, else the first line of the error text (or
  its Go type name); back-off 30 s with `waiting before trying the
  database again`. Driver wording differs from psycopg's (ledger
  C-1); always scrubbed by the caller.

**Once** (the game), a port of `pg()`: `OnceArgv` in `dir`, the
script on stdin, stdout and stderr captured apart; exit code not 0:
`cannot read the proxy database: <stderr stripped>`; else
`json.loads(stdout.strip() or "[]")` with numbers as json.Number;
docker missing: `cannot read the proxy database: cannot run docker:
<StrError>` (ledger O-7: Python crashed).

Read only, three locks everywhere: the session default (Session,
Direct), `BEGIN READ ONLY` on each question, and SQL that is fixed
text in the code (only names that passed rules checks are quoted in,
with Python's `lit()`).

### 9.6 The HTTP server (`web`)

- `net/http` with our own dispatcher (no `ServeMux`: no path cleaning
  or redirects). `http.Server{ReadHeaderTimeout: 10s, ReadTimeout:
  10s, IdleTimeout: 10s, MaxHeaderBytes: 1 << 20, ErrorLog:
  log.New(io.Discard, "", 0), DisableGeneralOptionsHandler: true}`.
- Listener: `net.Listen("tcp4", bind:port)` wrapped with caps: 256
  open connections in all; with `--lan` or `--docker`, 32 per remote
  address. Over a cap: close the new connection at once and keep
  accepting. Counted for the connection's whole life (streams too),
  given back exactly once on close.
- Python's own request limits, answered in the handler as Python's
  `send_error` twin (JSON, safety headers, `Connection: close`):
  request line longer than 65,536 bytes: 414; more than 100 header
  lines or one longer than 65,536: 431. Beyond 1 MiB of headers,
  Go's own 431 (ledger H-1).
- Every answer we write: `Content-Type`, an explicit
  `Content-Length` (never chunked, except SSE which is
  close-delimited), `Cache-Control`, `Vary: Accept-Encoding` when a
  zipped copy exists, `Content-Encoding: gzip` when the request's
  `Accept-Encoding` contains `gzip` as a substring (case matters),
  the four safety headers, `Server: FrontDesk`, and `Connection:
  close` on every POST answer and every send_error answer.
- Path: the raw request target cut at `?`, not decoded
  (`/%61pp.js` is a 404).
- A handler wrapper recovers panics: logs `error: ` + scrubbed text
  cut to 200, answers 500 with Python's text (GET `Something went
  wrong at the desk.`; POST that plus ` See the terminal where Front
  Desk runs.` or, in `--docker`, ` See its log: docker compose logs
  frontdesk`). A client that hung up or timed out: no answer, no
  log. Requests are never logged.

### 9.7 Routes (`web`)

- Methods other than GET, HEAD, POST (and `OPTIONS *`): 501
  `{"error": "Not Implemented."}`, connection closed.
- HEAD: for the page's files only (then as GET, headers only), else
  405 `Use GET.`, before the Host check.
- GET: Host check (421) -> static -> `/api/session` -> not
  `/api/state` or `/api/events`: 404 `Nothing here.` -> no session:
  401 `Log in first.` -> events or state.
- Static: the embedded files, each gzipped once at level 9, ETag
  from its bytes, `Cache-Control: no-cache`; `If-None-Match` equal
  to the ETag gives 304 with the ETag; `/` is `index.html`. Types:
  `text/html; charset=utf-8`, `text/javascript; charset=utf-8`,
  `text/css; charset=utf-8`, `image/svg+xml`.
- `/api/state`: `Fresh()`, then ETag and 304 or 200 with the shared
  gzip, `Cache-Control: no-cache, private`.
- `/api/session`: `{"lan", "docker", "need_login", "csrf",
  "seat_url", "reset_cmd"}` (9.12): `lan` is `--lan`, or in
  `--docker` `over_network()`; `seat_url` only with a csrf.
- POST: Host check (421) -> `Origin`, if sent, must equal `http://`
  + Host (403 `Requests from other sites are refused.`) -> body
  (9.8; 400) -> `/api/login` -> session (401) -> `X-CSRF-Token`
  constant-time equal (403 `Reload the page and try again.`) ->
  `/api/logout` -> `/api/mint` -> `/api/revoke` -> 404.
- Error bodies: `{"error": msg}` plus `"field"` for form fields.

### 9.8 POST bodies (`web` body.go)

`Content-Type` before `;` exactly `application/json`, else `Send
JSON.`; `Content-Length` missing or empty means 0 (a chunked body is
not read); not an integer: `Send valid JSON.`; negative or over 8192:
`Too much data.`; read that many bytes (empty means `{}`); a UTF-8
BOM is dropped (Python's `json.loads(bytes)` accepts it); not valid
UTF-8: `Send valid JSON.` (checked before decoding, so such a body
never counts as a wrong password); UTF-16 or UTF-32 bodies, `NaN`,
`Infinity`: `Send valid JSON.` (ledger H-6); not an object: `Send a
JSON object.`. Decoding uses `UseNumber`, so `budget` and `duration`
given as JSON numbers become text by `pyfmt.Str`, as Python's
`str()`.

### 9.9 Gate: password, sessions, lockout (`gate`)

A line-by-line port of `Gate` with an injected `clock.Clock`:

- No password (local, no `--login`): one CSRF token
  (`token_urlsafe(24)`) made at start for everyone; `/api/login`
  returns it.
- Password check: SHA-256 of the UTF-8 bytes, constant-time compare.
  `password` must be a JSON string of at most 200 code points, else
  `Type the password.` (400).
- Lockout on the monotonic clock: drop addresses whose last failure
  is older than 600 s; 5 or more failures: `Refused` with wait =
  max(1, ceil(last + 600 - tick)) and `Too many wrong passwords.
  Wait N minutes.` (`1 minute`); drop failures older than 600 s; a
  recent failure here and tick - last_fail < FAIL_GAP: `Refused`
  `Too many tries just now. Wait a second and try again.` (wait 1);
  wrong: record, then `Wrong password. That was the last try: wait 10
  minutes.` (locks) or `Wrong password. N tries left before a
  10-minute lock.` (`1 try`). 429 with `Retry-After` for Refused, not
  logged; 401 for wrong, logged `wrong password from <addr>` plus `:
  locked for 10 minutes` on the locking try.
- Sessions on the wall clock: cookie `token_urlsafe(32)` (43
  characters), csrf `token_urlsafe(24)` (32), (expires = wall +
  12 h, csrf, address); expired ones dropped at login; at most 100,
  the smallest (expires, csrf) dropped; `CSRFFor(cookie, addr)`
  drops an expired one and answers only for the address that logged
  in. Log `logged in: <addr>`.
- `Set-Cookie: fd_<public port>=<cookie>; Max-Age=43200; Path=/;
  HttpOnly; SameSite=Strict`, written as that exact string; logout:
  `fd_<public port>=; Max-Age=0; Path=/; HttpOnly; SameSite=Strict`.
  Cookie read with Go's parser (ledger F-8).
- Start: `FRONTDESK_PASSWORD` read, then `os.Unsetenv` before any
  child; empty means none. With `--lan`, `--login` or `--docker`: a
  given one must be 12 code points or more (`FRONTDESK_PASSWORD must
  be 12 characters or more.`; in `--docker` `... or more. Fix it in
  seats.env, then run: docker compose --profile frontdesk up -d`)
  and must be valid UTF-8 without U+FFFD (`FRONTDESK_PASSWORD has a
  character that is not plain text. Pick another password.` plus the
  same docker tail), exit 1 (ledger F-7); none given: `MakePassword`,
  shown in the banner.

### 9.10 Mint and revoke (`web` mint.go)

- Validation in Python's order and words: `Fresh()`; seat a string,
  `pyfmt.Strip`, `SEAT_RE` (field seat); game names via `Owns` (`Names
  that start with <prefix> are kept for the office game. Pick another
  name.` or `That name is kept ...`); proxy up (503 `The proxy is not
  answering, so no card can be made right now.`); models a non-empty
  list of known model strings, then sorted unique; budget
  `pyfmt.Strip(pyfmt.Str(v))` matching `BUDGET_RE` and > 0; refill
  exactly one of `""`, `1d`, `1w`, `1mo` and a string; duration
  `Strip(Str(v))` matching `DURATION_RE`; a card with the same seat by
  `Lower` (409, `<seat> already has a key card. Cancel it first, or
  pick a new name.`).
- The call, under one mutex (one mint or revoke at a time), with a
  30 s context: `mintkey.Exec(ctx, file, args, 25s)` where args are
  Python's (`<seat> --models a,b --budget B --duration D
  [--budget-duration R]`, `--revoke-seat <seat>`, `--revoke
  <token>`). `file`: host mode `seats.Load(settings.Path,
  os.LookupEnv)`, read fresh for each call (as mint-key.sh did);
  `--docker` `seats.FromEnviron` of the environment cut to
  `MINT_ENV`'s names.
- Result handling as `subprocess.run`: key = strip(stdout); exit not
  0 or key not `KEY_RE`: log `mint failed for seat <s>`, 400 with
  `plain(stderr)`, field seat when the message has `taken`. The 30 s
  context ending: as Python's TimeoutExpired, the 500 path.
- `plain` is `plain_error` exactly: scrub, `SECRET_RE` to
  `[hidden]`, last non-blank line by `SplitLines`, log `mint-key
  said: <last cut to 300>` (or `nothing`), then the matches in order:
  `already exists`, `Authentication Error` or `error: 401`, `No keys
  found` or `error: 404`, `Failed to connect` or `(7)` or `(52)`,
  `change-me`, else `mint-key said: <last>` (or `nothing (it
  failed)`). A contract test in `mintkey` pins that its messages
  still hold `error: 401`, `error: 404`, `Failed to connect`,
  `already exists`, `No keys found`, `change-me` where it can produce
  them.
- Success: log `minted a key card for seat <s>`, `ShowChange(seat,
  false)`, 200 `{"seat", "key", "models", "budget", "duration",
  "refill", "base_url"}`.
- Revoke: by `id`, else by `seat`; none: 404 `No key card like that.
  It may be cancelled already.`; a card with no seat name by its
  token (`That card is gone. Reload.` if unknown), logging `(no seat
  name)`; failure: log `revoke failed for seat <what>`, 400 plain;
  success: log `cancelled the key card for seat <what>`,
  `ShowChange(seat, true)` or `RefreshNow()`, 200 `{"ok": true,
  "seat": ...}`.

### 9.11 SSE (`web` events.go)

```go
// in the handler, after the 503 check and Fresh():
h := w.Header()
h.Set("Content-Type", "text/event-stream")
h.Set("Cache-Control", "no-store")
h.Set("Connection", "close")
h.Set("Transfer-Encoding", "identity") // close-delimited, no chunks
// + the four safety headers and Server: FrontDesk
rc := http.NewResponseController(w)
rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
w.WriteHeader(200)
// "retry: 3000\n\n", then the loop
```

- At most MAX_STREAMS (100) viewers: else 503 `Too many open pages.`;
  viewers decremented in a defer.
- Loop while `CSRFFor(cookie, addr)` holds: wait for a new version, 1
  s, or `r.Context().Done()` (the browser closed: Go cancels at once,
  verified; replaces `select` and `MSG_PEEK`); then `event:
  changed\ndata: {"v":N}\n\n` on a new version, else after
  PING_EVERY (15 s, mono) since the last write `event: tick\ndata:
  {"age":A}\n\n` with A = max(0, wall - checked) as `%.1f`; before
  each write a fresh 10 s write deadline; flush; update `wrote` and
  the desk's `seen`.
- The first `changed` goes out at once with `retry`; a logout or
  expiry ends the stream within a second.

### 9.12 The reset command on the page (gap 4)

- `/api/session` gains `"reset_cmd"`: host mode `args.Prog() + "
  game reset"` (so `./lvp game reset`, `.\lvp game reset` on
  Windows, `lvp game reset` when on PATH); `--docker`: `lvp game
  reset` (the container cannot know the host's system; the docs teach
  the `./` and `.\` forms once).
- `gui/static/app.js` (C2 edits only these lines): keep
  `const RESET = "python3 game/office.py reset";` as the fallback
  for the Python server, add `let resetCmd = RESET;`, set `resetCmd
  = s.reset_cmd || RESET;` in `boot()` next to `needLogin`, and use
  `resetCmd` at the two places that show `RESET` (today lines 612 and
  658). At Gate C, C5 drops the Python fallback (ledger F-6).

### 9.13 Start, banners, stop (`frontdesk`)

Flags: `--lan`, `--port N` (default 8040; 0..65535 else exit 2),
`--login`, `--host NAME` (repeatable), `--seat-url URL`, `--env
FILE`, `--docker`, `-h/--help`. `--docker` reads `FRONTDESK_PORT`
(digits, 1..65535, else `--port`) as the public port, appends
`FRONTDESK_HOSTS` split on `[\s,]+`, and `FRONTDESK_SEAT_URL` when
`--seat-url` is not given.

Sequence as `main()`: settings; password; tally source (Direct in
`--docker`, else Session in the proxy folder); desk; gate; server;
bind `0.0.0.0` when `--lan` or `--docker` else `127.0.0.1`, tcp4;
bind error: `cannot use port P: <StrError>. Try --port P+1.` exit 1;
start the desk and kick it once (psql's start is paid there); print
the banner and flush; serve.

Host banner, exact lines:

```text
Front Desk is open.
  On this computer: http://127.0.0.1:<port>
  On your network:  http://<lan_ip or <proxy-ip>>:<port>   (--lan)
  Plain http: use it only on a network you trust.           (--lan)
  Password, shown once: <pw>          (made here)
  Password: the one in FRONTDESK_PASSWORD     (given)
  Anyone logged in to this computer can use it; on a shared one,
  add --login.                        (no password; one line)
Stop it with Ctrl+C.
```

Docker banner: `docker_banner` exactly (`Front Desk is open, in
Docker.`, the two `FRONTDESK_BIND` variants, `Password, new at each
start: <pw>` and its `To keep one, set FRONTDESK_PASSWORD in
seats.env (12 characters or more), then run: docker compose
--profile frontdesk up -d` line, or `Password: the one in
FRONTDESK_PASSWORD in seats.env`, `Stop it with: docker compose stop
frontdesk`).

Stop: SIGINT, and SIGTERM too (ledger F-20): print an empty line,
stop the tally source (kill psql), exit 0. In the image STOPSIGNAL is
SIGINT anyway.

## 10. Host mode and container mode

From maintainer's table, checked against go-port's code. C1
re-checks it against the merged code (section 1).

| Point          | Host mode (default)        | `--docker` (the image)     |
|----------------|----------------------------|----------------------------|
| Settings       | seats.env file, reread on  | environment (compose       |
|                | change                     | `env_file: seats.env`),    |
|                |                            | once                       |
| Listen         | tcp4 127.0.0.1:`--port`;   | tcp4 0.0.0.0:`--port`      |
|                | 0.0.0.0 with `--lan`       | (8040) in the container;   |
|                |                            | compose publishes          |
|                |                            | `${FRONTDESK_BIND:-        |
|                |                            | 127.0.0.1}:${FRONTDESK_    |
|                |                            | PORT:-8040}:8040`          |
| Password       | with `--lan` or `--login`  | always; printed `Password, |
|                |                            | new at each start` if not  |
|                |                            | set                        |
| Per-device cap | with `--lan`               | always                     |
| `desk.lan`     | `--lan`                    | true                       |
| session `lan`  | `--lan`                    | `over_network()`           |
| Host check     | local: exactly             | `localhost`, a             |
|                | `127.0.0.1:P`,             | `FRONTDESK_HOSTS` or       |
|                | `localhost:P`, `[::1]:P`;  | `--host` name, or an IP    |
|                | `--lan`: localhost,        | literal; any port          |
|                | hostname, `<h>.local`,     |                            |
|                | `<first label>.local`,     |                            |
|                | `--host`, or an IP literal;|                            |
|                | port must be P (80 if none)|                            |
| Cookie         | `fd_<--port>`              | `fd_<FRONTDESK_PORT or     |
|                |                            | --port>`                   |
| 421 text       | local or `--lan` text with | docker text with the       |
|                | `--port`                   | public port                |
| Seat URL       | `--seat-url`; `--lan`: the | `FRONTDESK_SEAT_URL` or    |
|                | Host name unless loopback; | `--seat-url`; else the     |
|                | else `lan_ip`; else        | Host name or `<proxy-ip>`; |
|                | `<proxy-ip>`; PROXY_URL's  | port 4000 (SEAT_PORT)      |
|                | port                       |                            |
| Tally          | `pgread.Session` in the    | `pgread.Direct` to         |
|                | proxy folder               | `DATABASE_URL`             |
| Fallback tally | the proxy's numbers        | the same                   |
| Minting        | in process, seats.env file | in process, environment    |
|                |                            | cut to MINT_ENV            |
| Game folder    | `<proxy folder>/game`      | `game` under the working   |
|                |                            | folder: `/app/game`, the   |
|                |                            | read-only mount            |
| POST 500 text  | `See the terminal where    | `See its log: docker       |
|                | Front Desk runs.`          | compose logs frontdesk`    |
| Banner         | host banner                | docker banner              |
| `reset_cmd`    | `<Prog> game reset`        | `lvp game reset`           |
| Stop           | Ctrl+C or SIGTERM          | STOPSIGNAL SIGINT          |

`over_network()` takes the Host's name (`host_name()`: the urlsplit
hostname if it matches `HOST_RE`, else ""). True when that name is
an IP address that is not loopback, or is not an IP address and is
not `localhost` (so "" is true), as Python.

## 11. The office game in detail

### 11.1 Rules

`rules.Load` decodes with BurntSushi into the typed structs, then
runs `load_rules`' checks in Python's order with Python's words:
`cannot read <path>: <OSErrorText or TOML error>`; `bad duration
<repr>: use a whole number and s, m, h or d`; `day.length <repr>
must split the next unit evenly, like 1m, 2m, 5m, 1h or 1d`;
`day.close must be under half of day.length`; `seat name <repr>:
1-6 lowercase letters/digits`; `seat <name>: no persona <repr>`;
`seat name <repr> is used twice`; `office.seat_prefix: lowercase
letters, digits and - only`. A missing table or key, or a wrong type,
fails first with `rules.toml line N: <table.key> is missing` or
`... must be a whole number` / `a number` / `text` / `true or false`
/ `a list of two numbers` (ledger O-1). Unknown keys: one warning
line each on stderr, `rules.toml line N: unknown key <table.key>
(ignored)`; never fatal.

### 11.2 State and dice

- `gamestate` holds the file; `game` office.go wraps it as
  `Office{db, rules, id}` with `q`, `meta` (`INSERT OR REPLACE INTO
  meta VALUES (?, ?)` with Python's `str(v)`), `players` (ordered by
  seat) and `claim` (`db.Claim` with `rng.Choice`).
- Seeds: `pyrand.New(fmt.Sprintf("%s-quests-%d", seed.Text, day))`
  and `"%s-%d-%s"` with the player's name. Every draw happens in the
  same order as office.py: review checks it line by line and goldens
  prove it.
- `post_quests`: `names` sorted; `Choices` with the sizes' weights;
  ids `d<day>q<NN>`.

### 11.3 Bots and the day

- One goroutine per player with Python's field set; `run`, `work`,
  `call` line by line: the pause and deadline check, `RandInt` size,
  the prompt text (`Quest <id>, step <n>: ` plus `Choice(WORDS)` +
  " " until long enough), the body (`model`, `max_tokens`,
  `messages`, `metadata.tags` with the four tags, `stream` and
  `stream_options` for streaming personas), three tries with sleeps
  of 1, 2, 3 s after each failed one (the last included), no retry on
  422 or `budget_exceeded`, idle after a refusal, daylog row written
  at the end (`INSERT OR REPLACE`).
- `wait_for_day`, `play_day`, `wait_for_logs` on the wall clock
  (`clock.Clock`), every printed line identical (`Day N starts now;
  ...`, `Day N starts at HH:MM:SS UTC, when LiteLLM refills the mana
  (in N s).`, `  reset job late; re-minting N seats`, `  N quests
  posted; bots work until ... UTC, mana resets at ....`). Join: for
  each bot in order, wait up to max(1 s, deadline + 70 - now). Bots
  still running after that are cancelled and write nothing more
  (ledger O-4).
- Tag `<day>.<4 hex>`; office id 6 hex; `created` as
  `%Y-%m-%d %H:%M UTC` in UTC.

### 11.4 The chat client (chat.go)

POST `<url>/v1/chat/completions`, `Authorization: Bearer <seat
key>`, `Content-Type: application/json`, `User-Agent:
lvg-office/1`, 60 s, the answer read to the end; never proxied for
localhost or loopback (ledger O-6); non-2xx: `error.type` from the
JSON body (else ""), never the message. Transport errors keep
urllib's class names, decided with `httptrace.WroteRequest`: before
the request was fully sent (refused, DNS, connect timeout, write
error): `URLError`; after: timeout `TimeoutError`, closed before a
status line `RemoteDisconnected`, reset `ConnectionResetError`, a
body cut short `IncompleteRead` (ledger O-3); status 0.

### 11.5 Minting and revoking (minter.go)

- argv `[os.Executable(), "mint-key", ...]` (tests inject another
  argv through `game.Options`), working folder the proxy folder,
  environment passed through (so `SEATS_ENV` works as before).
- `mint(alias)`: `<alias> --models <office.models joined by ,>
  --budget <money(daily * usd)> --duration <key_life>
  --budget-duration <day.length>`; key = strip(stdout); exit not 0 or
  no `sk-` prefix: the stripped stderr, or `mint-key gave no key`;
  else `game/keys/` made private (fsx.PrivateDir) and
  `<alias>.key` written with key + "\n", private.
- `revoke(alias)`: `--revoke-seat <alias>`; err = stderr words joined
  by one space (`pyfmt.Fields`); exit 0: `revoked`; `No keys found` in
  err: `no key`; else err or `mint-key exit N`; the key file is
  removed (fsx.Remove) only for `revoked` or `no key`.

### 11.6 Scoring and boards

`score_day`, `streak`, `day_summary`, `rank_corr`, `scored_days`,
`heading`, `board`, `seat_board` line by line, with the same SQL
text, row order (no `ORDER BY` added or removed), sums in the same
order, stable sorts with the same keys, and every number printed by
`pyfmt.Format` with Python's spec (`>7.1f`, `>9,`, `>6,.0f`,
`>4.2f`, `+.2f`, `>+5.0%`, `<36`, `<6`, `<8`, `>2`, `>4`, `:,`,
`:.0f`, `02.0f`, `.1f`, `.2f`), `pyfmt.RoundInt` for the guild bars,
`FloorDiv` and `Mod` for `m:ss`. `top = max(avgs + [1])` keeps the int
1. `heading` prints `mana.daily` with `:,` of its value (int or
float as written) and `money(mana.usd)`.

### 11.7 Commands (game.go, commands.go)

- Flags: `lvp game [--rules FILE] <setup|run|board|reset>`; `run -d/
  --days N` (positive: `'<text>': use 1 or more`), `-q/--quiet`;
  `board -d/--day N`, `-s/--seat NAME`; `--rules` may also follow
  the command. Rules load before the command, as Python.
- `setup`, `run`, `board`, `reset`: as `cmd_*`, with the rename table
  (11.9). `reset` revokes first; then removes `office.db*` with
  `fsx.Remove`; a file still in use after 2 s (Windows): `game/
  office.db is in use by another program, likely a running lvp game
  run or Front Desk. Stop it, then run <Prog> game reset again.`,
  exit 1 (ledger O-11).
- Ctrl+C: `signal.NotifyContext`; every sleep and wait takes the
  context; when a command ends with the context done, print `\n` and
  the command's STOPPED text to stderr, exit 1. A closed stdout pipe
  ends quietly (Go's default on Linux and macOS matches SIG_DFL; on
  Windows a write error ends the command, exit 0).

### 11.8 Front Desk and the game together

Front Desk reads only `rules.toml` and `office.db` (read only) from
the game folder; never `keys/`. The game never imports Front Desk.

### 11.9 The rename table (messages that named old files)

| Python says                         | Go says                    |
|-------------------------------------|----------------------------|
| `python3 game/office.py <cmd>`      | `<Prog> game <cmd>`        |
| `` `office.py <cmd>` ``             | `` `<Prog> game <cmd>` ``  |
| `mint-key.sh gave no key`           | `mint-key gave no key`     |
| `mint-key.sh exit N`                | `mint-key exit N`          |
| `mint-key.sh said:` (host mode)     | `mint-key said:`           |
| `so mint-key.sh will refuse to run` | `so mint-key will refuse   |
|                                     | to run`                    |
| `office.py needs Python 3.11 ...`   | removed                    |
| `Front Desk needs Python 3.11 ...`  | removed                    |
| argparse prog `frontdesk.py`,       | `lvp desk`, `lvp game ...` |
| `office.py`                         |                            |

`<Prog>` is `args.Prog()`. Tests normalise it to `lvp`. Messages that
cite README steps keep their words (`do README Step 3 first`, `Do
README Step 3a.`, `Do Step 3a.`); C5 keeps those step numbers.

## 12. fake-ollama in detail

A line-by-line port: listen `tcp4` `0.0.0.0:11434`; print `practice
model server on :11434`; `GET /` `Ollama is running` (text/plain),
`/api/version`, `/api/tags`; POST body parsed first (`Content-Length`,
`{}` if empty; bad JSON 400 `{"error": "bad json"}`), then
`/api/show`, `/api/chat`, else 404 `{"error": "not found"}`; paths
match the raw target exactly, query included. `tokens` =
max(1, RoundInt(code points / 4)); `prompt_text` with content parts;
`num_predict` default 128, `int()` of it (int, float or digit text),
kept in 1..4096; reply `filler(RandInt(max(1, limit // 2), limit))`;
think time `uniform(0.05, 0.25) + r_tok * 0.0005` s; the same JSON
fields; streams built whole, 8 words a line, then the done line, as
`application/x-ndjson` with a Content-Length. Words come from
`math/rand/v2` (unseeded in both versions). Nothing is logged.
HTTP/1.1 keep-alive where Python closed each connection (ledger P-1).

## 13. Windows, macOS and Linux details

- **File locking (gap 3).** Python never ran natively on Windows.
  Every write the port makes to a file another program may hold
  (`game/keys/*.key`, `seats.env` and `.env` by C5's helpers) goes
  through `fsx.WriteFile` (temp file, rename, 2 s retry); every
  delete (`office.db*` in `reset`, key files in `revoke`) through
  `fsx.Remove` (2 s retry, then a plain message). SQLite holds
  `office.db` only while the game runs; Front Desk opens it read only
  for milliseconds and closes it. `fsx` tests on Windows hold a file
  open from another process and check the retry and the message.
- **Owner-only files (gap 7).** `fsx.Private` sets 0600/0700 on Linux
  and macOS and, on Windows, a protected access list (the user and
  SYSTEM only, SDDL `D:P(A;;FA;;;<user SID>)(A;;FA;;;SY)`, through
  `golang.org/x/sys/windows`). Used for game key files and their
  folder (C3), `seats.env` by `lvp init`, saved keys by `lvp mint
  --save` (C5). `lvp check` reports a `seats.env` others may read,
  with the fix. The docs also say: keep the folder in your user
  folder.
- **How to type lvp (gap 4).** `args.Prog()` (7.1) in every message
  that names a command, and in `reset_cmd`.
- **Ctrl+C.** Windows sends it to every program on the console, the
  `docker` child too; Front Desk then stops psql anyway. The game's
  child `lvp mint-key` stops with it; the command prints STOPPED.
- **Firewall prompts.** `lvp desk --lan` makes Windows Defender and
  macOS ask once; the docs say what to click (private networks only).
- **Unsigned programs.** macOS Gatekeeper and Windows SmartScreen warn
  once per file; the docs show the clicks and the `xattr` line.
- **PowerShell traps** the helpers remove: `>` writes UTF-16 in
  Windows PowerShell 5.1 (`lvp mint --save` instead of redirecting a
  key; `lvp env set` instead of `echo ... >> .env`); `$20` and bare
  commas in arguments (docs note).
- **CRLF and BOM** in seats.env from Notepad: `internal/seats`
  already drops them.
- **Paths with spaces**: no shell is ever used; CI tests a game
  folder path with a space on Windows.
- **Double-click.** `lvp` with no arguments, started by double-click
  on Windows (the console has only this process), prints the short
  help and waits for Enter (cli, optional, C1).
- **arm64 (gap 6).** Verified today: `ghcr.io/berriai/litellm:
  v1.106.6` lists `linux/arm64` and `linux/amd64`; `postgres:
  16-alpine` lists arm64; `golang:1.27-alpine` exists. So arm64
  Linux machines (Raspberry Pi 4/5) and Windows on Arm with Docker
  Desktop can run the whole stack. C4 re-checks it in the release
  job with `docker buildx imagetools inspect` and fails the release
  if a pinned image loses a platform.

## 14. The image and compose

### 14.1 Dockerfile (root; replaces gui/Dockerfile)

```dockerfile
# Front Desk and the practice model server, Go only. Compose builds
# it: docker compose --profile frontdesk up -d
ARG GO_DIGEST=<digest of golang:1.27-alpine, pinned by C4>
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:${GO_DIGEST} \
  AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY gui/ gui/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
      go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/lvp ./cmd/lvp \
 && go run ./internal/notices -bin /out/lvp \
      > /out/THIRD_PARTY_NOTICES.txt \
 && mkdir -p /out/app/game /out/tmp \
 && echo "# Empty on purpose: see the Dockerfile." > /out/app/seats.env

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/lvp /lvp
COPY --from=build /out/THIRD_PARTY_NOTICES.txt /
COPY --from=build /out/app/ /app/
# The mount point for compose's tmpfs; scratch has no /tmp.
COPY --from=build /out/tmp /tmp
# mint-key in the image reads this empty file, then the environment
# that compose passes in from seats.env. Never copy the real one.
ENV SEATS_ENV=/app/seats.env
WORKDIR /app
USER 10001:10001
EXPOSE 8040
STOPSIGNAL SIGINT
ENTRYPOINT ["/lvp"]
CMD ["desk", "--docker"]
```

(The `FROM` line is split here for width; the real file keeps it
under 80 columns with an `ARG` for the image name.) What this keeps:
`docker compose run --rm frontdesk mint-key laptop` runs `/lvp
mint-key laptop`, as gui/README documents today. Size: about 12 MB.
No shell, no package manager, no Python (ledger C-3).

### 14.2 .dockerignore

go-port's allow-list, with `!go.sum` added after `!go.mod`; nothing
else widened. A CI check builds the context and fails if it holds
`seats.env*` (other than the example, which is not allowed in
either), `.env`, `*.key` or `office.db*`, and inspects the image for
the same.

### 14.3 compose.yaml (C4)

- `frontdesk` service as the finished go-port one, with `build:
  {context: ., dockerfile: Dockerfile}` (no image name, so nothing is
  ever pulled under it); everything else unchanged (`profiles`,
  `env_file: seats.env`, `PROXY_URL: http://proxy:4000`,
  `FRONTDESK_BIND/PORT/HOSTS/SEAT_URL`, `./game:/app/game:ro`,
  ports, `read_only`, `tmpfs: [/tmp]`, `cap_drop: [ALL]`,
  `no-new-privileges`, `depends_on`, `restart`).
- **Gap 2**: `proxy` and `db` get `environment: FRONTDESK_PASSWORD:
  ""` (environment beats env_file), with a comment: Front Desk's
  password stays out of LiteLLM's and Postgres's containers. C4 checks
  on a live stack that `docker compose exec proxy printenv
  FRONTDESK_PASSWORD` prints an empty line, and that `docker inspect`
  of proxy and db shows it empty.

### 14.4 game/compose.game.yaml (C4)

```yaml
  fake-ollama:
    build:
      context: .
      dockerfile: Dockerfile
    command: ["fake-ollama"]
    read_only: true
    cap_drop: [ALL]
    security_opt:
      - no-new-privileges:true
    restart: unless-stopped
```

No ports, as now; the proxy's overlay settings unchanged. The first
`up` builds the image (Go builder and modules, about 130 MB once,
a minute or two) where Python pulled `python:3.12-slim` (ledger C-4).

## 15. CI and release

### 15.1 ci.yml (C1)

- `test`: `go vet ./...` and `go test ./...` on ubuntu-latest,
  ubuntu-24.04-arm, macos-latest (arm64), windows-latest, and where
  GitHub offers them an Intel macOS runner and windows-11-arm; Go
  1.26.x and 1.27.x, `check-latest: true`. (Gate A's G-1 counts the
  runners that exist when it runs.)
- `checks` (ubuntu): gofmt; `go test -race ./...`; build `lvp` and
  `mint-key` for six targets; `internal/archtest` (imports, widths,
  model ids, reference scan); govulncheck (pinned); `bash -n` on the
  shell scripts; `python3 -m compileall -q gui game` until Gate C.
- `pg` (ubuntu, `postgres:16-alpine` service, seeded from
  `internal/pgread/testdata/schema.sql`): `go test -tags pg ./...`
  (Direct is read only: an INSERT fails; Front Desk's three SQL
  texts run).

### 15.2 parity.yml and the image job (C4)

- `parity` (ubuntu, until Gate C): Python 3.12, psycopg from
  `gui/requirements-docker.txt` by hash, bash, curl, jq, a Postgres
  16 service; `go test -tags parity ./tests/parity/...`.
- `image`: `docker buildx build` for linux/amd64 and linux/arm64;
  checks user 10001, size under 20 MB, no secrets in context or
  image, `docker run --rm <image> version`, `docker run --rm <image>
  mint-key --version`.

### 15.3 release.yml (C4, extends go-port's)

On a `v*` tag:

1. Tests (as ci.yml `test` on ubuntu) and govulncheck.
2. Linux job: `lvp` and `mint-key` for linux and windows, amd64 and
   arm64, `CGO_ENABLED=0 -trimpath -ldflags "-s -w -X
   main.version=$TAG"`.
3. macOS job: both darwin builds, then `lipo -create` into one
   universal `lvp` and one universal `mint-key`; `lipo -info` must
   list x86_64 and arm64.
4. Pack: `git archive` of the tag (with `.gitattributes`
   `export-ignore` on `.github/` and `tests/`), plus the binaries and
   `THIRD_PARTY_NOTICES.txt` (`go run ./internal/notices -bin lvp`:
   Go's own LICENSE from GOROOT and each linked module's license
   file from the module cache; fails on a module without one). Every
   file's time set to the tag's commit time (SOURCE_DATE_EPOCH);
   `tar --sort=name --mtime=@$SOURCE_DATE_EPOCH --owner=0 --group=0
   --numeric-owner | gzip -n`; zip from a sorted list with `-X`.
5. `SHA256SUMS`; a second pack of the same tag must give the same
   sums (checked in the job).
6. Platforms check of the pinned images (13, arm64).
7. `gh release create` as a draft, for the user to publish.

Signing, notarising and publishing images are the user's calls
(questions 23.2, 23.3).

## 16. Tests and the parity proof

Seven layers. 1 to 3 run in CI on three systems with no Docker and
no Python; 4 runs in CI with Python; 5 to 7 need the real stack.

### 16.0 Step zero: bring the reference material in (gap 5; C1)

Before any golden is written, copy into the repo, on the port branch:

- `tests/reference/security/`: the scripts and recorded outputs from
  `scratchpad/sim/lvgui-security-r4/vb/` (`*_vb.py`, `lib.py`,
  `*.js`, `cap4162.out`, `cap4163.out`, `lock4162.out`,
  `lock4163.out`, `xss4162.out`, `xss4163.out`; not `*.pid`,
  `fd*.err`, `fd*.out` or `shots/`).
- `tests/reference/perf/`: from `scratchpad/tools-ver/` the scripts
  (`load.py`, `mint_ab.py`, `sse_bench2.py`, `tally_bench.py`,
  `tally_scale.py`, `startup.py`, `chat.py`, `seed.py`, `inputs.py`,
  `lock.py`, `rawhost.py`, `sec_fix.py`, the `*.js` page scripts) and
  the baselines (`before-*.txt`, `after-*.txt`); not `lan.out`,
  `lan2.out` and other files that printed a password.
- `tests/reference/game/`: `scratchpad/sim/lvg-bal-week{1,2,3}-
  office.db`, `-board.txt`, `-verify.txt`.
- `tests/reference/litellm/`: the LiteLLM v1.106.6 probe notes
  (`scratchpad/facts.json`, `probe_issues.json`, `fake_litellm.py`),
  the starting point for `fakelitellm`'s answers.
- `tests/reference/README.md`: where each came from, what it proves,
  which test uses it.

Every file is scanned before the commit, and on every CI run by
`archtest`: no `sk-` followed by 20 or more key characters, no
16-character dashed password (`[a-z2-9]{4}(-[a-z2-9]{4}){3}`), no
hex run of 32 or more outside the recorded `[hidden]` forms. A hit
fails the build.

### 16.1 Layer 1: unit tests in every package

Table-driven; fake clock; fake programs built in `TestMain` (Go, so
Windows needs no bash): `fakedocker` (persistent psql protocol and
the one-shot script, scripted answers, stall, ERROR, FATAL, death),
a fake `mint-key` argv for the game. `fakelitellm` for every proxy
call. `-race` on Linux.

### 16.2 Layer 2: Python goldens, captured once

`tests/golden/<name>.py` imports the frozen Python by path (both
files have a `__main__` guard), pins what is random (the Desk
secret, clocks, tokens) and writes JSON or text into the owning
package's `testdata/golden/`. CI reads the data; it never runs
these. Recapturing is a reviewed diff. Python version used is
written in `tests/golden/README.md`.

| Golden suite             | Owner | Covers                            |
|--------------------------|-------|-----------------------------------|
| `pyfmt.py`               | C1    | specs x values, round, repr, str, |
|                          |       | strip, splitlines, split, lower,  |
|                          |       | ISO parse/format (LiteLLM forms   |
|                          |       | and fuzz), urlsplit hosts,        |
|                          |       | ip_address, OS error texts        |
| `pyrand.py`              | C1    | 200 seeds x every operation       |
| `rules.py`               | C1    | good files; 30+ bad files with    |
|                          |       | their exact messages; SeatNames   |
| `psql.py`                | C1    | the exact stdin bytes of Psql and |
|                          |       | pg() for fixed marks              |
| `frontdesk_core.py`      | C2    | seat_state, dollars, utc_text,    |
|                          |       | merge_tally, http_tally, build()  |
|                          |       | (200 seats, refills, blocked,     |
|                          |       | expired, game seats, blank and    |
|                          |       | hostile names; same secret, same  |
|                          |       | ids), SQL strings, settings       |
| `frontdesk_http.py`      | C2    | host_ok (local, lan, docker) over |
|                          |       | a Host corpus, seat_url,          |
|                          |       | over_network, plain_error on both |
|                          |       | mint implementations' stderr,     |
|                          |       | mint validation (inputs.py,       |
|                          |       | refill_vb, misc_vb, game_vb)      |
| `frontdesk_gate.py`      | C2    | Gate traces (window_vb,           |
|                          |       | window2_vb, clock_vb, clock2_vb,  |
|                          |       | lock order), banners, flag errors |
| `frontdesk_games.py`     | C2    | GameSeats over odd rules and db   |
| `office_play.py`         | C3    | post_quests (5 days, 2 seeds),    |
|                          |       | single-bot traces (scripted chat: |
|                          |       | every body, sleep and row),       |
|                          |       | wait_for_day decisions            |
| `office_score.py`        | C3    | score_day over db snapshots and   |
|                          |       | spend rows; board, --day, --seat, |
|                          |       | day_summary texts on the          |
|                          |       | reference weeks and new captures; |
|                          |       | every die message; money,         |
|                          |       | seconds, clock, rank_corr         |
| `fakeollama.py`          | C3    | shapes and token counts           |

### 16.3 Layer 3: process behaviour (CI, three systems)

Start-up messages, exit codes, `--help`, missing seats.env, missing
proxy folder, busy port, `FRONTDESK_PASSWORD` rules, both banners,
Ctrl+C (Linux and macOS), SIGTERM; `lvp game` flows with the fake
mint-key and fakedocker (setup, setup twice, run with "no office
yet", "the week is over", reset with a failing revoke, reset with an
open office.db on Windows); `lvp mint-key` against go-port's tests.

### 16.4 Layer 4: the twin harness (C4, `tests/parity`, CI)

Python and Go side by side on two ports, the same fixtures: one
`fakelitellm`, `fakedocker` first on PATH (host mode; Python's
mint-key.sh uses bash, curl and jq against the fake), a seeded
Postgres service (`--docker`, Python with psycopg and the shim
`mint-key` on PATH). Device addresses from loopback aliases
(127.0.0.x). Modes: local, `--login`, `--lan`, `--docker`.

Cases (about 200 requests, many on raw sockets):

1. Every route x method (GET, HEAD, POST, PUT, DELETE, PATCH,
   `OPTIONS /`, `OPTIONS *`) for the five static paths, the three
   API GETs, the four POSTs and an unknown path.
2. Host corpus: the three local forms, wrong ports, names, IPv4 and
   IPv6 (brackets, zones, leading zeros), userinfo, trailing dot,
   upper case, empty, absent on HTTP/1.0.
3. Origin: none, same, other, `null`.
4. Bodies: content types, lengths (none, negative, 8192, 8193, text),
   BOM, invalid UTF-8, UTF-16, NaN, 2,000-deep nesting, array,
   string, number.
5. Login: right; five wrong (messages, 401s, the lock); the sixth
   (429, Retry-After); slow-down between devices (real 1 s gaps); a
   fresh device let in; 200 and 201 code points; not a string; a
   lone surrogate.
6. Sessions: a cookie from another address; logout; 101 logins.
7. CSRF: missing, wrong, right.
8. Static and state: If-None-Match, Accept-Encoding (`gzip`, none,
   `x-gzip`, `GZIP`), 304s, HEAD.
9. SSE: the prelude bytes (no Transfer-Encoding header, no chunks),
   the first `changed`, a mint gives a new `changed`, a tick after
   15 s, logout ends the stream within 1 s, the 101st stream 503.
10. Mint and revoke: success; every validation error; game prefix and
    roster names; `Sam` and `sam`; proxy down (503); LiteLLM's
    already-exists, 401, 404, no answer; change-me; revoke by seat,
    by id with no seat, unknown, gone.
11. Cards and tally: fixture key lists; database tally; psql failing
    (fallback and `why`); docker missing from PATH.
12. Notes: proxy refused, slow, unknown host, garbage, 401, 500.
13. seats.env edited while running (host): `reread seats.env`, new
    defaults.
14. Caps: 33 connections from one alias (`--lan`, `--docker`), 257 in
    all.
15. fake-ollama: the same requests to both; status, type, field sets
    and types, token counts, stream line counts.

Compared: status; `Content-Type`, `Cache-Control`, `Vary`,
`Content-Encoding`, `Connection`, `Retry-After`, `Server`, the four
safety headers, ETag presence, `Set-Cookie` attributes (value
masked); bodies as parsed JSON with masks (csrf, cookie, ids mapped
by (seat, created, ends), ETags, times from the fakes) or bytes for
static files; SSE event sequences; stderr lines with times cut.
Every difference must be listed in `tests/parity/expected.json`
(case name to ledger IDs), or the run fails.

**Page contract** (gap 10; `tests/parity/page_test.go`, no browser):
a table of every field `app.js` reads from `/api/session`,
`/api/state`, the SSE data, mint, revoke and error answers, with its
JSON type; checked against Go's answers in all four modes. The test
also scans `app.js` for property reads on the variables that hold
server JSON (`s`, `state`, `d`, `c`, `r`, `t`, `data`, and
`JSON.parse(e.data)`), with a short allow-list for the browser names
they share (`r.ok`, `r.status`, `r.json`), and fails on a name
missing from the table.

### 16.5 Layer 5: real stack (C4, `tests/e2e`, by hand or manual job)

- **Live twin**: both Front Desks on the real proxy and database;
  `/api/state` equal at quiet points (cards mapped); a mint on one
  shows on the other within 10 s and on itself at once; both images
  side by side (`--docker`), same checks. The run records the real
  LiteLLM answers for every route in 7.9 into
  `tests/e2e/live/captures/`, and `tests/e2e/live/fake_test.go`
  checks `fakelitellm` against them (status, `error.type`, field
  names); a mismatch goes back to C1's owner of the fake.
- **Security suite**: the reference scripts ported to Go tests (build
  tag `e2e`), first run against Python to show each port reproduces
  its recorded output, then against Go: Host checks, cookie flags,
  CSRF and Origin matrix, other-device cookie, body limits, scrubbed
  mint errors (a leaky fake), the password absent from the psql
  child's environment (`sec_vb`, `sec_fix`); 32 per device, 256 in
  all, others still served, slots given back (`cap_vb`, `cap`);
  lock, slow-down, flood, a fresh device always in (`lock_vb`,
  `lock`, `lock2`, `typo_vb`); odd or reset requests write no log
  line, log lines per second bounded under a flood (`fuzz_vb`,
  `rst_vb`, `rst2_vb`, `nest_vb`, `surr_vb`, `logflood`); budget
  strings, headers, SSE slots after resets (`misc_vb`); mint inputs
  (`refill_vb`, `game_vb`, `inputs`); odd Host headers (`rawhost`).
- **Secret sweep**: every response body and header, every SSE byte
  and every log line of every scenario searched for the master key,
  the salt key, the database password, `FRONTDESK_PASSWORD`, and each
  minted key after its one showing. Zero hits.
- **Browser** (required, gap 10): the reference Playwright scripts
  (`race*.js`, `closerace.js`, `restart_fix.js`, `lan_restart.js`,
  `down_mint.js`, `fallback.js`, `keepit*.js`, `xss*.js`,
  `tally_xss.js`, `evil*_vb.js`, `text_vb.js`) pointed at Go, in
  host mode and `--docker`: same results as recorded for Python.
- **Game**: a 5-day practice week with Go only (each day `spend logs:
  N of N rows`, day start within 5 s of the boundary, 422 refusals
  for the spending personas, the re-mint path with the reset job
  slowed down); cross-play both ways (Python setup, Go run 2 days,
  Python board equals Go board byte for byte; Go setup, Python run,
  Go board equals Python board); post_quests equal for one seed;
  `tests/golden/rescore.py` (C4) scores a Go-played day with Python's
  `score_day` over the same spend rows: scores equal.
- **mint-parity**: `tests/mint-parity.sh ./mint-key` (the shim) and
  with a two-line wrapper for `lvp mint-key`; its Python "callers"
  block gains a Go twin, `go run ./tests/parity/callers`, driving
  `web.plain` and the game's minter with both implementations.

### 16.6 Layer 6: performance A/B (C4)

The reference perf scripts ported to Go (`tests/e2e/perf`), Python
and Go on the same machine in one session, 200 seats and a year of
history (`seed.py`'s data), chat traffic. Each Go number at most the
Python number plus 10%, or better (Appendix A). Results in
`tests/e2e/perf-results.md`; C5 copies them into gui/README.

### 16.7 Layer 7: hand runs (C4 writes `tests/e2e/MANUAL.md`)

On a Windows 11 PC and an Apple silicon Mac, both with Docker
Desktop, and on Linux amd64 (and an arm64 board if one is at hand),
from the release archive: unpack, Gatekeeper and SmartScreen clicks,
the README path as it stands at the time, `lvp desk` and `--lan`
from a phone, the firewall prompt, Ctrl+C, `--profile frontdesk`,
practice mode, a path with a space, a CRLF seats.env, two phones on
the per-device lock, `reset` while `run` holds office.db.

## 17. Gates, and the checks before any Python file is deleted

### Gate A: Go is ready (C4 reports in `tests/e2e/gate-a.md`)

Every check passes, with the evidence linked:

- **G-1** CI green: tests on the five runners, both Go versions;
  `-race`; gofmt; vet; archtest; govulncheck clean.
- **G-2** Every golden suite in 16.2 passes; none skipped.
- **G-3** Twin harness: no unlisted difference in three consecutive
  runs, in all four modes.
- **G-4** Security suite against Go reproduces the recorded Python
  outputs (`cap4163.out`, `lock4163.out`, `xss4163.out` and the
  rest), each change ledgered.
- **G-5** Secret sweep: zero hits.
- **G-6** Page contract passes; every browser script passes against
  Go in host mode and `--docker`.
- **G-7** Every Appendix A target met.
- **G-8** Game: the Go week, both cross-plays byte-identical, equal
  quests, equal bot traces, equal rescore.
- **G-9** Image: amd64 and arm64 build; user 10001; read-only root;
  under 20 MB; no secrets in context or image; `--profile frontdesk
  up -d` works; `docker compose run --rm frontdesk mint-key <seat>`
  works; practice mode runs on the image's fake-ollama;
  `FRONTDESK_PASSWORD` empty in the proxy and db containers; on
  Linux the container cannot read `game/keys/*.key`.
- **G-10** Release dry run on a fork tag: five archives, notices,
  `SHA256SUMS`, same sums on a second pack, universal macOS binary,
  `lvp version` and `lvp --help` run from each unpacked archive on
  each system's runner.
- **G-11** `tests/mint-parity.sh` green against the shim and `lvp
  mint-key` on a live stack.
- **G-12** The hand runs in MANUAL.md done and written up.
- **G-13** Ledger complete: every difference any layer found is in
  `tests/parity/ledger/*.md` with its test.

Then the user reads the ledger and `gate-a.md` and signs off parity.
That sign-off unlocks C5.

### Gate B: docs switched (C5)

- Helpers done and tested (20, C5).
- README.md, gui/README.md, game/README.md point to `lvp` only; the
  Python files are listed as "the old version, removed soon".
- No beginner path uses Python, bash, curl, jq, openssl, sed or git
  (git and Go only under "Build from source"; curl only in the
  optional by-hand boxes and on seats, where Windows ships
  `curl.exe`).
- Every command in the docs was run as written on Windows
  (PowerShell), macOS and Linux (archtest also extracts the fenced
  commands and checks each names a real `lvp` command and flag).
- Width check passes.

### Gate C: removal, only after the user's go-ahead

Preconditions: Gate A signed off; Gate B done; a release with `lvp`
published (the user may ask for one release in use first, question
23.8); the user says go. Then one pull request, in this order:

1. Tag the last commit with Python as `python-final` (the user
   approves the tag push).
2. Delete `gui/frontdesk.py`, `game/office.py`, `game/fake_ollama.py`,
   `gui/requirements-docker.txt`, `tests/golden/*.py`, the Python
   scripts under `tests/reference/` (their Go ports live in
   `tests/e2e/`; the recorded outputs, the JS originals and the
   office.db files stay as test data), `.github/workflows/parity.yml`;
   `mint-key.sh` only if the user chose so (question 23.5).
3. Edit `.github/workflows/ci.yml` (drop the Python steps),
   `.gitignore` (drop `__pycache__/`), `gui/static/app.js` (drop the
   Python fallback and reword "Talks only to frontdesk.py"), the
   docs' file maps.
4. Checks that must pass on that pull request: CI green with no
   Python installed; `git grep -n -i python` finds only history
   notes; every golden suite and the security, perf and game suites
   run against Go alone with the recorded results as expectations;
   `lvp` builds from a clean clone with `go build ./cmd/lvp`.

## 18. Docs changes (C5, after Gate A)

All plain, beginner words, 80 columns, `./lvp` with one line for
`.\lvp`; each step says what success looks like; no model
identifiers; the License section and its wording exactly as on
`main` ("to be decided"; go-port's README still says MIT, so the
merge must keep main's).

**README.md**

- In plain terms: `lvp mint` prints the key cards.
- Quick start: download and unpack, then `lvp init --ollama
  http://<ollama-ip>:11434`, `docker compose up -d`, `lvp wait`, `lvp
  mint laptop --save laptop.key`, `lvp test`; the same in PowerShell;
  "Prefer clicking?" with `lvp desk` and the Docker way.
- Step 1: `docker version` and `docker compose version` only; no WSL
  needed on Windows (Docker Desktop handles its own); how to open a
  terminal in a folder on each system; the Ollama check via `lvp init
  --ollama` (it tries the address) or `curl.exe`; the Ollama setup
  text stays.
- Step 2: which file for which computer (4.1); unpack; check
  `SHA256SUMS` (`sha256sum -c`, `shasum -a 256 -c`, PowerShell
  `Get-FileHash`); the Gatekeeper and SmartScreen clicks; or `git
  clone` and `go build -o . ./cmd/lvp` under "Build from source".
- Step 3: `lvp init` replaces cp, chmod, openssl and sed; "3a. The
  three secrets" explains what init wrote and why they must stay;
  3b: editing in Notepad (`notepad seats.env`), TextEdit (`open -e
  seats.env`) or nano; 3c: `lvp check` replaces the grep line.
- Step 4: `docker compose up -d`; "Is it up?": `lvp wait`, `lvp
  check`; the 401 explanation stays.
- Step 5: `lvp mint laptop --save laptop.key`.
- Step 6: `lvp test`, what each check means; the curl version in a
  short "by hand" box.
- Step 7: unchanged (seats may use curl or a browser).
- Minting more keys, revoking: `lvp mint`, `lvp revoke laptop`, `lvp
  revoke --key-file laptop.key`; the PowerShell `$20` and comma notes;
  "The Go version" folds into "About `lvp mint-key`" (the old form
  for scripts; `mint-key.sh` still works on Linux and macOS until
  Gate C).
- New: "Upgrading" (stop `lvp desk`; unpack over the folder, which
  never holds `seats.env`, keys or `office.db`, but does hold
  `rules.toml` and `config.yaml`, so keep yours aside if you edited
  them; `docker compose up -d --build`; `lvp version`, `lvp check`).
- Troubleshooting: drop curl `--fail-with-body`, `chmod +x
  mint-key.sh` and the jq items; add Gatekeeper, SmartScreen,
  antivirus, `lvp: command not found` (type `./lvp`), PowerShell
  "is not recognized" (type `.\lvp`), Linux "Permission denied"
  (`chmod +x lvp`), the docker group, the firewall prompt with
  `--lan`, "cannot find compose.yaml".
- File map: `lvp`, `mint-key` (this release), `cmd/`, `internal/`
  (one line per package group), `Dockerfile`, `gui/embed.go`,
  `THIRD_PARTY_NOTICES.txt`, the Python files as the old version
  (until Gate C). Glossary: terminal, PowerShell, archive, checksum,
  release.

**gui/README.md**

- "Front Desk is part of `lvp`, one program." Run it in Docker:
  unchanged commands; the image is Go only, about 12 MB; the first
  build downloads about 130 MB once.
- Pick your own password: `lvp init --desk-password` replaces the
  printf and openssl line.
- Let phones in: `lvp env set FRONTDESK_BIND=0.0.0.0` replaces `echo
  ... >> .env`; More settings uses `lvp env set` too.
- Mint keys through Docker: the same `docker compose run --rm
  frontdesk mint-key ...` lines; note the image has no shell.
- "Or run it with Python" becomes "Or run it on this computer":
  `./lvp desk`, `--lan`, `--login`, the password for one run in bash
  and in PowerShell (`$env:FRONTDESK_PASSWORD='...'`, then
  `Remove-Item Env:FRONTDESK_PASSWORD`).
- Key cards: office seats removed with `lvp game reset`.
- Safety notes: the same substance; "Error messages from minting";
  the container has no shell or Python; Front Desk's password reaches
  only its own container; the shared-address note on Docker Desktop
  stays; "it reads only rules.toml and office.db from game/; on Linux
  the 0700 keys folder keeps the keys from it".
- Troubleshooting: drop the Python items; add firewall prompts and
  "cannot find compose.yaml".
- Under the hood: the Go numbers from `perf-results.md`; "Standard
  library only" becomes "One program, three small libraries"; the
  image size.

**game/README.md**

- Try it: `lvp game practice on` (it writes `.env`, restarts the
  proxy and waits), `docker compose ps`, `lvp game setup`, `lvp game
  run --days 2`, `lvp game board`; no Python check; the first `on`
  builds the fake-ollama image (about 130 MB once).
- Back to normal: `lvp game reset`, `lvp game practice off`.
- Privacy: "The game never sees the master key: it runs `lvp
  mint-key` for that" (the property stays true).
- Files: `office.py` becomes `lvp game`; `fake_ollama.py` becomes
  `lvp fake-ollama` in the Front Desk image; `office.db` stays "the
  game's state (SQLite)".
- Troubleshooting: drop "needs Python 3.11"; keep "cannot read the
  proxy database" with the docker-group advice; add the Windows "in
  use" message for `reset`.

**seats.env.example**: the header points at `lvp init` first, the
openssl lines as the by-hand way; everything else unchanged, so old
files and instructions keep working.

## 19. The starting ledger

Format, one entry per ID in `tests/parity/ledger/<area>.md` (base,
frontdesk, game, container, release: one file per owning chunk):

```text
ID      H-1
What    ...
Why     ...
Safe    why it is not weaker
Tests   case names in tests/parity/expected.json or test functions
```

Expected entries (confirm or remove as tests show):

Front Desk core (F), owner C2 unless noted:

- **F-1** seats.env is read by `internal/seats`: `$(...)` and
  backticks refused with the line at start; `$NAME` expanded; inline
  comments cut; names the file leaves out come from the environment.
  Agrees with mint-key (C1).
- **F-2** `PROXY_URL` with a path, without a scheme, or with a bad
  port: handled as mint-key does; a bad one stops at start with a
  message (Python crashed).
- **F-3** Generic mint failure reads `mint-key said:` in host mode
  too; the Go mint-key's own wording (from go-port).
- **F-4** Card ids, ETag values and gzip bytes differ (opaque).
- **F-5** JSON bytes: number spelling (`5` for `5.0`, `0.00001` for
  `1e-05`), spacing, `<>&` escaped, non-ASCII not escaped; parsed
  values equal.
- **F-6** `/api/session` gains `reset_cmd`; the page shows the right
  command for this system.
- **F-7** A `FRONTDESK_PASSWORD` with U+FFFD or invalid UTF-8 is
  refused at start.
- **F-8** A Cookie header with other apps' malformed cookies: Go
  still finds Front Desk's (Python's SimpleCookie gave up: logged
  out).
- **F-9** A card time that cannot be read counts as absent and is
  logged once (Python's check failed; the page froze). Answers of an
  unexpected shape give the `Could not read the key cards` note.
- **F-10** NaN or Infinity in a proxy answer: refused as bad JSON
  (Python passed it on and the page froze); non-finite sums become 0.
- **F-11** Log lines that showed Python's `repr(e)` show the Go
  error text, scrubbed, cut at the same length.
- **F-12** JSON decode errors' wording in the tally's `why`.
- **F-13** A tally query is skipped while one still runs.
- **F-14** Python class names in notes come from a mapping table;
  rare errors may read `OSError`.
- **F-15** `Lower` of letters whose case mapping changed between
  Unicode versions may differ.
- **F-16** Front Desk's GETs ignore `HTTP_PROXY` (as Python);
  minting honours it (as mint-key). Listed to be tested.
- **F-17** Host mode needs the proxy folder (compose.yaml in the
  current folder or next to `lvp`), with a new message.
- **F-18** Flags: no prefix abbreviations, different help layout,
  `--port` 0..65535 (Python crashed above).
- **F-19** The port-in-use message uses Windows' words on Windows.
- **F-20** SIGTERM stops cleanly, as Ctrl+C (Python died at once).
- **F-21** `lvp version`; faster start, less memory.

Front Desk HTTP (H), owner C2:

- **H-1** Requests net/http refuses before any handler (bad request
  line, HTTP/2.0 line, no Host on HTTP/1.1, two Hosts, bad
  Content-Length, control bytes in headers, headers over 1 MiB) get
  Go's plain 400, 431 or 505 without JSON or safety headers.
  Browsers never send them; nothing echoed or logged.
- **H-2** An absolute-form target is checked by its host (421).
- **H-3** A Host with userinfo is 400.
- **H-4** Slow senders get 10 s for the whole request (Python: 10 s
  per read). Stricter.
- **H-5** HTTP/1.0 requests: status line version as net/http writes
  it (only if it differs from Python's `HTTP/1.1`).
- **H-6** UTF-16 and UTF-32 bodies, `NaN` and `Infinity` are refused
  (`Send valid JSON.`); deep nesting Python refused may reach the
  next check (still a 400, never a counted wrong password). Stricter.
- **H-7** 304 answers carry the headers net/http allows (only if C2
  finds a difference).

Game (O), owner C3:

- **O-1** A missing or wrongly typed rules key: one line with the
  key and line, exit 1 (Python: traceback); unknown keys warn.
- **O-2** Mint and revoke errors in the Go mint-key's words.
- **O-3** A chat answer cut short: kind `IncompleteRead` (Python's
  bot thread crashed; its daylog row was still written).
- **O-4** Bots still working 70 s after the deadline are stopped and
  write nothing more.
- **O-5** Key files are owner-only by access list on Windows.
- **O-6** Calls to the proxy on localhost never use `HTTP_PROXY`.
- **O-7** docker missing: `cannot read the proxy database: cannot run
  docker: ...` (Python: traceback).
- **O-8** Renamed messages (11.9).
- **O-9** Flags: no prefix abbreviations; help layout.
- **O-10** Chat request JSON spacing and escaping.
- **O-11** `reset` on Windows while office.db is held open: retries 2
  s, then a message.
- **O-12** TOML: error wording; some TOML 1.1 forms `tomllib` refuses
  are read.

fake-ollama (P), owner C3:

- **P-1** Keep-alive (HTTP/1.1); JSON spacing.

Container (C), owner C4:

- **C-1** Database driver wording in the tally's `why`.
- **C-2** SIGTERM stops cleanly.
- **C-3** No shell or Python in the image; `docker compose run --rm
  frontdesk mint-key ...` still works.
- **C-4** Practice mode builds fake-ollama from the Dockerfile (about
  130 MB once) instead of pulling `python:3.12-slim`.
- **C-5** `FRONTDESK_PASSWORD` is empty in the proxy and db
  containers (a fix).

Kept equal on purpose, though Go would differ by default: invalid
UTF-8 and BOM bodies, Python class names in notes, Python's C library
words for port and docker errors, banker's rounding, the `0:60` that
`02.0f` can print, `OPTIONS *` as 501 JSON, explicit Content-Length.

## 20. Work breakdown: five chunks

Rules for every chunk: own only the files listed; anything else needs
the lead's OK. Only C1 edits `go.mod` and `go.sum`; CI fails on `go
mod tidy` changes. Each stub C1 writes starts with `// Owner: Cn`.
Golden scripts belong to the chunk whose tests read them, so no two
chunks write the same testdata. A chunk is done when its tests pass
on the CI runners (Linux, macOS, Windows; `-race` on Linux) and its
done-when holds. Commits follow the repo's attribution rules; nothing
is pushed or tagged without the user's say-so.

```text
go-port merged into main, py-reference tag
        |
       C1 base ----- M1 (interfaces frozen, stubs) ------+
        |              |              |                  |
        |             C2 front desk  C3 game            C4 ship
        |              |              |                 and prove
        +--------------+--------------+------ done -----+
                                                        |
                                    Gate A sign-off (the user)
                                                        |
                                C5 beginner path, docs, retirement
                                   (Gate B, then Gate C on go-ahead)
```

C2, C3 and C4 start at M1 and finish after C1 is done. Up to four
agents work at once after M1.

### C1 base

- **Owns**: `go.mod`, `go.sum`, `.gitignore`, `.gitattributes`,
  `.github/workflows/ci.yml`; `cmd/lvp/`, `cmd/mint-key/`;
  `internal/cli/`, `args/`, `kit/`, `clock/`, `fsx/`, `pyfmt/`,
  `pyrand/`, `seats/`, `litellm/` (with `fakelitellm/`), `mintkey/`,
  `pgread/` (with `fakedocker/`), `rules/`, `gamestate/`,
  `archtest/`, `notices/`; `gui/embed.go`; the M1 stubs
  `internal/frontdesk/frontdesk.go` (then C2),
  `internal/game/game.go` and `internal/fakeollama/fakeollama.go`
  (then C3), `internal/setup/setup.go` (then C5);
  `tests/reference/`, `tests/golden/README.md`, `common.py`,
  `pyfmt.py`, `pyrand.py`, `rules.py`, `psql.py`;
  `tests/parity/ledger/base.md`.
- **Needs**: go-port merged; the `py-reference` tag.
- **Step 0**: re-read the merged contract (section 1); import the
  reference material (16.0).
- **M1 when**: every signature in section 7 compiles; the stubs are
  in place; `lvp help` and `lvp version` run; archtest's import rules
  pass; CI green on all runners.
- **Done when**: go-port's mint-key tests pass unchanged against
  `internal/mintkey`, and the shim's output equals `lvp mint-key`'s;
  `tests/mint-parity.sh ./mint-key` passes on a live stack; pyfmt,
  pyrand, rules and psql goldens pass; Session and Once pass their
  protocol tests against fakedocker on three systems, Direct refuses
  a write against Postgres 16 (`pg` job); gamestate opens the
  reference office.db files read only and read-write, and Python's
  `board` reads a Go-made file; fsx retry and access-list tests pass
  on Windows; fakelitellm answers every route in 7.9 with the
  captured bodies; the width, model-id and reference scans pass;
  release builds of `lvp` and `mint-key` for six targets.

### C2 front desk

- **Owns**: `internal/frontdesk/` (all, from the M1 stub on);
  `gui/static/app.js` (only the reset lines of 9.12, until Gate C);
  `tests/golden/frontdesk_*.py`; `tests/parity/ledger/frontdesk.md`.
- **Needs**: C1 M1 to start; C1 done to finish.
- **Done when**: every frontdesk golden passes; unit tests cover
  every rule in section 9 (routes and methods, headers, body rules,
  Host tables in three modes, caps with real sockets, SSE bytes with
  no chunking and hang-up ending, lockout and session traces on a
  fake clock, mint and revoke messages, banners byte for byte, flags,
  start errors); an end-to-end test runs `lvp desk` against
  fakelitellm and fakedocker (host mode, all three host modes) and
  against Postgres (`pg` tag, `--docker`); Ctrl+C and SIGTERM stop
  psql; the secret sweep over its own tests finds nothing; every
  difference found is in its ledger file.

### C3 game

- **Owns**: `internal/game/` (all, from the M1 stub on),
  `internal/fakeollama/` (all); `tests/golden/office_*.py`,
  `tests/golden/fakeollama.py`; `tests/parity/ledger/game.md`.
- **Needs**: C1 M1 to start; C1 done to finish.
- **Done when**: quest, bot-trace, score and board goldens pass byte
  for byte; the day loop is tested on a fake clock (fresh start,
  waiting, late reset job and re-mint, Ctrl+C at each stage with the
  STOPPED texts); the minter works through a child `lvp mint-key`
  (and, on Linux, through `mint-key.sh`) with fakelitellm; a practice
  day runs end to end against fakelitellm and fakedocker with a
  2-second day; reset with an open office.db is tested on Windows;
  fake-ollama shape and count goldens pass; every difference found is
  in its ledger file.

### C4 ship and prove

- **Owns**: `Dockerfile`, `.dockerignore`, `compose.yaml`,
  `game/compose.game.yaml`; deletes `gui/Dockerfile`;
  `.github/workflows/release.yml`, `.github/workflows/parity.yml`;
  `tests/parity/` (harness, `expected.json`, `page_test.go`,
  `callers/`, `ledger/README.md`, `ledger/container.md`,
  `ledger/release.md`; not the other ledger files);
  `tests/e2e/` (security, perf, browser, game, live, `MANUAL.md`,
  `perf-results.md`, `gate-a.md`); `tests/golden/rescore.py`;
  `tests/mint-parity.sh`.
- **Needs**: C1 M1 to start (image, compose, release, harness
  skeleton); C2 and C3 done to finish.
- **Done when**: G-1 to G-13 hold and `tests/e2e/gate-a.md` shows
  each with its evidence; the user is asked to sign off parity.

### C5 beginner path, docs and retirement

- **Owns**: `internal/setup/` (all, from the M1 stub on),
  `seats.env.example`, `README.md`, `gui/README.md`,
  `game/README.md`. At Gate C it takes over, for the deletion pull
  request only: the files listed in Gate C steps 2 and 3
  (`.github/workflows/ci.yml`, `.github/workflows/parity.yml`,
  `.gitignore`, `gui/static/app.js`, `gui/requirements-docker.txt`,
  the Python files, `tests/golden/*.py`, the Python scripts under
  `tests/reference/`), once their first owners are done.
- **Needs**: the user's Gate A sign-off; C1, C2, C3, C4 done. Docs
  may be drafted from M1 on a branch, merged only after sign-off.
- **The helpers**, each with tests against fakelitellm, fakedocker
  and temp folders:
  - `init [--ollama URL] [--desk-password] [--force]`: makes
    `seats.env` from the folder's `seats.env.example` if missing
    (private); fills only placeholders, exactly README 3a's three
    `sed` rules in their order (empty `LITELLM_MASTER_KEY=` gets
    `sk-` + 48 hex; `LITELLM_SALT_KEY=sk-change-me` gets `sk-` + 48
    hex; every other `change-me` one 32-hex password); a second run
    changes nothing; `--ollama` sets `OLLAMA_API_BASE` and tries it
    (warns unless `Ollama is running` within 5 s);
    `--desk-password` adds `FRONTDESK_PASSWORD=<24 hex>` if absent;
    refuses to set a new database password when the
    `local-verse-proxy_pgdata` volume already exists, unless
    `--force`, saying why; keeps CRLF and BOM; never prints a secret;
    ends with the next command.
  - `check`: proxy folder; seats.env present, filled (the grep rule),
    private (mode or access list); docker runs; `docker compose ps`
    shows db healthy and proxy running; the proxy answers and accepts
    the master key; `OLLAMA_API_BASE` answers (a warning only, and
    not in practice mode). Each problem with its fix and README
    section; exit 1 if any check fails.
  - `wait [--timeout 3m]`: liveliness every 3 s, a dot each time,
    then `The proxy is up.`, or the "Is it up?" advice, exit 1.
  - `test`: README Step 6's three checks with the same requests and
    expected codes, on a throwaway seat `test-<6 hex>`, always
    revoked; PASS or FAIL per check with the reason.
  - `mint <seat> [...] [--save FILE]`: `mintkey.Exec` with
    mint-key's options; `--save` refuses an existing file, writes
    key + "\n" private, prints where; without it, the key only.
  - `revoke <seat> | --key-file FILE | --key KEY`.
  - `env [set NAME=value | unset NAME]`: `.env` (Compose's file,
    not seats.env), UTF-8 without BOM, other lines kept, one line
    per name; prints the line and the `up` command to run.
  - `game practice on|off`: on adds `# lvp game practice on`,
    `COMPOSE_PATH_SEPARATOR=:` and
    `COMPOSE_FILE=compose.yaml:game/compose.game.yaml` to `.env`
    (refuses if `COMPOSE_FILE` is already set to something else),
    runs `docker compose up -d --force-recreate proxy`, waits as
    `wait`, runs `docker compose ps`; off removes exactly those lines
    and runs `docker compose up -d --force-recreate --remove-orphans
    proxy`.
  - `setup.Ready = true`, so `lvp help` lists them.
- **Done when**: the helpers' tests pass on three systems; Gate B
  holds; after the user's go-ahead, Gate C's pull request passes
  every check in Gate C step 4.

## 21. Risks

1. **The `--docker` mode is still moving** on go-port. Mitigation: C1
   re-reads the merged code first; the twin harness runs both
   `--docker` servers against each other.
2. **The go-port merge** carries README text from before main's
   "license to be decided" commit. Mitigation: keep main's wording;
   C1 checks it after the merge.
3. **Docker Desktop shows every device as one address** to the
   container: per-device lock, cap and session binding act on all at
   once. Inherited from Python `--docker` and already in gui/README;
   kept, measured in the hand runs.
4. **Byte-exact Python output** (rounding, specs, dice, row order) is
   easy to get almost right. Mitigation: whole-output goldens, wide
   corpora, cross-play.
5. **Bot timing is not deterministic**: proofs rest on single-bot
   traces, quests, scores from fixed rows, and cross-play boards.
6. **Timing tests flake** (lock gaps, ticks): fake clocks in units;
   margins and retries only in e2e.
7. **Unsigned binaries** warn on macOS and Windows; this hurts
   saleability until signed (question 23.3).
8. **Local image builds** cost about 130 MB and a minute or two, also
   for practice mode. Published images would avoid it (question
   23.2).
9. **SQLite over Docker Desktop file sharing**: locks may not cross
   into the container. Front Desk only reads and retries; worst case,
   a stale game-seat list for a few seconds.
10. **Game keys on Docker Desktop**: the 0700 folder protects them
    from the container on Linux only. Front Desk never reads them;
    the docs say it plainly.
11. **Answer size**: `litellm` reads 1 MiB by default; Front Desk
    uses 16 MiB (decision 32); a larger answer gives the note.
12. **LiteLLM drift**: the fakes copy v1.106.6; a `LITELLM_TAG` change
    needs the live checks again, as the README already says.
13. **Reference material** may hold sandbox ports, paths or printed
    secrets: the scan (16.0) runs on every CI build.
14. **Go 1.26 for contributors**: the go command downloads the
    toolchain itself; the docs say so under "Build from source".
15. **Build time**: SQLite makes a cold build about 30 s per target.
    Accepted.

## 22. The judge's gaps, and where each is closed

1. SSE without hijack or chunking: decision 9, 9.11 (verified again
   for this spec, Appendix B).
2. Secrets spread across containers: decision 27, 14.3, G-9, C-5.
3. Native Windows file locking: decision 29, 13, 11.7 (`reset`),
   O-11, C1 and C3 done-when.
4. Command text by OS: decision 28, 9.12, 7.1 `Prog`, 11.9, F-6.
5. Reference material in a scratchpad: 16.0, C1 step 0.
6. arm64 not checked: verified in 13; re-checked by release.yml.
7. Windows file permissions: 7.4 `Private`, 13, `lvp check`, O-5.
8. Leftover openssl, sed and bash in the docs: C5 helpers, 18, Gate
   B.
9. Model id in shipped files: question 23.4; no new file names one;
   archtest checks it.
10. Python/Go page parity: the page contract (16.4) and the browser
    scripts (16.5), both required by G-6.

## 23. Questions for the user

1. The program's name: `lvp`, or another? (One constant, the
   Dockerfile and the docs.)
2. Publish the image for amd64 and arm64 to GitHub's registry with
   each release, so nobody builds it locally (practice mode too)?
3. Sign and notarise the macOS and Windows programs (paid accounts),
   or only document the warnings?
4. `config.yaml` names a hosted model and a local model; every
   archive would ship it as it is. Ship it so, or with placeholders
   that `lvp init` fills?
5. `mint-key.sh`: keep it for good, or delete it at Gate C?
6. Approve the `py-reference` and `python-final` tags.
7. Drop the separate `mint-key` program in the release after this
   one?
8. Gate C: right after a release with `lvp`, or after one release in
   use?
9. Later, outside the port: a read-only Postgres role for Front
   Desk's `--docker` mode instead of the `litellm` owner account.

---

## Appendix A: performance baseline and targets

Python numbers from the earlier rounds (200 seats, a year of history,
chat traffic; `tests/reference/perf`). Target: Go at most the Python
number plus 10% noise, measured A/B in one session.

| Measure                                | Python          | Go target  |
|----------------------------------------|-----------------|------------|
| Start to first page                    | about 0.1 s     | <= 0.1 s   |
| Memory, idle                           | about 30 MB     | <= 30 MB   |
| Memory, 20 open pages                  | 31-35 MB        | <= 35 MB   |
| Page load, 6 GETs, 1 viewer, p50       | 2.0-3.1 ms      | <=         |
| Page load, 20 viewers, p50             | 30-40 ms        | <=         |
| `/api/state` 200 gzip, 1 viewer, p50   | 0.19-0.25 ms    | <=         |
| `/api/state` 200 gzip, 20 viewers, p50 | 3.9-4.6 ms      | <=         |
| `/api/state` 304, 20 viewers, p50      | 3.2-3.9 ms      | <=         |
| Mint, p50, no load / 20 viewers        | 57-76 / 75-92ms | <=         |
| Cancel, p50, no load / 20 viewers      | 42-55 / 56-72ms | <=         |
| Page wrong right after mint or cancel  | 0 of 40         | 0 of 40    |
| Live stream per page, traffic          | 5.5 MB/h down   | <=         |
| Live stream per page, idle             | 0.01 MB/h       | <=         |
| Front Desk CPU, 20 pages, traffic      | 0.6% of a core  | <=         |
| Tally today, p50                       | 4-6 ms          | same SQL   |
| History, every 10 min                  | 150-170 ms      | same SQL   |
| psql start, once                       | 130-170 ms      | same       |
| Whole check, p50                       | about 8 ms      | <=         |
| Watching cost, Postgres and LiteLLM    | 0.9%, 1.1%      | <=         |
| Static files zipped, total             | 18,520 bytes    | 18,553     |
|                                        |                 | (measured) |
| `/api/state` zipped, 200 cards         | about 11 KB     | <= +3%     |
| Keep-alive requests a second           | about 10,000    | >=         |
| 50 page loads at once, worst           | no SYN stall    | no stall   |
| Login flood: 200 wrong, 40 devices     | 40 checked,     | same       |
|                                        | 160 refused     |            |
| Legit user during guessing             | in at once      | same       |
| One device, 400 stalled connections    | 32 held; 20/20  | same       |
|                                        | others served   |            |
| 8 devices x 40 stalled                 | 256 held; 9th   | same       |
|                                        | refused         |            |
| Busy redraw on a slow phone            | about 50 ms     | page as is |

The maintainer's Go prototype of the serving core already measured:
ready in 6.8 ms, 34,500 keep-alive requests a second with 8 clients,
50 page loads at once worst 6 ms, 14 MB resident.

## Appendix B: facts verified for this spec

All throwaway, under `port-design/exp-spec/` and `exp-judge/`.

- **SSE framing** (go1.24.7): with `Transfer-Encoding: identity`, no
  `Transfer-Encoding` header goes out, the body is not chunked, every
  event arrives, and the stream ends on close. With ReadTimeout 1 s
  the stream ran its full 2.5 s.
- **Hang-up** (go1.24.7, ReadTimeout 10 s, identity, a 10 s write
  deadline): the client closing the stream cancelled `r.Context()`
  0.3 s later, at once.
- **Pin set** (go1.26.10, `CGO_ENABLED=0 -trimpath -s -w`, toml
  v1.6.0, pgconn v5.11.0, sqlite v1.60.1, x/sys v0.48.0 on Windows):
  linux-amd64 10.52 MB (32 s cold), linux-arm64 10.03 MB, darwin-amd64
  10.51 MB, darwin-arm64 10.06 MB, windows-amd64 10.58 MB,
  windows-arm64 9.90 MB. SQLite answered `select 1`; pgconn parsed
  the compose `DATABASE_URL` form.
- **Module downloads** for an image build: sqlite 23.2 MB, libc 19.0
  MB, x/text 9.2 MB, x/sys 2.1 MB, pgx 0.8 MB; with the Go builder
  image, about 130 MB once.
- **Module dates** (proxy.golang.org): pgx v5.11.0 2026-09-07, v5.12.0
  today; sqlite v1.60.1 2026-09-29, v1.61.0 2026-10-21, v1.61.1
  today; toml v1.6.0 2025-12-18; x/sys v0.48.0 2026-08-31. Toolchains
  available: go1.26.10, go1.27.3.
- **Images**: `ghcr.io/berriai/litellm:v1.106.6` index lists
  linux/arm64 and linux/amd64; `postgres:16-alpine` lists arm64 (and
  more); `golang:1.27-alpine` and `golang:1.26-alpine` exist.
- From the plans and the judge, re-read for this spec: Go's JSON
  turns `"\ud800"` into U+FFFD; Python's `json.loads(bytes)` accepts
  a UTF-8 BOM and UTF-16; `tcp` on 0.0.0.0 is reachable over `::1`,
  `tcp4` is not; `litellm.Client` reads at most 1 MiB; Go answers
  `OPTIONS *` with 200 unless `DisableGeneralOptionsHandler`; a Go
  Mersenne Twister seeded like Python matched CPython for `random`,
  `choice`, `choices`, `randint` and `uniform`; Go's gzip of the
  static files is 18,553 bytes against Python's 18,520.

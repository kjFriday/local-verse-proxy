# Ledger: base (chunk C1)

Differences between the Go port's shared packages and the Python they
stand for (or go-port's `mint-key`), each with why, why it is not
weaker, and the test that shows it. Format and rules: spec section 19.
Where section 19 already gave an ID for something C1's code does
(O-1, O-7, O-12, C-1), the entry keeps that ID and lives here; the
chunks that use the package point to it rather than repeat it.

## Settled: go.mod's Go and the files that named Go 1.24

C1 raises go.mod to `go 1.26.0` (spec section 6: sqlite v1.60.1
needs it). The lead took way (a): with C1, `gui/Dockerfile` builds
mint-key with golang:1.26.10 on Alpine 3.24 (pinned by digest) and
copies go.sum, `.dockerignore` lets go.sum in, release.yml uses Go
1.26.10, and README.md says "Go 1.26 or newer". The image's Go stage
was built and its mint-key run here; its pip stage could not reach
PyPI from this sandbox, so C4's image job (15.2) proves the whole
image.

## Still open after C1 (for C2-C5)

- fsx on Windows, private=false: a target whose access list is not
  protected but has explicit entries of its own (an icacls /deny)
  loses them on a rewrite; only inherited entries come back. Fix:
  copy the list whenever it has an explicit entry, or use
  ReplaceFileW; test with icacls /deny on a Windows runner.
- O-1 leaves out a few measured shapes. Python plays with
  `roster.seats = ""` (no seats) where Go refuses it; and a
  non-text seat_prefix or seat name, non-empty text roster.seats and
  a missing table crash Python at load. C3 adds them to O-1 and to
  tests/golden/rules.py.
- TestDirectKnownExposures does not yet call pg_read_file; the
  package doc lists it. A read-only role (question 23.9) closes all.

## Step 0: the merged contract, checked against spec section 10

Re-read on `main` at `py-reference` (c7d7098): `gui/frontdesk.py`,
`compose.yaml`, `gui/Dockerfile`, `gui/README.md`, `README.md`. The
table in section 10 holds. These points it leaves out, or gets
slightly wrong; the code is the oracle, so C2 follows the code:

1. **Session `lan` in `--docker`** is `args.lan or over_network()`:
   with `--docker --lan` it is always true. The table says
   `over_network()` only.
2. **401 from the proxy in `--docker`** reads `MASTER_REFUSED_DOCKER`:
   "Front Desk and the proxy have different master keys. If seats.env
   changed, run: docker compose --profile frontdesk up -d", in the
   desk's notes (`Proxy.json`) and in `plain_error`. Section 9.2 names
   only `MASTER_REFUSED`.
3. **`plain_error` has two matches section 9.10 leaves out**, between
   the 404 and the "Failed to connect" checks: "may have made the key
   anyway" (the proxy took too long; the card may exist) and "may have
   revoked it anyway". Both come from the Go mint-key's late-answer
   text, which `mintkey` keeps.
4. **`Pg` (the `--docker` tally) waits 3 s after the first failure,
   then 30 s** while failures go on, and a question that fails on a
   connection kept from before is tried once more on a new one
   (`Stale`). Section 9.5 says 30 s. `pgread.Direct` does as `Pg`.
5. **`Pg` asks through psycopg with `prepare=True`** (Postgres's
   extended protocol: one statement per question) inside
   `conn.transaction()` on a `read_only` connection, not as one
   simple-protocol text `begin read only;\n<sql>\ncommit;` as section
   9.5 says. The simple form would run `select 1; commit; set
   default_transaction_read_only = off; insert ...`. `pgread.Direct`
   does as `Pg`, and more (B-2).
6. **Minting in `--docker`** ran the Go mint-key with the environment
   cut to `MINT_ENV`, which has no `HTTPS_PROXY`, `HTTP_PROXY` or
   `NO_PROXY`: it never used a web proxy, whatever Docker set.
   `mintkey.Exec` does the same from the `seats.File` it is given (B-1).
   That child also read `SEATS_ENV=/app/seats.env` (the image's empty
   stand-in) with the cut environment behind it, so its messages name
   `/app/seats.env`, as gui/README.md tells readers to expect. For the
   same words, C2 builds the File with `seats.Load(SEATS_ENV, <lookup
   of the cut environment>)`; `seats.FromEnviron` has Path "".
7. **The seat URL** brackets an IPv6 host (`http://[::1]:4000`); in
   `--docker`, `--seat-url` wins over `FRONTDESK_SEAT_URL` (section
   9.13 says so; the table's order reads the other way).
8. In the image Front Desk is `/app/gui/frontdesk.py`, so its `REPO`
   is `/app`, the game folder `/app/game` (the read-only mount), and
   mint-key runs in `/app`. As the table says.
9. **The playtest files in tests/reference/game are older than the
   schema**: their `daylog` has no `kind` column (office.py added it
   after). Reading them works in both languages (gamestate's tests);
   a `run` that continued such a week would fail on the daylog insert,
   in Python as in Go. C3's cross-play must start from a new setup.

## Additions to section 7 at M1

Only additions; nothing in section 7 changed. Unexported details are
not listed.

- `clock.(*Fake).Waiting() int`: how many sleeps wait on the fake, so
  a test knows a goroutine has reached its sleep before `Advance`.
- `fakelitellm.Options.CostPerToken` (spend per token a chat call
  adds; 0 is $0.000001) and `Options.Now` (the fake's clock).
- `mintkey.Usage() string`: mint-key's usage, for `lvp help mint-key`.
- `gamestate.Row.Columns() []string`.
- `pyfmt.PortError` (URLSplitHost's error when only the port is bad,
  as Python's `.port` raises where `.hostname` works) and
  `pyfmt.SpaceClass()` (Space as a regexp class).
- `pgread.SessionConfig`: `App` "" means `frontdesk`, `Clock` nil
  the real clock.

Changed by the C1 fix round, before C2 and C3 start (no signature
changed; what a caller sees, listed for C2, C3 and C5):

- `litellm.Client.Raw`: an answer longer than MaxAnswer now gives
  `*litellm.TooBigError` ("the proxy's answer is over N bytes"), with
  the first MaxAnswer bytes in body. It was cut silently. C2's
  `/key/list` read must treat it as a failed read.
- `litellm`: the master key is scrubbed before an answer is cut to
  300 characters, and a cut that ends in the key's first characters
  hides them too; a redirect's Location shows scheme, host and path
  only (B-20).
- `mintkey.UsageFor(cmd string) string`: the usage as `lvp help
  mint-key` prints it.
- `pgread`: Session, Direct and Once refuse text with a ";" before its
  end (B-16); a question whose ctx ends sets no back-off (B-5);
  Direct resets the kept connection after each question (B-2) and
  keeps the reason of a connection failure (C-1); a DATABASE_URL
  pgconn cannot read gives fixed words (C-1).
- `fsx.WriteFile`: private files are owner-only from the moment the
  temporary file exists; a replaced file keeps its mode (Linux,
  macOS) or its protected access list (Windows), even with private
  false; a new shared file gets 0666 less the umask, as Python's
  open(). `fsx.RemoveGlob` reads only the last part as a pattern.
  `fsx.IsPrivate` also checks the owner, and on Windows counts entry
  kinds it does not read as letting others in. On macOS it reads only
  the mode: an extended access list (`chmod +a`) is not seen, which
  C5's `lvp check` must say.
- `pyfmt.Format` refuses ("!(...)") every spec Python refuses for
  the value, and does `#`, `z`, `c` and a bool with "" as Python does
  (4,000 random specs in the golden). A NaN never prints a sign.
  `pyfmt.Str` prints TOML dates and times as Python's str() of
  tomllib's values.
- `gamestate.Row`: columns found as sqlite3.Row finds them (the
  first, ASCII case only); `Str` prints a float as Python's str().
- `rules.SeatNames` lists a repeated seat once and walks a text or
  table `roster.seats` as Python's loop did; `rules` refuses a file
  that starts with a byte order mark, as tomllib (O-12).
- `seats.File.MasterKey` on FromEnviron's settings names "the
  environment".
- `lvp help version`, `lvp help help` and the stubs' `--help` answer
  with exit 0.

## Entries

```text
ID      B-1
What    mintkey.Exec runs mint-key's rules in this process on a
        seats.File already read, instead of a child program. A web
        proxy is used only if the File's settings name one
        (HTTPS_PROXY, HTTP_PROXY or their lower-case forms); then the
        proxy settings are the process's, as for the program.
Why     Decision 4 (Front Desk mints in process). In --docker the
        Python child got no proxy settings (MINT_ENV); in host mode it
        got the whole environment. Both hold for the File C2 passes.
Safe    Same checks, same request, same words: Exec's output equals
        the program's for the same file and arguments.
Tests   internal/mintkey: TestExecEqualsMain, TestExecWebProxy,
        TestFrontDeskContract; go-port's tests, unchanged in meaning
        (mintkey_test.go)
```

```text
ID      B-2
What    pgread.Direct refuses, before it runs, any question that is
        not one statement whose answer is exactly one column: COPY
        (also TO PROGRAM), SET, DO, VACUUM, NOTIFY, DDL, COMMIT, and
        text with two statements. Python's Pg let psycopg run any one
        statement; COPY ... TO PROGRAM is allowed in a read-only
        transaction, so it would have run. After each question Direct
        runs "reset all; set session authorization default; select
        pg_advisory_unlock_all()" on the connection it keeps, so a
        setting (set_config(..., false)) or an advisory lock one
        question takes does not reach the next. Python kept both.
Why     More locks: the read-only session and BEGIN READ ONLY do not
        stop COPY TO PROGRAM, COPY TO a file, or session state.
Safe    Stricter. Front Desk's two questions are one SELECT each.
        Not a full guarantee: read-only mode does not stop a
        superuser's functions (the stack's litellm role is one):
        pg_terminate_backend, replication slots and origins,
        lo_export to a file, pg_import_system_collations. Python's Pg
        had the same exposure. The guard is that the SQL is fixed
        text in the code (pgread's package doc); a role with only
        pg_read_all_data would close it (question 23.9).
Tests   internal/pgread (tag pg): TestDirectRefusesWrites (32 ways to
        write, each refused, nothing changed, reads still answered),
        TestDirectResetsSession, TestDirectKnownExposures (what still
        runs, on purpose, so nobody reads the first as a guarantee)
```

```text
ID      B-3
What    pgread.Direct: an answer with no rows reads "the database
        gave no answer", as NULL and non-objects do. Python raised a
        TypeError there ("'NoneType' object is not subscriptable"),
        which became the tally's why.
Why     Python's crash path, replaced by its nearest message.
Safe    Same outcome (no tally from the database, the fallback runs).
Tests   internal/pgread (tag pg): TestDirectRefusesWrites
        ("select pg_notify(...)" gives no JSON)
```

```text
ID      C-1
What    pgread.Direct's error text is the first line of pgconn's
        error, or a server error's primary message (as psycopg gives
        it). Server errors read the same as Python's ("cannot execute
        INSERT in a read-only transaction"); connection errors read
        differently: "failed to connect to `user=litellm
        database=litellm`: 127.0.0.1:1 (127.0.0.1): dial error: dial
        tcp 127.0.0.1:1: connect: connection refused" (the first
        address's reason kept), against psycopg's "connection failed:
        ...". A DATABASE_URL pgconn cannot read gives "DATABASE_URL in
        seats.env is not a valid address; check its password":
        pgconn's own text quotes the address and can show part of
        the password. The caller scrubs it all (9.1).
Why     Another driver (decision 8).
Safe    Same cases fail; the scrub still applies; no secret is added,
        and the bad-address words hold none of DATABASE_URL.
Tests   internal/pgread: TestDirectNoDatabase, TestDirectErrorWords,
        TestDirectConnectionWords, TestDirectBadURLHidesPassword;
        (tag pg) TestDirectRefusesWrites logs every message
```

```text
ID      O-7
What    pgread.Once with docker missing: "cannot read the proxy
        database: cannot run docker: No such file or directory" (on
        Windows: "The system cannot find the file specified"). Python
        crashed with a traceback.
Why     Python's crash path, replaced by its nearest message.
Safe    Same exit (the game dies with the message).
Tests   internal/pgread: TestOnceFailures
```

```text
ID      B-4
What    pgread.Once: an answer that is not JSON rows reads "cannot
        read the proxy database: its answer is not the rows
        expected". Python crashed (JSONDecodeError).
Why     Python's crash path, replaced by its nearest message.
Safe    Same exit.
Tests   internal/pgread: TestOnceFailures
```

```text
ID      B-5
What    pgread.Session reads psql's output lines up to \n and drops a
        trailing \r; Python's text mode also split on a lone \r. A
        context that ends (Ctrl+C, shutdown, a viewer gone) ends a
        question at once with the context's error: Session stops psql
        (its answer may still come) and Direct drops its connection.
        Neither counts it as a failure: no back-off, and the next
        question starts a new psql or connection at once.
Why     Go reads bytes; Python had no cancellation. A hang-up is the
        asker's, not the database's: with a back-off, one viewer
        leaving would hold the tally for everyone for 30 s with a
        wrong reason.
Safe    psql never prints a lone \r; cancelling only stops sooner.
Tests   internal/pgread: TestSessionGolden, TestSessionFailures,
        TestSessionStall, TestSessionCancelNoBackoff,
        TestDirectCancelNoBackoff
```

```text
ID      O-1
What    rules.Load checks every table and key the game uses, in
        type, before load_rules' own checks: "rules.toml line N:
        <key> is missing" or "... must be a whole number / a number /
        text, in quotes / true or false / a list of two numbers / a
        list of two whole numbers / a list of text"; a missing table
        "rules.toml: <table> is missing". Every load_rules message
        Python gives for a right-typed file is the same, word for
        word. Three groups change:
        (1) Python's own message for a wrong type now reads Go's:
        day.length or day.close as a number (Python: "bad duration
        120: use a whole number and s, m, h or d"), a seat's persona
        as a number (Python: "seat fern: no persona 5").
        (2) Shapes that crashed Python later with a traceback now stop
        at load: per_seat or per_step as a float, chars of three
        numbers or of floats, pause as text, mana.daily as text, a
        missing persona key or guild.
        (3) Shapes Python played with are now refused: whole floats
        for counts (max_attempts = 3.0, quests = 99.0, calls = 2.0,
        reply = 80.0), a bool for a count (per_seat = true counted
        as 1), a value that is not true or false for individuals or
        stream ("false" as text was true; 0 was false),
        office.models as text ("local" minted with --models
        l,o,c,a,l), and a count outside -2147483648..2147483647
        (reply = 9223372036854775807 played a day; Go: "must be a
        whole number from -2147483648 to 2147483647").
        game.md's O-1 addendum lists three shapes that crashed Python
        mid-run and are refused at load (empty quests.sizes, weights
        not adding up to a finite number above 0, chars as [high,
        low]).
Why     Ruling, decision 20: a plain error with the line, not a
        traceback, and no quiet surprise from a wrong type.
Safe    Stricter: each refusal names the key and its line, and says
        what the value must be; no file Python refused is read.
Tests   internal/rules: TestLoadGolden (Python's answers from
        tests/golden/rules.py, Go's words in testdata/go_messages.json:
        per-seat-float, chars-three, pause-text, daily-text,
        models-text, individuals-text, individuals-false-text,
        max-attempts-float, quests-float, calls-float, reply-float,
        per-seat-bool, stream-int, ...); TestUnplayableShapes
```

```text
ID      O-12
What    rules (Load and SeatNames) read TOML with BurntSushi v1.6.0,
        Python with tomllib (TOML 1.0).
        Words: a file TOML cannot read gives "cannot read <path>:" and
        the TOML reader's words and line ("toml: line 5: expected '.'
        or ']' to end table name ..."), where Python gave tomllib's
        ("Expected ']' at the end of a table declaration (at line 4,
        column 8)"). Invalid UTF-8: Python crashed
        (UnicodeDecodeError); Go reads "cannot read ...: toml: line
        156 ...: invalid UTF-8 byte: 0xff". A file that starts with a
        byte order mark (Notepad's "UTF-8 with BOM") is refused, as
        tomllib refuses it, in Go's words ("toml: line 1: the file
        starts with a byte order mark; save it as UTF-8 without
        BOM"); BurntSushi alone would have read it.
        Files Go reads that Python refused: TOML 1.1 forms (an inline
        table over several lines, a comma after an inline table's
        last item, the \e and \x escapes, a time or date-time without
        seconds). The game plays them where office.py died; Front
        Desk guards their game seats where GameSeats lost them all.
        Files Python read that Go refuses: a number BurntSushi cannot
        hold, even under a key the game ignores (an integer past
        int64, a float past float64 such as 1e400). The game dies
        with the line; Front Desk's SeatNames gives no game seats
        from the rules (players already in office.db are still
        guarded).
Why     Another TOML reader (decision 14); neither difference can be
        switched off in it.
Safe    Every refusal names the file and a line. Go's game and Go's
        Front Desk read every file the same way, so they agree on the
        seats; the shapes that differ are not ones the shipped
        rules.toml or its comments use.
Tests   internal/rules: TestLoadGolden (syntax-error,
        syntax-no-value, duplicate-key, bad-utf8, bom, toml11-*,
        big-int-unknown, big-float-unknown), TestSeatNamesGolden
        (the same, with Go's answers in go_messages.json seat_names)
```

```text
ID      B-6
What    rules.Load warns of each key the game does not read, one
        line each: "rules.toml line N: unknown key <key> (ignored)".
        Python said nothing.
Why     Ruling, decision 20: a typo in a key name was silent.
Safe    A warning only; never fatal.
Tests   internal/rules: TestLoadGolden (good-unknown-*)
```

```text
ID      B-7
What    pyfmt.Str of a dict (only reached for absurd values, such as
        a TOML table as seat_prefix in SeatNames) lists keys sorted;
        Python keeps the file's order. Other absurd values print as
        Python's str(): TOML dates and times, lists of them, floats.
        SeatNames lists a seat the file repeats once (Python kept a
        set) and walks roster.seats as Python's loop did (an empty
        text or table: no seats, the prefix kept).
Why     Go's maps keep no order.
Safe    Such a prefix matches no seat either way.
Tests   internal/pyfmt: TestStr; internal/rules: TestSeatNamesGolden
        (prefix-date*, prefix-time*, seat-name-dates, seat-dup-name,
        seats-empty-text, seats-text, seats-empty-table)
```

```text
ID      B-8
What    pyfmt.ParseISO: a zone offset with a fraction of a second
        (+05:30:15.5) keeps whole seconds; isoformat then drops the
        fraction. Python keeps it.
Why     Go's fixed zones hold whole seconds.
Safe    LiteLLM never sends such offsets; the time is read, not
        refused.
Tests   internal/pyfmt: TestISO (5 such cases skip the text check)
```

```text
ID      B-9
What    pyfmt.Lower uses Go's Unicode tables; letters whose case
        changed between Unicode versions may differ from Python
        3.13's (Unicode 15.1). Same as F-15, at its source.
Why     Go's own tables.
Safe    Seat names are ASCII by SEAT_RE; the golden's 339 strings
        (Greek final sigma, dotted I, Cherokee) agree.
Tests   internal/pyfmt: TestText
```

```text
ID      B-10
What    pyfmt.IsLoopback follows Python 3.13: an IPv4-mapped address
        (::ffff:127.0.0.1) is loopback. Python 3.12, which the Python
        Front Desk image runs, says it is not.
Why     Goldens come from 3.13 (tests/golden/README.md).
Safe    Over a network (over_network) is answered false for such a
        name only when it truly is this machine.
Tests   internal/pyfmt: TestIP
```

```text
ID      B-11
What    args: no prefix abbreviations (--po is not --port; argparse
        took it), "-qx" (two short flags in one word) is refused, and
        --help lays out its own way. Errors keep argparse's form:
        "usage: ..." then "<cmd>: error: argument -d/--days: '0': use
        1 or more", exit 2. Same as F-18 and O-9, at their source.
Why     Decision 18.
Safe    A mistyped flag is refused rather than guessed.
Tests   internal/args: TestParse, TestErrors, TestHelpAndExit
```

```text
ID      B-12
What    fsx.Private on a folder (Windows) also lets what is made in
        it inherit the two entries (SDDL OICI), so a key written into
        game/keys is private before Private is called on it. The spec
        gives the SDDL without OICI; files get exactly that. A private
        file is owner-only from the moment its temporary file exists
        (made with that access list, share mode 0; on Linux and macOS
        made 0600), not only after it is written.
Why     A file that is briefly the folder's default would otherwise
        get a default access list, and a handle opened then keeps its
        rights.
Safe    Stricter: nothing in the folder is ever open to others.
Tests   internal/fsx: TestWriteFileModes (Linux, macOS);
        (Windows, CI only) TestPrivateACL, TestWriteFilePrivateFromBirth,
        TestWriteFileKeepsPrivate
```

```text
ID      B-13
What    gamestate.Files lists office.db* by reading the folder (sorted;
        on Windows any case), not with a glob, so a folder with [ in
        its name is not read as a pattern.
Why     Python's Path.glob escapes the folder; Go's Glob does not.
Safe    The same files.
Tests   internal/gamestate: TestFiles
```

```text
ID      B-14
What    `lvp help mint-key` prints mint-key's usage on standard output
        and exits 0, typed as lvp is typed here ("./lvp mint-key
        <seat> ..."), without the mint-key program's "Type
        ./mint-key ..." line. The mint-key program prints its own
        usage on standard error with exit 2, and only once it has
        found seats.env. `lvp mint-key` after a mistake (--help,
        a wrong option) prints that same usage, on standard error
        with exit 2, but typed as lvp is typed, as `lvp help
        mint-key` does: the program's "Type ./mint-key ..." line
        named a mint-key.exe that a user of lvp does not have. One
        of mint-key's options typed straight after lvp (`lvp
        --revoke-seat x`) adds "Did you mean: lvp mint-key
        --revoke-seat x" under the no-command line. `lvp help
        version` and `lvp help help` say what those do, exit 0.
Why     Help and usage should be in the words of the program the
        user typed, and show help without a seats.env.
Safe    Everything else `lvp mint-key` prints, and every request,
        is byte-equal to the program; the mint-key program keeps
        its own words.
Tests   internal/cli: TestHelpAndVersion, TestMintKeyUsageFor,
        TestCommandHelp, TestMintFlagHint, TestMintKeyUsageNamesLvp;
        cmd/mint-key: TestShimEqualsLvp
```

```text
ID      B-15
What    The secret scan (archtest, section 16.0) covers every file in
        the repo, text or not, but those git ignores, where the spec
        names tests/reference only. A match is not a secret when it is
        a run of decimal digits alone (a golden prints 1.5e300 with
        "f"), a pinned hash after "sha256:", "DIGEST=" or "@" (images,
        packages, actions' commits), or an sk- key with a word no
        random key has (fake, test, example, dummy, master, shim) that
        a test made up. A hit names the file, the line, the kind and
        the length, and shows none of the match.
Why     The spec's hex rule matched long numbers and pinned hashes;
        a hit that printed part of the secret would put it in the CI
        log.
Safe    Wider: secrets are looked for everywhere, not in two folders.
Tests   internal/archtest: TestSecretScan, TestSecretScanMessages
```

```text
ID      B-16
What    pgread refuses a question whose text has a ";" before its end
        (Session and Direct), or anywhere (Once, whose text goes
        inside a subquery): "refused: a question must be one
        statement, with a ; only at its end", or for Once "cannot
        read the proxy database: refused: a question must be one
        statement, with no ;". Python sent such text to psql as it
        was, where "select 1; commit; set
        default_transaction_read_only = off; insert ..." writes.
        Session and Once also refuse any backslash ("refused: a
        question for psql must hold no \"): in psql's stdin it
        starts psql's own commands, and \g or \! need no ";".
Why     Defence in depth for spec 9.5's byte-for-byte texts; the
        fixed SQL stays the guard.
Safe    Stricter. Front Desk's questions end with their only ";"; the
        game's have none; names quoted in pass the rules' checks,
        which allow no ";".
Tests   internal/pgread: TestSeveralStatementsRefused,
        TestBackslashRefused, TestSessionGolden, TestOnceGolden
```

```text
ID      B-17
What    The password form the scan looks for is
        [a-hjkmnp-z2-9]{4}(-[a-hjkmnp-z2-9]{4}){3}, anywhere in a line,
        where the spec says [a-z2-9]{4}(-[a-z2-9]{4}){3}.
Why     make_password never uses i, l or o (they look like 1 and 0);
        the spec's form also hits names such as
        "390x844-dark-copy-refused" in a recorded browser script.
Safe    Every password Front Desk makes is still found, also when a
        letter, digit or _ touches it.
Tests   internal/archtest: TestSecretScanMessages
```

```text
ID      B-18
What    mint-key's message for a redirect names where it pointed as
        scheme, host and path only ("(to https://h.example/ui)"),
        the path decoded and scrubbed; go-port's showed the whole
        Location with only the literal master key cut. An answer
        shown in a message is scrubbed before it is cut to 300
        characters, and an answer cut at the most a Client reads that
        ends in the key's first characters has them hidden too.
        mint-key.sh showed no Location.
Why     A server that echoes the request (a wrong PROXY_URL, or a
        hostile one) could show the master key, cut or %-encoded,
        on the terminal and, once C2 shows these errors, the page.
Safe    Stricter: no part of the master key is ever shown, beyond the
        last 4 characters Mask keeps.
Tests   internal/litellm: TestEchoedKeyIsHidden, TestRedirectHidesKey,
        TestNoRedirects
```

```text
ID      B-19
What    A proxy address of "localhost" is tried as ::1 first, then
        127.0.0.1 after 300 ms (or at once if ::1 is refused); the
        first connection made is used. This is how curl, and so
        mint-key.sh, reads localhost. Go's resolver, and Python's
        (gui/frontdesk.py, game/office.py), take /etc/hosts, where
        on many Linux systems localhost is 127.0.0.1 only. Any other
        host, 127.0.0.1 written out included, is dialed as it is.
Why     Seen on a real Windows machine: inside WSL with the proxy in
        Docker, 127.0.0.1:4000 accepted and then hung while
        [::1]:4000 answered, so lvp mint-key and lvp desk failed
        with PROXY_URL=http://localhost:4000 where mint-key.sh and
        curl worked.
Safe    Both addresses are this machine's own loopback; nothing
        leaves it. Where ::1 is missing the dial falls through at
        once, so nothing gets slower.
Tests   internal/litellm: TestLocalhostFirst, TestLocalhostReal
```

Not a difference, written down so nobody looks for one: `lvp
mint-key` and the `mint-key` shim print the same bytes as go-port's
`mint-key` for every case its tests and `tests/mint-parity.sh` cover
(B-18 is a redirect, or an answer holding the key, which they do not);
go-port's test file moved to `internal/mintkey/mintkey_test.go` with
only the package name, `run` to `Main` and the `System` field names
changed.

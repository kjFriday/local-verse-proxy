# Ledger: game and fake-ollama (chunk C3)

Differences between `lvp game` (internal/game) and `game/office.py`,
and between `lvp fake-ollama` (internal/fakeollama) and
`game/fake_ollama.py`, each with why, why it is not weaker, and the
test that shows it. Format and rules: spec section 19. The entries
for C1's packages that the game uses (O-1, O-7 and O-12 for rules
and pgread; B-11 for flags) live in `base.md`; this file adds what
the game itself does.

## What is the same, and how it is shown

Byte for byte, against Python's own answers captured at
`py-reference` with Python 3.13.17 (`tests/golden/office_play.py`,
`office_score.py`, `fakeollama.py`):

- post_quests for four seeds (int, text, float) over five days: ids,
  sizes, values, as stored (`TestPostQuestsGolden`).
- Thirteen single-bot traces with a scripted proxy: every chat
  body's bytes (the first five in full, the rest by SHA-256), every
  sleep, the clock at the end, and the quests, completions and
  daylog rows (`TestBotGolden`). So the dice are drawn in the same
  order, and the request JSON is Python's `json.dumps` exactly.
- wait_for_day over fifteen proxy states: what it asks Postgres
  (the SQL text), every sleep, revoke and re-mint, what it prints,
  its die messages and its answer (`TestWaitForDayGolden`).
- score_day over twelve made-up days in four weeks (slow, quick and
  never-complete spend logs; other keys' calls; untagged calls; a
  seat tag that is not the paying key's; rules as floats): the SQL
  asked, the sleeps, and the days, usage and scores rows after,
  with every float's repr (`TestScoreDayGolden`).
- board, board --day, board --seat (by name and by seat, unknown,
  with a quote), day_summary and the guilds-only board, on those
  four weeks and on the three reference weeks
  (`TestBoardsGolden`, `TestReferenceBoards`): 300-odd texts.
- The commands' texts and effects with a fake mint-key: setup (and
  twice, a failing mint, no key, a silent failure), reset (none,
  all, one, failing, an exit code), run and board without an
  office, the week over, no day yet, a day out of range; also the
  arguments each mint and revoke got, the key files and the files
  left (`TestCommandsGolden`).
- money, seconds (and its die message), clock, rank_corr, positive
  (`TestHelpersGolden`).
- fake-ollama: tokens and prompt_text over a corpus, and the whole
  answer (status, Content-Type, Content-Length, body) to 54
  requests with the dice pinned, the 501 pages included
  (`TestTokensAndPromptGolden`, `TestAnswersGolden`).

Python 3.12 and later add floats in `sum()` with Neumaier's
compensated sum; the board's guild averages and the seat board's
totals use it, so Go does too (internal/game pyval.go). A plain sum
was off by 0.1 on one reference week's seat board.

End to end: `TestPracticeWeek` plays a week of two 2-second days
against fakelitellm, with a docker stand-in that answers psql from
the fake's records and a real child `lvp mint-key`; Python's `board`
then reads the office.db Go wrote and prints the same bytes as Go's
(skipped where no Python 3.11 runs, as on Windows' store stub). The
live run on a real stack is at the end of this file.

## Entries

```text
ID      O-1 (game's addendum; the entry is in base.md)
What    Shapes base.md left for C3, measured against office.py at
        py-reference: roster.seats = "" (Python plays a week with
        no seats: setup mints none, run posts no quests), a
        non-empty text roster.seats, a number as seat_prefix or as a
        seat name, and a missing table (Python: TypeError or KeyError
        traceback at load). Go refuses each at load: "rules.toml line
        143: roster.seats must be a list of seats, like {name =
        "fern", persona = "frugal", guild = "owls"}", "... line 5:
        office.seat_prefix must be text, in quotes", "... line 144:
        roster.seats[0].name must be text, in quotes", "rules.toml:
        office is missing".
        Four more, which Python read at load and then crashed on in
        the middle of `run`, are refused at load too:
        - an empty [quests.sizes] (Python: IndexError in post_quests;
          Go had panicked there): "rules.toml line 34: quests.sizes
          must have at least one size, like [quests.sizes.small]";
        - weights adding up to 0 or less, or to inf or nan (Python:
          random.choices' "Total of weights must be greater than
          zero" / "must be finite" before the first quest; Go had
          posted every quest as the last size and played the week):
          "quests.sizes weights must add up to more than 0" / "... to
          a finite number". A negative weight with a total above 0
          plays the same in both;
        - a persona's chars as [high, low] (Python: randint's "empty
          range in randrange(700, 301)" stopped that bot mid-day; Go
          had played the bot all day): "rules.toml line 82:
          personas.frugal.chars must be [low, high], like [300,
          700]";
        - a whole number outside -2147483648..2147483647 (Python
          played reply = 9223372036854775807): "... must be a whole
          number from -2147483648 to 2147483647" (it read "must be a
          whole number" before, wrongly). This one is base.md O-1's
          group (3).
Why     Decision 20; Python's crash paths, and an empty office that
        can only be a mistake.
Safe    Stricter; each refusal names the key and line.
Tests   internal/rules TestUnplayableShapes (the four above, and the
        negative weight and equal chars that still load); C1's
        goldens hold the typed refusals; the first five files were
        run by hand against both programs for this entry
        (tests/golden/rules.py is C1's, so they are not added
        there).
```

```text
ID      O-2
What    Mint and revoke errors are the Go mint-key's words, which the
        game passes on as office.py passed on mint-key.sh's: "mint
        game-fern failed: <mint-key's stderr>", "could not revoke N
        of M game seats. <seat>:\n  <its stderr, words joined>". For
        a proxy that is down, mint-key.sh said curl's "curl: (7)
        Failed to connect to localhost port 4000 ..."; lvp mint-key
        says "Failed to connect to the proxy at ...". The tests that
        LiteLLM's 404 means "no key" ("No keys found" in the text)
        and exit 0 means "revoked" read both programs the same.
Why     Decision 3: the game mints through a child lvp mint-key.
Safe    Same checks, same outcomes; the master key stays in the
        child.
Tests   internal/game: TestMinter (lvp mint-key), TestMinterScript
        (mint-key.sh, Linux and macOS), TestCommandsGolden
```

```text
ID      O-3
What    A chat call that fails after the request was sent is named
        as urllib named it: TimeoutError, RemoteDisconnected,
        ConnectionResetError; before it, URLError. Where Python's
        bot thread crashed, Go names the failure and the bot goes
        idle like any refused call: an answer cut short is
        IncompleteRead (Python: http.client.IncompleteRead, not an
        OSError, crashed the thread; its daylog row was still
        written); a status line Go cannot read is RemoteDisconnected
        (Python: BadStatusLine crash). A redirect is not followed:
        its status is the answer (urllib followed 301, 302 and 303
        as a GET and refused 307 and 308 for a POST; the proxy never
        redirects). An error answer whose body cannot be read is
        judged on what came (Python: crash).
Why     Python's crash paths, replaced by the nearest behaviour; a
        seat key is never sent to another address.
Safe    The bot stops for the day, as for any refusal; the
        scorekeeper still checks the spend logs.
Tests   internal/game: TestChatAnswers, TestChatFailures
```

```text
ID      O-4
What    After the deadline, play_day waits for each bot up to
        max(1 s, deadline + 70 s - now), as Python's join. A bot
        still working then is stopped and writes nothing more (no
        daylog row, no quest or completion change). Python's bot, a
        daemon thread, kept running and could write while the day
        was being scored.
Why     A late write could change a day already scored.
Safe    The day is scored on what was written by the deadline +
        70 s; its quests still "working" become "unfinished", as in
        Python.
Tests   internal/game: TestAbandonedBots
```

```text
ID      O-5
What    Key files are written through fsx.WriteFile, owner-only
        from the moment they exist (0600; on Windows a protected
        access list: the user and SYSTEM). game/keys is made
        owner-only on every mint (0700, or that list); Python made it
        0700 only when it created it (less the umask) and left an
        existing folder as it was.
Why     Gap 7: Windows has no modes; and a folder made by hand was
        left open.
Safe    Stricter.
Tests   internal/game: TestMinter, TestPracticeWeek (fsx.IsPrivate);
        internal/fsx's Windows tests (CI only)
```

```text
ID      O-6
What    Chat calls to the proxy on localhost or a loopback address
        never use a web proxy from HTTP_PROXY and the like; other
        hosts use it, as urllib did. urllib used the environment's
        proxy for localhost too, unless NO_PROXY named it.
Why     The proxy on this machine is never behind a web proxy; a
        set HTTP_PROXY (common in offices) broke every bot call.
Safe    Nothing leaves the machine that did not before.
Tests   internal/game: TestChatNoProxyForLoopback
```

```text
ID      O-8
What    Messages that named old files say lvp, as spec 11.9's table:
        "`<Prog> game reset`" for "`office.py reset`" and
        "`python3 game/office.py reset`", "mint-key gave no key",
        "mint-key exit N"; "Next: <Prog> game run --days 2". <Prog>
        is how lvp is typed here (./lvp, .\lvp, lvp, or its path).
        "office.py needs Python 3.11" is gone.
Why     The Python files go at Gate C.
Safe    Words only.
Tests   internal/game: every golden test compares through the
        table (rename in helpers_test.go); TestFlags
```

```text
ID      O-9
What    Flags: lvp game [--rules FILE] <setup|run|board|reset>, with
        --rules also after the command (argparse refused it there);
        no prefix abbreviations (B-11). Usage and errors keep
        argparse's form with lvp's names: "usage: lvp game run [-h]
        [--rules FILE] [-d N] [-q]" then "lvp game run: error:
        argument -d/--days: '0': use 1 or more", exit 2; the missing
        and the unknown command read as argparse's ("the following
        arguments are required: cmd", "argument cmd: invalid choice:
        'x' (choose from 'setup', 'run', 'board', 'reset')"). The
        metavars are N, NAME and FILE (argparse: DAYS, DAY, NAME,
        RULES), and --help lays out its own way. A count past the
        int range is held at the largest int (Python's int is
        unbounded; either way the week ends first).
Why     Decision 18; spec 4.2's usage lines.
Safe    A mistyped flag is refused rather than guessed.
Tests   internal/game: TestFlags, TestHelpersGolden (positive)
```

```text
ID      O-10 (removed: no difference)
What    The chat request's JSON is Python's json.dumps of the same
        dict, byte for byte: ", " and ": ", ASCII escapes, the keys
        in office.py's order. So is spend_where's tags literal.
Tests   internal/game: TestBotGolden, TestScoreDayGolden
```

```text
ID      O-11
What    reset removes office.db* with fsx.Remove, which retries for
        2 s while another program holds a file (Windows), then
        stops: "game/office.db is in use by another program, likely
        a running <Prog> game run or Front Desk. Stop it, then run
        <Prog> game reset again.", exit 1, the seats already
        revoked. Python never ran natively on Windows; on Linux and
        macOS removing an open file works, in both.
Why     Gap 3.
Safe    Nothing half-wiped is reported as done; reset can be run
        again.
Tests   internal/game: TestResetWhileOpen (Windows, CI only)
```

```text
ID      O-13
What    lvp game needs the proxy folder: compose.yaml in the current
        folder or next to lvp (kit.Dir); else "cannot find
        compose.yaml: run lvp in the proxy's folder, ...", exit 1,
        before the rules are read. office.py found its folder from
        its own path. The rules default to <proxy folder>/game/
        rules.toml, so "cannot read ..." names that full path, as
        Python's did.
Why     A single program has no script path to start from (as F-17).
Safe    A plain message where a wrong folder would have run psql
        and mint-key in the wrong place.
Tests   internal/game: TestNoProxyFolder
```

```text
ID      O-14
What    Python's tracebacks in the game become one plain line, exit
        1: an office.db error ("office.db: <SQLite's words>"; in a
        bot "bot <seat>: office.db: ...", and that bot stops); a
        player whose persona the rules no longer have ("seat fern:
        no persona 'frugal'", load_rules' words); a key file that
        cannot be read (str(OSError)); meta day that is not a number;
        scores or usage naming a seat that is not a player; a board
        with no players; a completion with no quest; mint-key that
        cannot be started ("cannot run mint-key: <C library's
        words>"); a key folder or file that cannot be written (the
        mint's failure: "mint game-fern failed: [Errno 13]
        Permission denied: ..."). Values Python could not add or
        compare and crashed on are read as 0: a NULL reset_at,
        spend or token sum from Postgres. A division Python refused
        (the Goodhart line's points ratio when the top scorer has
        quests done but 0 points, which needs quest values of 0)
        prints inf or nan.
Why     Python's crash paths, replaced by the nearest documented
        behaviour (spec 1).
Safe    Same outcome (the command stops, or the board prints);
        none of these happen with the shipped rules and a proxy
        that answers.
Tests   internal/game: TestDieMessages; the shapes are reviewed in
        code (each is a single branch); the shipped flows are in
        the goldens
```

```text
ID      O-15
What    Ctrl+C: every sleep, wait, psql and mint-key child takes the
        command's context; once it is done nothing more is written
        to office.db (bots included) and the command prints "\n" and
        its STOPPED text to standard error, exit 1, as office.py's
        KeyboardInterrupt did. Python's bot threads were killed at
        exit and could write in between. SIGTERM ends lvp at once,
        as it ended Python. A closed standard output (board | head)
        ends quietly on Linux and macOS, as SIG_DFL did; on Windows
        the write fails and the command ends.
Why     Spec 11.7.
Safe    A stopped day is never half scored; run plays it again.
Tests   internal/game: TestStoppedRun (while waiting for the day,
        while bots work, while waiting for spend logs),
        TestStoppedMint (setup and reset, the child stopped)
```

```text
ID      P-1
What    lvp fake-ollama speaks HTTP/1.1 and keeps connections open;
        fake_ollama.py's BaseHTTPRequestHandler answered HTTP/1.0
        and closed each one (its 501 pages still say Connection:
        close). It sends no Server header (Python: BaseHTTP/0.6
        Python/3.12.x). The JSON is json.dumps's, byte for byte (the
        spacing part of the spec's P-1 is not a difference). Kept
        equal: a path that starts with // is cut to one / before
        routing, as Python 3.12's http.server does (LiteLLM sends
        //api/chat when api_base ends in /).
Why     net/http; LiteLLM's client is fine with either.
Safe    Same answers.
Tests   internal/fakeollama: TestAnswersGolden ("//", "///api/tags",
        "//api/chat")
```

```text
ID      P-2
What    Requests that crashed Python's handler (it printed a
        traceback and closed the connection with no answer) get 400
        {"error": "bad json"}: a num_predict int() refuses ("abc",
        a list), options or messages of the wrong shape, a content
        part whose text is not text, a chat body that is not an
        object, a Content-Length that is not a number.
Why     Python's crash paths, replaced by the nearest documented
        answer.
Safe    LiteLLM never sends these; it gets an answer instead of a
        dropped connection.
Tests   internal/fakeollama: TestAnswersGolden (the "crash" cases)
```

```text
ID      P-3
What    JSON Go refuses where Python's json.loads read it: the NaN,
        Infinity and -Infinity words, and UTF-16 or UTF-32 bodies.
        Both give 400 {"error": "bad json"}. A lone surrogate escape
        ("\ud800") reads as U+FFFD, so an answer that echoes it (the
        model name) says "\ufffd" where Python said "\ud800" (as
        Front Desk's F-5). Kept equal: a UTF-8 byte order mark before
        the body is skipped, as json.loads of bytes does.
Why     encoding/json reads RFC 8259 JSON only, and Go strings hold
        no lone surrogates.
Safe    LiteLLM sends UTF-8 JSON, with neither form.
Tests   internal/fakeollama: TestAnswersGolden (the byte order mark
        cases equal; the "\ud800" case equal but for that escape;
        NaN and UTF-16 forms are not in the golden)
```

```text
ID      P-4
What    lvp fake-ollama takes no arguments but -h/--help: anything
        else is "unrecognized arguments", exit 2 (fake_ollama.py
        ignored its arguments). A port it cannot listen on reads
        "lvp fake-ollama: cannot listen on port 11434: Address
        already in use", exit 1 (Python: a traceback). Headers must
        come within 60 s (Python waited forever, holding a thread).
        The words come from math/rand/v2, unseeded, as Python's
        module random was unseeded: the filler differs, its sizes
        and counts follow the same rules.
Why     Spec 12; a stuck client should not hold the server.
Safe    Stricter, and the same counts.
Tests   internal/fakeollama: TestMainFlags, TestServe, TestShapes
```

## Live run (sandbox stack lvp-c3, port 4222)

One stack (LiteLLM v1.106.6 image built here, Postgres 16, and the
practice model server as the Go `lvp fake-ollama` in a distroless
container), game/compose.game.yaml's settings (reset sweep every
5 s), two copies of the repo: one played by `lvp game` (seat prefix
go-), one by `python3 game/office.py` (prefix py-), the same rules
otherwise: seed 42, 1m days, close 25s, 2 days, all ten seats.

- setup: both printed the same text (but the prefix, the office id
  and the command name); keys 0600 in a 0700 folder on both sides.
- run -d 2, both at once: exit 0 for both, every printed line in the
  same form; the two weeks' 200 quests (id, day, size, value,
  calls, par mana) are identical. The rest differs by timing, as
  between two Python runs ("same seed, same dice, timing still
  varies"): 167 and 166 calls answered on day 1, and so on.
- Cross-reading: 52 board commands (board, -d 1 to 3, -s for every
  seat by name and by seat, an unknown seat) on each office.db,
  printed by Go and by Python: identical bytes, exit codes and
  errors, both ways.
- A mixed week each way: Go played day 3 on the week Python had
  played days 1 and 2 of (Python's keys, from mint-key.sh), and
  Python played day 3 on Go's week; after each, the 52 cross-read
  boards were still identical.
- reset: Go revoked 10 seats through child lvp mint-key, then "0
  game seats; 10 had no key" on a second run; Python revoked its 10
  through mint-key.sh; the proxy then held no go- or py- key.
- Ctrl+C (SIGINT) during run: "\nstopped; this day is not scored.
  `run` plays it again." on standard error, exit 1, no day scored.
- fake-ollama logged nothing but its start line.

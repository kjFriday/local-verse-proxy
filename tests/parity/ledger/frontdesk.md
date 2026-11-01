# Ledger: Front Desk (chunk C2)

Every way `lvp desk` differs from `python3 gui/frontdesk.py` at
`py-reference` (c7d7098), with why, why it is not weaker, and the
test that shows it. Format and rules: spec section 19. Entries that
live in another chunk's ledger are pointed to, not repeated (B-n:
tests/parity/ledger/base.md).

## How the proof is laid out

- `tests/golden/frontdesk_core.py`, `frontdesk_games.py`,
  `frontdesk_gate.py`, `frontdesk_http.py` capture Python 3.13.17's
  answers into `internal/frontdesk/*/testdata/golden/`. Run from the
  repo folder with `python3 -B tests/golden/<name>.py`. A second run
  of each gives the same bytes (checked), but `start.json`, whose
  Python stops on Ctrl+C sometimes end in a traceback (F-20).
- What is byte for byte equal to Python, by those goldens: the state
  JSON of 200 cards with hostile names (`TestGoldenBuild`), seat
  states, `dollars`, `utc_text`, `merge_tally`, `http_tally`, the SQL,
  the settings, GameSeats over odd files, every Gate trace (lock
  windows, clock steps, sessions, the 100-session cap), the banners,
  the start errors, the flag error lines, `host_ok` over 42 Hosts in
  three modes, `host_name`, `over_network`, `seat_url`,
  `plain_error`, and about 225 raw requests to a real server (status,
  headers, bodies, log lines, mint-key arguments) in local and
  `--lan` modes, but where an entry below says otherwise.
- Live, on the sandbox stack `lvp-c2` (LiteLLM v1.106.6, Postgres
  16, fake-ollama): `lvp desk` on 8021 beside `frontdesk.py` on 8121,
  host mode, then `--login`, then `lvp desk --docker` in a distroless
  container on the stack's network. Mint, SSE, a chat call through
  each minted key, the tally from Postgres, revoke, 409 and 400
  answers, the lock after five wrong passwords: the same answers; the
  two `/api/state` answers equal at a quiet point but for card ids
  (and `desk.lan`, which `--docker` always sets). No secret in any
  log. `frontdesk.py --docker` was not run: its image needs psycopg
  from PyPI, which this sandbox cannot reach (C4's image job runs it).

## Kept equal on purpose

A viewer who closes the page mid-mint or mid-revoke does not stop it:
mint-key runs to its end as Python's subprocess.run did, and the
card is made or cancelled (web TestHangUpFinishesMint).

Python's behaviour, kept though Go would differ by default: JSON
bytes as `json.dumps` writes them (ensure_ascii, its separators,
`repr()` floats, ints kept as ints, keys in the proxy's order;
`internal/frontdesk/pyjson`); explicit Content-Length, never chunked;
`Accept-Encoding` searched for `gzip` as text, case and all; a path
that starts with `//` cut to one `/` (Python 3.12 and later); a
request line over 65,536 bytes is 414, 100 or more header lines (the
blank line counts, as http.client counts it) or one over 65,536 is
431, both as Python's JSON; `OPTIONS *` and unknown methods (`get`
too) 501 JSON; `If-None-Match: ""` matching an empty ETag; a UTF-8
byte order mark accepted on bodies and proxy answers; a lone
surrogate password counted as a wrong try; Python's class names in
notes; the C library's words for a busy port; `Server: FrontDesk`.

## Entries

```text
ID      F-1
What    Host-mode settings come from internal/seats (bash-like, as
        the Go mint-key reads seats.env): a name the file leaves out
        comes from the environment (Python read the file only), and
        an empty DEFAULT_MODELS, DEFAULT_BUDGET or DEFAULT_DURATION
        means the default (Python: an empty list or text). The
        hidden list holds the file's names only, as before. $(...)
        or backticks stop the start with the line number.
Why     One reader for seats.env everywhere (decision 26).
Safe    The same values for every file both read; a line that could
        run a command is refused instead of half-read.
Tests   desk TestGoldenSettings; config TestLoadFileErrors,
        TestFromEnviron

ID      F-2
What    PROXY_URL goes through litellm.ParseBase: no scheme means
        http:// (Python's urlsplit read "localhost:4000" as a scheme
        and fell back to localhost:80); a path stays in front of the
        API paths, as mint-key's and mint-key.sh's requests always
        kept it (Python's desk dropped it); a bad address or a port
        over 65535 stops the start: "PROXY_URL in <path> is not a web
        address (...)" or "... has a bad port" (Python crashed in its
        first check, then every check failed).
Why     The desk and minting must reach the same proxy.
Safe    Stricter; the address is never shown in the message.
Tests   config TestBadProxyURL

ID      F-3
What    A mint-key failure reads "mint-key said: ..." in host mode
        too, and the log line "mint-key said: ..."; the text after it
        is the Go mint-key's own (go-port). Python said
        "mint-key.sh said:".
Why     Minting runs mintkey.Exec in process (decision 4); rename
        table 11.9.
Safe    The same scrub, [hidden] and 300-character cut.
Tests   web TestGoldenPlain, TestGoldenServerLocal (the goldens
        apply the rename)

ID      F-4
What    ETag values (/api/state and the page's files) are 16 hex of
        SHA-256, where Python used BLAKE2b; gzip bytes differ. Card
        ids are the same HMAC-SHA256 as Python's (equal with the same
        secret, which is random per run in both).
Why     BLAKE2 is not in Go's standard library; ETags are opaque.
Safe    Same length, same use; the id-to-token map stays private.
Tests   desk TestGoldenBuild (ids equal); web TestGoldenServerLocal
        (ETags masked, 304s equal)

ID      F-5
What    JSON bytes are Python's (pyjson), with these left: a lone
        surrogate escape in a proxy answer ("\ud800") becomes U+FFFD
        and is written "\ufffd"; tally counts past 2**53 lose
        precision (Python ints are exact).
Why     Go strings are UTF-8; counts are float64.
Safe    Display only; no such values come from LiteLLM.
Tests   pyjson TestMarshal, TestDecode; desk TestGoldenBuild

ID      F-6
What    /api/session gains "reset_cmd": "<how lvp was typed> game
        reset" in host mode, "lvp game reset" with --docker;
        gui/static/app.js shows it where it showed "python3
        game/office.py reset", which stays as its fallback.
Why     Gap 4: the page must name the right command on each system.
Safe    A new field; the page works with either server.
Tests   frontdesk TestEndToEndLocal, TestEndToEndDocker (pg); web
        TestGoldenServerLocal (masks it)

ID      F-7
What    A FRONTDESK_PASSWORD with U+FFFD or invalid UTF-8 stops the
        start (with --lan, --login or --docker): "FRONTDESK_PASSWORD
        has a character that is not plain text. Pick another
        password." (and the --docker tail).
Why     Go's JSON turns "\ud800" into U+FFFD; such a password could
        then match a typed U+FFFD.
Safe    Stricter.
Tests   frontdesk start golden (length rule); the rule itself is
        in frontdesk.Run

ID      F-8
What    A Cookie header with other apps' malformed cookies still
        finds Front Desk's (Go's parser); Python's SimpleCookie gave
        up, so such a browser was logged out.
Why     net/http's cookie reader.
Safe    The cookie must still match a session from that address.
Tests   web TestCookieAmongOthers

ID      F-9
What    A card time that cannot be read (expires) counts as absent,
        and is logged once per text: "a card's time cannot be read,
        so it counts as none: '<text>'". Python's check failed, every
        time, and the page froze. An answer of the wrong shape (keys
        not a list, a key not an object, a model id not text) gives
        the note "Could not read the key cards: JSONDecodeError"
        (Python: "check failed: AttributeError(...)" in the log, page
        frozen); a spend that is not a number counts as 0;
        /user/daily/activity of the wrong shape is skipped.
Why     Python crash paths are replaced by the nearest documented
        behaviour (spec 1).
Safe    The page keeps working and says what is wrong.
Tests   desk TestGoldenSeatState (Python's errors map to absent),
        TestNotes

ID      F-10
What    NaN and Infinity in a proxy answer are refused as bad JSON
        (the JSONDecodeError note); a non-finite spend is 0.
Why     Go's decoder; Python passed NaN on and the page froze.
Safe    Stricter.
Tests   desk TestNotes; pyjson TestDecode

ID      F-11
What    Log lines that showed Python's repr(e) show the Go text,
        scrubbed, cut at the same length: "check failed: <text>",
        "error: <panic text>". mint-key running past 30 s logs
        "error: mint-key did not finish in 30 seconds" and answers
        the 500 (Python logged repr(TimeoutExpired), which held the
        command line, a card's token for a revoke by id).
Why     No repr() in Go.
Safe    Less in the log: no token, never a key.
Tests   web TestMintLate, TestPanicIs500

ID      F-12
What    The tally's "why" uses Go's words for a JSON error and
        pgconn's for a database error (C-1 in base.md), scrubbed, cut
        at 160 code points; a question still running after 10 s gives
        "" as Python's.
Why     Different libraries.
Safe    Same scrub and cut.
Tests   desk TestTallyAndFallback, TestTallySkippedWhileBusy

ID      F-13
What    A tally question still running is not asked again; Python
        queued it behind the first.
Why     Ruling D13: no pile-up against a slow database.
Safe    The last tally stays, or the proxy's numbers, as before.
Tests   desk TestTallySkippedWhileBusy

ID      F-14
What    The class names in "nothing answers at host:port (<Class>)"
        and "Could not read the key cards: <Class>" come from a
        table (desk/errname.go); rare errors read OSError.
Why     Go errors are not Python's classes.
Safe    The words around them are the same.
Tests   desk TestNotes (refused, timeout), TestNotesNonsense

ID      F-15
What    Lower() of letters whose case changed between Unicode
        versions may differ (seat sorting, the 409 check, game
        names).
Why     Go's Unicode tables are not Python's.
Safe    Only exotic letters; checks stay exact for the rest.
Tests   desk TestGoldenBuild (Turkish, Greek and accented names)

ID      F-16
What    The desk's GETs never use HTTPS_PROXY or HTTP_PROXY (as
        http.client); minting does, in host mode, as mint-key does;
        --docker minting never does (B-1).
Why     Decision 30.
Safe    The master key goes only to the proxy, or through a web proxy
        the person set for mint-key already.
Tests   by construction (litellm.Options.EnvProxy false in
        desk/proxy.go); mintkey's own tests

ID      F-17
What    Host mode needs the proxy folder (compose.yaml in the current
        folder or next to lvp), else exit 1: "cannot find
        compose.yaml: run lvp in the proxy's folder ...".
Why     psql runs `docker compose exec` there; Python knew its folder
        from its own path.
Safe    A plain message instead of psql failing later.
Tests   frontdesk start golden (the folder is given); kit's tests

ID      F-18
What    Flags: no prefix abbreviations (--po is not --port); the
        usage and help text differ; --port takes 0 to 65535 (Python
        crashed above); the error lines are argparse's words.
Why     internal/args (decision 18).
Safe    Same flags, same exit code 2.
Tests   frontdesk TestGoldenStart (error lines equal)

ID      F-19
What    "cannot use port P: <words>. Try --port P+1." uses Windows'
        own words on Windows.
Why     pyfmt.StrError.
Safe    Same message around them.
Tests   frontdesk TestGoldenPortInUse (CI only on Windows)

ID      F-20
What    Ctrl+C and SIGTERM both stop cleanly: an empty line, psql
        stopped, exit 0. Python died at once on SIGTERM; its Ctrl+C
        sometimes ended in a KeyboardInterrupt traceback, exit -2
        (seen in the start golden); a frontdesk.py started in the
        background by a script ignored SIGINT and kept its psql
        running (seen live).
Why     signal.Notify for both.
Safe    psql never outlives Front Desk.
Tests   frontdesk TestEndToEndLocal (Ctrl+C, psql gone),
        TestEndToEndLogin (SIGTERM); Linux and macOS

ID      F-21
What    lvp version; one program for desk, game and mint-key.
Why     Decision 1.
Safe    Nothing removed.
Tests   cli's tests

ID      F-22
What    The proxy GETs' time limits (2 s for /health/liveliness, 5 s
        otherwise) are for the whole request, where Python's were per
        socket read; a stale kept connection is retried by net/http
        itself.
Why     net/http's model, as H-4.
Safe    Stricter: a trickling proxy cannot hold a check.
Tests   desk TestNotes (slow liveliness gives TimeoutError)

ID      F-23
What    A proxy answer over 16 MiB gives the note "Could not read the
        key cards: the proxy's answer is over 16777216 bytes" (Python
        read any size).
Why     Decision 32; risk 11.
Safe    Bounded memory.
Tests   litellm's TooBigError tests; desk keysNote

ID      F-24
What    Server: FrontDesk, without the trailing space Python's
        BaseHTTPRequestHandler wrote ("FrontDesk ").
Why     net/http trims nothing we set; ours has none.
Safe    Clients trim it anyway.
Tests   web TestGoldenServerLocal (headers trimmed in both)

ID      H-1
What    Requests net/http refuses before any handler get its plain
        400, 431 or 505, without JSON or safety headers: a bad
        request line, HTTP/2.0, no Host on HTTP/1.1, two Hosts
        (Python read the first, so with --lan "ip, evil" was served
        and "evil, ip" refused), a
        bad or negative Content-Length (Python: 400 "Send valid
        JSON." on a POST, nothing on a GET), control bytes, headers
        over 1 MiB, a Host with bytes outside ASCII (Python: its 421
        JSON, "Open Front Desk at ...").
Why     net/http checks these first.
Safe    Browsers never send them; nothing is echoed or logged.
Tests   web TestNoRequestLog; TestGoldenServerLocal ("host
        127.0.0.1:{port}\u0131": Go's 400 checked, Python's 421 in the
        golden); twin harness (expected.json); tests/e2e/security
        TestRawHost, TestInputs, TestSecFix (side by side, gate-a.md
        G-4)

ID      H-2
What    An absolute-form target (GET http://host/x) is checked by
        its host, not the Host header (net/http sets Request.Host so).
Why     net/http.
Safe    Still one Host check, then 404 for the path.
Tests   by construction (web.get uses req.Host)

ID      H-3
What    A Host with userinfo (user@10.0.0.5:8040) is net/http's 400.
        Python's host_ok refused it with 421 in local mode; with
        --lan its urlsplit took the part after the @, so
        "evil.test@192.0.2.2:8132" was served, 200 (seen live,
        rawhost.py's case, G-4; --docker runs the same code).
Why     net/http.
Safe    Stricter: refused in every mode. A browser never sends
        userinfo in Host; the rest of the request is checked as
        before.
Tests   web TestGoldenHostOK (the function agrees with Python);
        tests/e2e/security TestRawHost (local and --lan, side by
        side)

ID      H-4
What    A slow sender gets 10 s for the whole request (Python: 10 s
        per read): a connection that drips its request closes after
        10 s, where Python kept it while each byte came within 10 s.
        Writing an answer gets its own 10 s, counted from when the
        answer is ready (each SSE event too), so a mint or revoke
        that takes up to MintWait (30 s) still gets its answer, as
        in Python.
Why     ReadTimeout (decision 10); the write clock starts in
        App.send, not at the request.
Safe    Stricter on reads; a minted key is always shown.
Tests   by construction (web.App.Server); web TestSlowMintAnswers,
        TestMintLateAfterWriteWait (a stand-in mint-key slower than a
        shortened write wait still gets its 200 or 500); C4's
        security suite: the cap drip line (tests/e2e/security,
        TestCaps)

ID      H-5
What    An HTTP/1.0 request gets an "HTTP/1.0" status line, and
        "Connection: keep-alive" when it asked for keep-alive; Python
        always wrote "HTTP/1.1" and no such header.
Why     net/http.
Safe    Correct HTTP; codes, headers and bodies otherwise equal.
Tests   web TestGoldenServerLocal ("http10 host", status parsed)

ID      H-6
What    UTF-16 and UTF-32 bodies, NaN and Infinity are "Send valid
        JSON." (Python decoded them and went on to the next check);
        nesting deeper than 10,000 is refused (Python refused about
        1,000 deep), so 2,000 deep reaches the next check in Go.
Why     Go's decoder.
Safe    Stricter, or the same 400 class; never a counted wrong
        password.
Tests   web TestGoldenServerLocal (differs: "body utf16", "body
        nan", "body inf"; "body deep" equal)

ID      H-7
What    A 304 has no Content-Type and no Content-Length (Python sent
        application/json and 0).
Why     net/http drops them on 304.
Safe    A 304 has no body; browsers ignore both.
Tests   web TestGoldenServerLocal (skipped on 304 only)

ID      H-8
What    A Host with spaces or tabs around it ("127.0.0.1:8040 \t",
        "localhost:9999 ") is read trimmed, so a name the check takes
        passes (200); Python's host_ok saw the spaces and answered
        421.
Why     net/http trims optional whitespace around header values
        (RFC 9110, OWS) before the handler sees them.
Safe    The same names pass and the same names are refused; only the
        spaces around them are ignored.
Tests   web TestGoldenServerLocal ("host 127.0.0.1:{port} \t": Go's
        200 checked, Python's 421 in the golden); twin harness
        (expected.json)

ID      H-9
What    A Host with a space inside it ("192.0.2.2 :8040") is
        net/http's 400 "malformed Host header"; Python's host_ok
        answered its 421 JSON.
Why     net/http checks the Host's characters before any handler.
Safe    Refused either way; nothing is echoed or logged.
Tests   tests/e2e/security TestRawHost ("space in host", local and
        --lan, side by side, gate-a.md G-4)

ID      F-25
What    In host mode a mint or revoke gives up after 25 s
        (MintTimeout, the in-process mint-key's own request wait)
        with "The proxy took too long to answer, and may have made
        the card anyway"; Python ran mint-key.sh, whose curl had no
        wait of its own, so a proxy answering at 25-30 s still gave
        Python the key. Both stop at the 30 s run limit (MintWait).
Why     Spec 9.6: mint-key's request has a wait under the run limit,
        so a slow proxy gets a message, not the 500 path.
Safe    The message says the card may exist and to cancel it; no key
        is shown that the desk did not get. Measured on lvp-w1 with
        a 27 s forwarder: Python 200 at 27.1 s, Go this message at
        25.0 s; with 12 s both gave 200 and the key.
Tests   web TestMintLateAfterWriteWait, TestSlowMintAnswers
```

## Still open (for C4 and the lead)

- Settled by the lead: `internal/cli/cli_test.go` TestStubs is now
  TestCommandHelp, which asks desk, game and fake-ollama for --help.
- The twin harness (16.4) and the browser scripts (16.5) are C4's:
  the page contract, SSE event sequences side by side, caps from
  loopback aliases against Python, and `frontdesk.py --docker`.
- `tests/golden/README.md` (C1's) does not yet list the four
  frontdesk goldens' run lines; each script's docstring has its own.

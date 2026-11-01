# Reference material

What the Python Front Desk and office game were tested with before the
Go port, and what those tests recorded. The port must reproduce it
(spec section 16), so it lives here, in the repo, instead of in a
working folder that goes away. Imported once, for chunk C1 (spec
16.0), as the files were; only these changes were made:

- absolute paths of the machine they ran on became `<scratchpad>` and
  `<repo>`;
- two model names in `litellm/facts.json` became `<hosted-model>` and
  `<local-model>` (no file in the repo names a model but config.yaml);
- nothing that printed a password or a key was brought in, and every
  file is scanned for secrets on every CI run (`internal/archtest`:
  no `sk-` key of 20 or more characters, no Front Desk password, no
  hex run of 32 or more).

The scripts are kept as their authors wrote them: some lines are over
80 columns, and they name the ports (4162, 4163, 4116, 4117) and the
sandbox stack they ran against. Python is needed to run them; their Go
ports come in chunk C4 (`tests/e2e`). At Gate C the Python scripts go;
the recorded outputs, the browser scripts and the `office.db` files
stay as test data.

## security/

From the last security round (`lvgui-security-r4`), run against two
Front Desks side by side (ports 4162 and 4163, `--lan`, loopback
aliases as devices). `lib.py` is their shared helper.

- `cap_vb.py`, `cap4162.out`, `cap4163.out`: 32 connections per
  device, 256 in all, others still served.
- `lock_vb.py`, `lock_vb_p.py`, `lock4162.out`, `lock4163.out`: the
  login lock and slow-down.
- `xss_vb.js`, `xss3_vb.js`, `xss4_vb.js`, `xss4162.out`,
  `xss4163.out`: hostile names in the page (Playwright).
- `evil_vb.js`, `evil2_vb.js`, `text_vb.js`, `tally_xss.js`: other
  sites, page text, tally names.
- `sec_vb.py`: Host, Origin, CSRF, cookies, body limits, scrubbed
  errors.
- `window_vb.py`, `window2_vb.py`, `clock_vb.py`, `clock2_vb.py`: lock
  windows and clock changes.
- `fuzz_vb.py`, `rst_vb.py`, `rst2_vb.py`, `nest_vb.py`, `surr_vb.py`,
  `logflood.py`: odd and reset requests write no log line; the log's
  growth under a flood.
- `misc_vb.py`, `refill_vb.py`, `game_vb.py`, `typo_vb.py`: budgets,
  refills, game names, a mistyped password.
- `hostile_mint.py`, `spend_hostile.py`: cards with hostile names made
  straight in LiteLLM, and spend for them.

`logflood.py`, `lock_vb_p.py`, `hostile_mint.py` and
`spend_hostile.py` are not in 16.0's list, but 16.5 relies on them, so
they came too.

## perf/

From the performance round (`tools-ver`): the measuring scripts and
the before and after numbers that Appendix A of the spec quotes.

- `load.py`, `before-load.txt`, `after-load.txt`: page loads and
  `/api/state` with 1 and 20 viewers.
- `mint_ab.py`, `before-mint.txt`, `after-mint.txt`: mint and cancel
  times.
- `sse_bench2.py`, `before-sse.txt`, `after-sse.txt`: live-stream
  traffic and CPU.
- `tally_bench.py`, `tally_scale.py`, `before-tally.txt`,
  `after-tally.txt`: tally and check times.
- `startup.py`, `chat.py`, `seed.py`: start-up, chat traffic, 200
  seats and a year of history.
- `inputs.py`, `lock.py`, `rawhost.py`, `sec_fix.py`: mint inputs,
  lockout, raw Host headers, the fixes of that round.
- `*.js`: the Playwright page scripts of that round.

`lan.out`, `lan2.out` and the other files that printed a password were
left out.

## browser/

Browser scripts section 16.5 names that were not in `tools-ver`: the
newest copy of each (`race2.js`, `race3.js`, `fallback.js`,
`keepit.js`, `keepit2.js`).

## game/

Three practice weeks of the office game (`lvg-bal-week1..3`): each
week's `office.db`, its board as printed then, and (weeks 1 and 2) the
check of every score against the proxy's spend logs. The boards were
printed by an older `office.py`, whose GOODHART lines read "Token
board #1" where today's read "Most tokens:"; the files are older too
(`daylog` has no `kind` column). `internal/gamestate` opens them read
only and read-write, and shows that today's `office.py board` prints
the same bytes for each of them and for a copy Go writes.

## litellm/

What LiteLLM v1.106.6 was found to do, from the first review rounds:
`facts.json` (claims checked against its source and a running proxy),
`probe_issues.json` (behaviour probed on a live stack), and
`fake_litellm.py` (the first, tiny stand-in). They were the starting
point for `internal/litellm/fakelitellm`, whose answers are now
checked against bodies recorded from a real v1.106.6
(`internal/litellm/fakelitellm/testdata/captures.json`).

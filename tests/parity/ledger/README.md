# The parity ledger

Every way the Go port differs from the Python it replaces, on
purpose, with why, why it is not weaker, and the test that shows it
(spec section 19). Anything not listed here must be the same; a
difference the tests find that is not listed fails them.

One file per chunk that owns the code:

- `base.md` (C1): the shared packages: settings, the proxy client,
  mint-key, Postgres, rules, game state, formatting (B-, C-1, O-1,
  O-7, O-12).
- `frontdesk.md` (C2): Front Desk (F-, H-).
- `game.md` (C3): the office game and the practice model server (O-,
  P-).
- `container.md` (C4): the Docker image and compose (C-).
- `release.md` (C4): the downloads (R-).

Each entry is one block:

```text
ID      H-1
What    what differs
Why     why it changed
Safe    why it is not weaker
Tests   case names in tests/parity/expected.json, or test functions
```

## How the entries are checked

- `tests/parity/expected.json` lists every twin-harness case that may
  differ, with its IDs. The harness (`go test -tags parity
  ./tests/parity`, CI job `parity`) fails on a difference it does not
  list. Its report (PARITY_OUT) shows each listed difference.
- The goldens (`tests/golden/*.py`, read by each package's tests)
  hold Python's own output; a Go test that differs fails.
- The real-stack suites in `tests/e2e` (security, browser, perf, game,
  live) compare Go with what Python recorded or with Python running
  next to it; `tests/e2e/gate-a.md` says which ran and where.

Reading order for a sign-off: `tests/e2e/gate-a.md`, then this
folder, then the report of the last parity run.

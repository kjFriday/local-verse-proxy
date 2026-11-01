# Python goldens

Each script here runs the frozen Python (`gui/frontdesk.py`,
`game/office.py`, loaded by path; nothing of theirs runs on import)
over many cases and writes what Python answered into the owning Go
package's `testdata/golden/`. The Go tests read those files and must
give the same answers. CI never runs the scripts: a golden changes
only when someone runs one again and the diff is reviewed.

Captured with **Python 3.13.17** on Linux, from the commit tagged
`py-reference`. Each file records the version it came from. The Front
Desk image ran Python 3.12; where 3.12 and 3.13 differ, the goldens
follow 3.13 and the ledger says so (tests/parity/ledger/base.md, B-10).

Run from the repo folder, with `-B` so Python writes no
`__pycache__` folder into `gui/` or `game/` (its files would hold this
machine's paths):

```sh
python3 -B tests/golden/pyfmt.py
python3 -B tests/golden/pyrand.py
python3 -B tests/golden/rules.py
python3 -B tests/golden/psql.py
```

- `common.py`: loads the frozen files, writes a golden.
- `pyfmt.py`, to `internal/pyfmt/testdata/golden/pyfmt.json`: format()
  over specs and values (NaNs with the sign bit set among them), 4,000
  random specs over every kind of value with Python's answer or its
  refusal, round(), repr() of 3,000 floats, str() of
  JSON values, strip, split, splitlines, lower, s[:n], repr(str),
  fromisoformat and isoformat (LiteLLM's forms and 5,900 others),
  urlsplit hosts and ports, ipaddress, float // and %, and the C
  library's errno words.
- `pyrand.py`, to `internal/pyrand/testdata/golden/pyrand.json`:
  random.Random for 200 str seeds, every operation the game uses, in
  one order.
- `rules.py`, to `internal/rules/testdata/golden/rules.json` and
  `files/`: load_rules on game/rules.toml and 80-odd edited copies
  (a byte order mark, TOML 1.1 forms, numbers too big to hold, counts
  too big for the close check, wrong types Python played with), with
  their messages; Front Desk's GameSeats over the same and odder files
  (TOML dates and times, text or tables as the roster, a repeated
  seat).
- `psql.py`, to `internal/pgread/testdata/golden/psql.json`: the bytes
  Front Desk's Psql and the game's pg() write to psql, and their
  arguments, with fixed marks.

The other suites in spec 16.2 belong to chunks C2 and C3
(`frontdesk_*.py`, `office_*.py`, `fakeollama.py`) and C4
(`rescore.py`).

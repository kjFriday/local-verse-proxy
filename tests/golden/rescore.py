"""Scores a day the Go game played with Python's score_day, over the
same spend rows in the proxy's database, and compares the scores and
usage rows with the ones Go wrote (spec 16.5, Gate A G-8).

    python3 -B tests/golden/rescore.py [--rules FILE] <proxy folder>
        <office.db> [day...]

<proxy folder> is the one with compose.yaml, where `docker compose
exec db psql` reaches the stack the week was played on (set
COMPOSE_PROJECT_NAME and COMPOSE_FILE as for that stack). <office.db>
is the Go-played file; it is copied and only the copy is changed. With
no days given, every day that has scores is checked. --rules is the
rules file the week was played with (default: game/rules.toml in the
proxy folder). Prints one line
a day; exits 1 if any day differs. Reads the frozen game/office.py of
this repo; prints no key.
"""
import importlib.util
import shutil
import sqlite3
import sys
import tempfile
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent.parent


def load_office(root, db):
    spec = importlib.util.spec_from_file_location(
        "office", REPO / "game" / "office.py")
    office = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(office)
    office.ROOT = root
    office.GAME = root / "game"
    office.DB = db
    return office


def rows(db, sql, *args):
    con = sqlite3.connect(db)
    try:
        return [tuple(r) for r in con.execute(sql, args).fetchall()]
    finally:
        con.close()


def main():
    args = sys.argv[1:]
    rules_file = None
    if args[:1] == ["--rules"] and len(args) > 1:
        rules_file, args = Path(args[1]), args[2:]
    if len(args) < 2:
        print("usage: rescore.py [--rules FILE] <proxy folder> "
              "<office.db> [day...]", file=sys.stderr)
        sys.exit(2)
    root = Path(args[0]).resolve()
    played = Path(args[1]).resolve()
    if rules_file is None:
        rules_file = root / "game" / "rules.toml"
    days = [int(d) for d in args[2:]] or [
        d for (d,) in rows(played, "SELECT DISTINCT day FROM scores "
                           "ORDER BY day")]
    tmp = Path(tempfile.mkdtemp())
    copy = tmp / "office.db"
    shutil.copyfile(played, copy)
    office = load_office(root, copy)
    rules = office.load_rules(rules_file)
    o = office.Office(rules)
    bad = 0
    for day in days:
        o.q("DELETE FROM scores WHERE day = ?", day)
        o.q("DELETE FROM usage WHERE day = ?", day)
        office.score_day(o, day)
        for table, order in (("scores", "quest"), ("usage", "seat")):
            sql = f"SELECT * FROM {table} WHERE day = ? ORDER BY {order}"
            a, b = rows(played, sql, day), rows(copy, sql, day)
            if a == b:
                print(f"day {day}: {table}: {len(a)} rows, equal")
                continue
            bad += 1
            print(f"day {day}: {table}: DIFFERS")
            for x, y in zip(a, b):
                if x != y:
                    print("  Go:    ", x)
                    print("  Python:", y)
            if len(a) != len(b):
                print(f"  Go {len(a)} rows, Python {len(b)}")
    shutil.rmtree(tmp)
    sys.exit(1 if bad else 0)


if __name__ == "__main__":
    main()

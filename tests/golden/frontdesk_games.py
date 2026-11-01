"""Goldens for Front Desk's GameSeats (internal/frontdesk/desk): the
seats it finds and owns() over odd rules.toml and office.db files, and
what it rereads when a file changes. The office.db files go next to the
JSON, in games/, so the Go test reads the very same bytes.
python3 -B tests/golden/frontdesk_games.py"""
import os
import shutil
import sqlite3
import tempfile
import time
from pathlib import Path

import common

OUT = os.path.join(common.REPO, "internal", "frontdesk", "desk",
                   "testdata", "golden", "games")

RULES = {
    "none": None,
    "normal": '[office]\nseat_prefix = "game-"\n\n[roster]\nseats = [\n'
              '  { name = "fern", persona = "x", guild = "a" },\n'
              '  { name = "Moss", persona = "y", guild = "b" },\n]\n',
    "upper-prefix": '[office]\nseat_prefix = "Game-"\n[roster]\n'
                    'seats = [{ name = "Fern" }]\n',
    "no-prefix": '[office]\nseat_prefix = ""\n[roster]\n'
                 'seats = [{ name = "solo" }, { name = "solo" }]\n',
    "number-prefix": '[office]\nseat_prefix = 7\n[roster]\n'
                     'seats = [{ name = 1 }]\n',
    "broken": '[office\nseat_prefix = "game-"\n',
    "no-roster": '[office]\nseat_prefix = "g-"\n',
    "no-name": '[office]\nseat_prefix = "g-"\n[roster]\n'
               'seats = [{ persona = "x" }]\n',
}

DBS = {
    "none": None,
    "players": ["CREATE TABLE players (seat TEXT PRIMARY KEY)",
                "INSERT INTO players VALUES ('game-fern')",
                "INSERT INTO players VALUES ('Other-Seat')",
                "INSERT INTO players VALUES ('\u00c9cole')"],
    "empty-table": ["CREATE TABLE players (seat TEXT PRIMARY KEY)"],
    "no-table": ["CREATE TABLE other (x)"],
    "garbage": b"this is not a database at all, just bytes\n" * 20,
    "zero-bytes": b"",
}

NAMES = ["game-fern", "GAME-FERN", "game-new", "Game-x", "fern", "moss",
         "game-moss", "Other-seat", "other-seat", "\u00e9cole",
         "\u00c9COLE", "solo", "SOLO", "g-1", "71", "7x", "nobody", "",
         "g-", "game-"]


def make_db(path, spec):
    if spec is None:
        return
    if isinstance(spec, bytes):
        with open(path, "wb") as f:
            f.write(spec)
        return
    db = sqlite3.connect(path)
    for s in spec:
        db.execute(s)
    db.commit()
    db.close()


def main():
    fd = common.load("frontdesk", "gui/frontdesk.py")
    shutil.rmtree(OUT, ignore_errors=True)
    os.makedirs(OUT)
    cases = []
    with tempfile.TemporaryDirectory() as tmp:
        for rname, rtext in RULES.items():
            for dname, dspec in DBS.items():
                folder = os.path.join(tmp, rname + "." + dname)
                os.mkdir(folder)
                if rtext is not None:
                    with open(os.path.join(folder, "rules.toml"),
                              "w") as f:
                        f.write(rtext)
                db = os.path.join(folder, "office.db")
                make_db(db, dspec)
                g = fd.GameSeats(Path(folder))
                seats = sorted(g.get())
                owns = {n: g.owns(n) for n in NAMES}
                cases.append({"rules": rname, "rules_text": rtext,
                              "db": dname, "prefix": g.prefix,
                              "seats": seats, "owns": owns})
                if dspec is not None and rname == "none":
                    shutil.copy(db, os.path.join(OUT, dname + ".db"))

        # Rereads: a new player and a new prefix show on the next get.
        folder = os.path.join(tmp, "change")
        os.mkdir(folder)
        rules = os.path.join(folder, "rules.toml")
        with open(rules, "w") as f:
            f.write(RULES["normal"])
        db = os.path.join(folder, "office.db")
        make_db(db, DBS["players"])
        g = fd.GameSeats(Path(folder))
        steps = [sorted(g.get())]
        time.sleep(0.01)
        c = sqlite3.connect(db)
        c.execute("INSERT INTO players VALUES ('late-joiner')")
        c.commit()
        c.close()
        with open(rules, "w") as f:
            f.write(RULES["no-prefix"])
        steps.append(sorted(g.get()))
        os.remove(db)
        steps.append(sorted(g.get()))
        shutil.copy(os.path.join(OUT, "players.db"), db)
        c = sqlite3.connect(db)
        c.execute("INSERT INTO players VALUES ('late-joiner')")
        c.commit()
        c.close()
        shutil.copy(db, os.path.join(OUT, "players-late.db"))
    common.write("frontdesk/desk", "games.json", {
        "python": [common.version()],
        "cases": cases,
        "changes": [{"steps": steps}],
    })


if __name__ == "__main__":
    main()

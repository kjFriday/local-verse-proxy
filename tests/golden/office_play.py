"""Goldens for internal/game's play: post_quests over seeds and days,
single-bot traces with a scripted chat (every body, sleep and row), and
wait_for_day's decisions over scripted proxy states, with a fake clock.
python3 -B tests/golden/office_play.py"""
import contextlib
import hashlib
import io
import json
import os
import re
import sqlite3
import tempfile

import common

office = common.load("office", "game/office.py")
RULES = open(os.path.join(common.REPO, "game", "rules.toml"),
             encoding="utf-8").read()


def rules_text(**changes):
    """game/rules.toml with some `key = value` lines changed: changes
    maps "table.key" to the new value's TOML text."""
    text = RULES
    for path, value in changes.items():
        table, key = path.rsplit(".", 1)
        head = "[" + table + "]"
        start = text.index(head + "\n")
        pat = re.compile(r"^" + re.escape(key) + r"\s*=.*$", re.M)
        m = pat.search(text, start)
        end = m.end()
        if m.group(0).rstrip().endswith("["):  # a list over lines
            end = text.index("\n]", end) + 2
        text = text[:m.start()] + key + " = " + value + text[end:]
    return text


def load_rules(text, tmp):
    path = os.path.join(tmp, "rules.toml")
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)
    return office.load_rules(path)


def typed(v):
    """A value as Go's test reads it: its Python type and str."""
    if v is None:
        return ["n", ""]
    if isinstance(v, bool):
        return ["b", str(v)]
    if isinstance(v, int):
        return ["i", str(v)]
    if isinstance(v, float):
        return ["f", repr(v)]
    return ["s", v]


def dump(db, table):
    cur = db.execute(f"SELECT * FROM {table} ORDER BY rowid")
    return [[typed(v) for v in row] for row in cur.fetchall()]


class Clock:
    """time.time and time.sleep for office.py: sleeps move the clock
    and are recorded."""

    def __init__(self, now, events):
        self.now, self.events = now, events

    def time(self):
        return self.now

    def sleep(self, s):
        self.events.append(["sleep", repr(float(s))])
        self.now += s

    def strftime(self, *a):
        return office_time.strftime(*a)

    def gmtime(self, t=None):
        return office_time.gmtime(self.now if t is None else t)


office_time = office.time


def new_office(tmp, rules, oid="abc123"):
    path = os.path.join(tmp, "office.db")
    if os.path.exists(path):
        os.remove(path)
    db = sqlite3.connect(path)
    db.executescript(office.SCHEMA)
    db.execute("INSERT INTO meta VALUES ('office', ?)", (oid,))
    db.execute("INSERT INTO meta VALUES ('day', '0')")
    for s in sorted({s["guild"] for s in rules["roster"]["seats"]}):
        db.execute("INSERT INTO guilds VALUES (?)", (s,))
    prefix = rules["office"]["seat_prefix"]
    for s in rules["roster"]["seats"]:
        db.execute("INSERT INTO players VALUES (?,?,?,?)",
                   (prefix + s["name"], s["name"], s["persona"],
                    s["guild"]))
    db.commit()
    db.close()
    office.DB = office.GAME = None
    office.DB = __import__("pathlib").Path(path)
    return office.Office(rules)


# --- post_quests ------------------------------------------------------

def quests_cases(tmp):
    out = []
    for name, ch in [("seed-42", {}),
                     ("seed-text", {"sim.seed": '"office"'}),
                     ("seed-float", {"sim.seed": "7.5"}),
                     ("per-seat-3", {"quests.per_seat": "3",
                                     "sim.seed": "1234567"})]:
        text = rules_text(**ch)
        rules = load_rules(text, tmp)
        o = new_office(tmp, rules)
        for day in range(1, 6):
            n = office.post_quests(o, day)
            out.append({"name": name, "rules": text, "day": day, "n": n,
                        "rows": [r for r in dump(o.db, "quests")
                                 if r[1] == ["i", str(day)]]})
        o.db.close()
    return out


# --- bots -------------------------------------------------------------

SCRIPTS = {
    "ok": lambda i: (200, ""),
    "budget-at-9": lambda i: (200, "") if i < 9
    else (422, "budget_exceeded"),
    "budget-400": lambda i: (200, "") if i < 5
    else (400, "budget_exceeded"),
    "flaky": lambda i: (500, "internal_server_error") if i % 4 == 1
    else (200, ""),
    "down": lambda i: (0, "URLError"),
    "rate": lambda i: (200, "") if i < 3 else (429, "rate_limit_error"),
    "late-503": lambda i: (200, "") if i < 20 else (503, ""),
}

BOTS = [
    # name, persona seat, script, seconds to the deadline, changes
    ("frugal-ok", "fern", "ok", 300, {}),
    ("balanced-stream", "bea", "ok", 300, {}),
    ("gearhead-budget", "gus", "budget-at-9", 300, {}),
    ("slacker-bluff", "sid", "ok", 300, {}),
    ("spammy-flaky", "spike", "flaky", 200, {}),
    ("frugal-down", "moss", "down", 300, {}),
    ("balanced-rate", "ben", "rate", 300, {}),
    ("spammy-400", "blitz", "budget-400", 300, {}),
    ("gearhead-late", "gia", "late-503", 400, {}),
    ("deadline-now", "fern", "ok", 1, {}),
    ("few-quests", "bea", "ok", 2000,
     {"quests.per_seat": "1", "sim.seed": '"few"'}),
    ("redo", "fern", "ok", 400,
     {"personas.frugal.skill": "0.1", "quests.max_attempts": "4"}),
    ("seed-text", "spike", "flaky", 120, {"sim.seed": '"xyz"'}),
]


def bot_cases(tmp):
    out = []
    for name, who, script, left, ch in BOTS:
        text = rules_text(**ch)
        rules = load_rules(text, tmp)
        o = new_office(tmp, rules)
        keys = os.path.join(tmp, "keys")
        os.makedirs(keys, exist_ok=True)
        office.KEYS = __import__("pathlib").Path(keys)
        prefix = rules["office"]["seat_prefix"]
        seat = prefix + who
        with open(os.path.join(keys, seat + ".key"), "w") as f:
            f.write("sk-seat-" + who + "\n")
        day = 3
        office.post_quests(o, day)
        events = []
        clock = Clock(1700000000.25, events)
        office.time = clock
        calls = [0]

        def chat(url, key, body):
            status, kind = SCRIPTS[script](calls[0])
            calls[0] += 1
            text = json.dumps(body)
            if calls[0] > 4:  # the rest by hash, to keep the file small
                text = "sha256:" + hashlib.sha256(
                    text.encode()).hexdigest()
            events.append(["chat", url, key, text, status, kind])
            return status, kind

        office.chat = chat
        player = [p for p in o.players() if p["name"] == who][0]
        b = office.Bot(o, player, day, "3.beef", clock.now + left,
                       "http://proxy.test:4000")
        b.run()
        office.time = office_time
        out.append({"name": name, "rules": text, "seat": seat,
                    "day": day, "tag": "3.beef", "start": repr(1700000000.25),
                    "left": left, "script": script, "events": events,
                    "end": repr(clock.now),
                    "quests": [r for r in dump(o.db, "quests")
                               if r[1] == ["i", "3"]],
                    "completions": dump(o.db, "completions"),
                    "daylog": dump(o.db, "daylog")})
        o.db.close()
    return out


# --- wait_for_day -----------------------------------------------------

def state(rows):
    """A seat_state answer: [seat, spend, reset_at] as JSON text."""
    return json.dumps([{"seat": s, "spend": sp, "reset_at": at}
                       for s, sp, at in rows])


T0 = 1700000040.0  # a 2m boundary is at 1700000040 (14166667 * 120)


def seats(sp, at, n=3, skip=()):
    names = ["game-bea", "game-ben", "game-blitz"][:n]
    return [(s, sp, at) for s in names if s not in skip]


WAITS = [
    # name, now, answers (JSON texts, used in order, last repeats),
    # revoke answers, mint answers
    ("fresh", T0 + 5.5, [state(seats(0, T0 + 120))], [], []),
    ("fresh-spent", T0 + 5.5,
     [state(seats(0.0001, T0 + 120)), state(seats(0, T0 + 240))],
     [], []),
    ("fresh-float-epoch", T0 + 29.9,
     [state(seats(0.0, T0 + 120.000001))], [], []),
    ("quarter-past", T0 + 30.5,
     [state(seats(0, T0 + 120)), state(seats(0, T0 + 240))], [], []),
    ("wait-50", T0 + 69.6,
     [state(seats(0.5, T0 + 120)), state(seats(0, T0 + 240))], [], []),
    ("wait-under-1", T0 + 119.4,
     [state(seats(1e-05, T0 + 120)), state(seats(0, T0 + 120)),
      state(seats(0, T0 + 120)), state(seats(0, T0 + 240))], [], []),
    ("past", T0 + 125.0,
     [state(seats(2, T0 + 120)), state(seats(0, T0 + 240))], [], []),
    ("polls-3", T0 + 100.0,
     [state(seats(1, T0 + 120))] + [state(seats(0, T0 + 120))] * 3 +
     [state(seats(0, T0 + 240))], [], []),
    ("seat-gone", T0 + 100.0,
     [state(seats(1, T0 + 120)), state(seats(0, T0 + 240,
                                             skip=["game-ben"])),
      state(seats(0, T0 + 240))], [], []),
    ("late-remint", T0 + 100.0,
     [state(seats(1, T0 + 120)), state(seats(0, T0 + 121))],
     ["revoked", "no key", "revoked"], [None, None, None]),
    ("late-some", T0 + 100.0,
     [state(seats(1, T0 + 120)),
      state([("game-bea", 0, T0 + 240), ("game-ben", 0, T0 + 120),
             ("game-blitz", 0, T0 + 240)])],
     ["revoked"], [None]),
    ("late-revoke-fails", T0 + 100.0,
     [state(seats(1, T0 + 120)), state(seats(0, T0 + 120))],
     ["proxy said no"], []),
    ("late-mint-fails", T0 + 100.0,
     [state(seats(1, T0 + 120)), state(seats(0, T0 + 120))],
     ["revoked", "no key"], [None, "Key with alias exists"]),
    ("missing", T0 + 5.0, [state(seats(0, T0 + 120, skip=["game-ben",
                                                          "game-bea"]))],
     [], []),
    ("min-reset", T0 + 10.0,
     [state([("game-bea", 0, T0 + 240), ("game-ben", 0, T0 + 120),
             ("game-blitz", 0, T0 + 360)])], [], []),
]


def wait_cases(tmp):
    text = rules_text(**{"roster.seats": '[{name = "bea", persona = '
                         '"balanced", guild = "owls"}, {name = "ben", '
                         'persona = "balanced", guild = "bees"}, '
                         '{name = "blitz", persona = "spammy", '
                         'guild = "bees"}]'})
    rules = load_rules(text, tmp)
    out = []
    for name, now, answers, revokes, mints in WAITS:
        o = new_office(tmp, rules)
        events = []
        clock = Clock(now, events)
        office.time = clock
        left = list(answers)

        def pg(sql):
            a = left.pop(0) if len(left) > 1 else left[0]
            events.append(["pg", sql])
            return json.loads(a)

        rv, mn = list(revokes), list(mints)

        def revoke(alias):
            events.append(["revoke", alias])
            return rv.pop(0)

        def mint(alias, rules):
            events.append(["mint", alias])
            return mn.pop(0)

        office.pg, office.revoke, office.mint = pg, revoke, mint
        stdout, stderr = io.StringIO(), io.StringIO()
        result = None
        with contextlib.redirect_stdout(stdout), \
                contextlib.redirect_stderr(stderr):
            try:
                started, ends = office.wait_for_day(o, o.players(), 2)
                result = [repr(started), repr(ends)]
            except SystemExit:
                pass
        office.time = office_time
        out.append({"name": name, "rules": text, "now": repr(now),
                    "answers": answers, "revokes": revokes,
                    "mints": mints, "events": events,
                    "stdout": stdout.getvalue(),
                    "stderr": stderr.getvalue(), "result": result})
        o.db.close()
    return out


def main():
    with tempfile.TemporaryDirectory() as tmp:
        data = {"python": common.version(),
                "quests": quests_cases(tmp),
                "bots": bot_cases(tmp),
                "waits": wait_cases(tmp)}
    common.write("game", "office_play.json", data)


if __name__ == "__main__":
    main()

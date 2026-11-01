"""Goldens for internal/game's scoring and boards: score_day over
made-up days (office.db before and after, and the spend rows the proxy
gave), board, board --day, board --seat and day_summary on those weeks
and on the reference weeks, the commands' texts with a fake mint-key,
every die message, and money, seconds, clock, rank_corr and positive.
python3 -B tests/golden/office_score.py"""
import argparse
import contextlib
import io
import itertools
import json
import os
import pathlib
import random
import shutil
import sqlite3
import stat
import sys
import tempfile

import common
from office_play import (Clock, dump, load_rules, new_office, office,
                         office_time, rules_text)

TABLES = ["meta", "guilds", "players", "quests", "completions", "days",
          "daylog", "usage", "scores"]


def dump_all(db):
    return {t: dump(db, t) for t in TABLES}


def run(fn, *a):
    """fn(*a) with its standard output and error, and whether it
    died."""
    out, err = io.StringIO(), io.StringIO()
    code = 0
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
        try:
            fn(*a)
        except SystemExit as e:
            code = e.code if isinstance(e.code, int) else 1
    return {"stdout": out.getvalue(), "stderr": err.getvalue(),
            "code": code}


# --- made-up weeks ---------------------------------------------------

KINDS = ["budget_exceeded", "", "rate_limit_error", "URLError",
         "budget_exceeded"]


def spend_value(gen):
    return gen.choice([0, 0.0, round(gen.uniform(1e-5, 0.02), 9),
                       gen.uniform(0.0001, 0.009), 1e-05, 0.012345678])


def make_day(o, gen, day, cases, flush):
    """One day's play, made up; then score_day, recorded."""
    rules = o.rules
    office.post_quests(o, day)
    players = o.players()
    quests = o.q("SELECT * FROM quests WHERE day = ? ORDER BY id", day)
    rows = []  # the spend rows the proxy gives
    expected = 0
    for q in quests:
        p = gen.choice(players)
        fate = gen.choice(["open", "done", "done", "done", "unfinished",
                           "working"])
        if fate == "open":
            continue
        o.q("UPDATE quests SET seat = ?, state = ? WHERE id = ?",
            p["seat"], fate, q["id"])
        calls = gen.choice([0, 1, q["calls"] - 1, q["calls"],
                            q["calls"] + gen.randint(0, 6)])
        if fate == "done":
            o.q("INSERT INTO completions VALUES (?,?,?,?,?,?)", q["id"],
                p["seat"], day, gen.randint(1, 3),
                int(gen.random() < 0.8), calls)
        if calls and gen.random() < 0.9:
            seat_tag = "seat:" + p["seat"]
            if gen.random() < 0.05:
                seat_tag = "seat:" + gen.choice(players)["seat"]
            rows.append({"alias": p["seat"], "seat": seat_tag,
                         "quest": "quest:" + q["id"], "calls": calls,
                         "spend": spend_value(gen),
                         "tokens": gen.randint(50, 9000) * calls})
            expected += calls
    # Calls without a quest tag, and another key's calls.
    for p in players[:3]:
        if gen.random() < 0.5:
            rows.append({"alias": p["seat"], "seat": None, "quest": None,
                         "calls": 2, "spend": spend_value(gen),
                         "tokens": 777})
    rows.append({"alias": "someone-else", "seat": "seat:x",
                 "quest": "quest:" + quests[0]["id"], "calls": 5,
                 "spend": 0.5, "tokens": 5000})
    gen.shuffle(rows)
    o.q("UPDATE quests SET state = 'expired' WHERE day = ? AND state = "
        "'open'", day)
    o.q("UPDATE quests SET state = 'unfinished' WHERE day = ? AND state "
        "= 'working'", day)
    started = 1700000000.0 + day * 120 + gen.random()
    for p in players:
        refused = int(gen.random() < 0.3)
        out_at = started + gen.uniform(0, 100) if refused else None
        o.q("INSERT OR REPLACE INTO daylog VALUES (?,?,?,?,?,?,?)", day,
            p["seat"], gen.randint(0, 40), refused,
            gen.choice([422, 0, 429, 500]) if refused else 0,
            gen.choice(KINDS) if refused else "", out_at)
    worked = gen.uniform(40, 90)
    o.q("INSERT INTO days VALUES (?,?,?,?,?,NULL,NULL)", day,
        "%d.%04x" % (day, gen.randrange(65536)), started, worked,
        expected)
    before = dump_all(o.db)
    counts = [max(0, expected - 7), expected - 1, expected]
    if flush == "slow":
        counts = [0] * 40  # never all in: flush_wait ends it
    elif flush == "now":
        counts = [expected + 3]
    answers = [json.dumps([{"n": n}]) for n in counts]
    answers.append(json.dumps(rows))
    events = []
    clock = Clock(started + worked, events)
    left = list(answers)

    def pg(sql):
        events.append(["pg", sql])
        if "GROUP BY" not in sql:
            while len(left) > 2:
                return json.loads(left.pop(0))
            return json.loads(left[0])
        return json.loads(left[-1])

    office.pg, office.time = pg, clock
    office.score_day(o, day)
    office.time = office_time
    o.meta("day", day)
    cases.append({"day": day, "before": before, "answers": answers,
                  "now": repr(started + worked), "events": events,
                  "after": {t: dump(o.db, t) for t in
                            ("days", "usage", "scores")}})


WEEKS = [
    # name, rules changes, seed, days, flush per day
    ("full", {}, 1, 5, ["fast", "now", "slow", "fast", "fast"]),
    ("small", {"roster.seats": '[{name = "ann", persona = "frugal", '
               'guild = "owls"}, {name = "bo", persona = "spammy", '
               'guild = "owls"}, {name = "cy", persona = "slacker", '
               'guild = "bees"}]', "quests.per_seat": "4",
               "sim.seed": '"small"'}, 2, 3, ["fast"] * 3),
    ("float-rules", {"mana.daily": "1500.0", "mana.usd": "0.00002",
                     "day.per_week": "3", "score.streak_step": "0.25",
                     "score.streak_cap": "0.4", "score.eff_max": "2"},
     3, 3, ["now"] * 3),
    ("one-day", {"quests.per_seat": "2"}, 4, 1, ["now"]),
]


def board_runs(o, names, days, summaries):
    out = []
    for d in [None] + list(range(1, days + 2)):
        out.append({"fn": "board", "day": d,
                    **run(office.board, o, d)})
    for n in names + ["nobody", "game-" + names[0]]:
        for d in (None, 1):
            out.append({"fn": "seat", "seat": n, "day": d,
                        **run(office.seat_board, o, n, d)})
    if summaries:
        for d in range(1, days + 1):
            out.append({"fn": "summary", "day": d,
                        **run(office.day_summary, o, d)})
    return out


def week_cases(tmp):
    out = []
    for name, ch, seed, days, flush in WEEKS:
        text = rules_text(**ch)
        rules = load_rules(text, tmp)
        o = new_office(tmp, rules, "%06x" % (seed * 99991))
        if name == "full":  # a guild with nobody in it
            o.q("INSERT INTO guilds VALUES ('zebras')")
        gen = random.Random(seed)
        cases = []
        for day in range(1, days + 1):
            make_day(o, gen, day, cases, flush[day - 1])
        names = [s["name"] for s in rules["roster"]["seats"]]
        runs = board_runs(o, names, days, True)
        quiet = load_rules(rules_text(**ch, **{
            "board.individuals": "false"}), tmp)
        o.rules = quiet
        runs.append({"fn": "board", "day": None, "individuals": False,
                     **run(office.board, o, None)})
        out.append({"name": name, "rules": text, "days": cases,
                    "db": dump_all(o.db), "runs": runs})
        o.db.close()
    return out


def reference_cases(tmp):
    rules = office.load_rules(os.path.join(common.REPO, "game",
                                           "rules.toml"))
    out = []
    folder = os.path.join(common.REPO, "tests", "reference", "game")
    for week in (1, 2, 3):
        src = os.path.join(folder, "lvg-bal-week%d-office.db" % week)
        dst = os.path.join(tmp, "ref.db")
        shutil.copy(src, dst)
        office.DB = pathlib.Path(dst)
        o = office.Office(rules)
        names = [s["name"] for s in rules["roster"]["seats"]]
        out.append({"file": os.path.basename(src),
                    "runs": board_runs(o, names, 5, False)})
        o.db.close()
    return out


# --- small helpers ---------------------------------------------------

def helper_cases():
    gen = random.Random(7)
    money = [0, 0.0, 1, 10, 0.015, 0.00001, 1e-05, 0.123456789,
             1500 * 0.00001, 0.1 + 0.2, 123456.5, 2.5e-09, 1e-08,
             0.000000005, 0.0000000049, 99.999999995]
    money += [gen.uniform(0, 3) for _ in range(200)]
    money += [gen.randint(0, 3000) * gen.choice([1e-05, 2e-06, 0.001])
              for _ in range(200)]
    durations = ["1s", "35s", "2m", "1h", "1d", "60s", "8d", "0s", "01m",
                 "1w", "", "1", "s", "1.5m", " 1m", "1m ", "10M",
                 "999999999999d", "٣m", "1m\n"]
    clocks = [0, 1, 59.999, 1700000000.25, 1700000160.0, -0.5, 86399.9,
              1e9 + 0.5]
    clocks += [gen.uniform(0, 4e9) for _ in range(100)]
    perms = []
    for n in range(0, 7):
        a = ["s%d" % i for i in range(n)]
        for _ in range(8):
            b = a[:]
            gen.shuffle(b)
            perms.append([a, b, repr(office.rank_corr(a, b))])
    a = ["s%d" % i for i in range(40)]
    for _ in range(20):
        b = a[:]
        gen.shuffle(b)
        perms.append([a, b, repr(office.rank_corr(a, b))])
    texts = ["1", "2", "0", "-1", " 3 ", "+4", "1_0", "1__0", "_1", "x",
             "", "1.5", "٣", "007", "99999999999999999999", "\t5\n",
             "1e3", "0x10", "١٢"]
    pos = []
    for t in texts:
        try:
            pos.append([t, office.positive(t), ""])
        except argparse.ArgumentTypeError as e:
            pos.append([t, 0, str(e)])
    secs = []
    for d in durations:
        r = run(lambda: secs.append([d, office.seconds(d), ""]))
        if r["code"]:
            secs.append([d, 0, r["stderr"]])
    return {"money": [[repr(m) if isinstance(m, float) else m,
                       office.money(m)] for m in money],
            "seconds": secs,
            "clock": [[repr(float(c)), office.clock(c)] for c in clocks],
            "rank_corr": perms, "positive": pos}


# --- commands, with a fake mint-key ----------------------------------

FAKE = r'''#!PYTHON
import json, os, sys
with open(os.environ["GOLDEN_MINT_LOG"], "a") as f:
    f.write(json.dumps({"argv": sys.argv[1:], "dir": os.getcwd()}) + "\n")
script = json.loads(os.environ["GOLDEN_MINT"])
a = sys.argv[1:]
key = a[1] if a and a[0] == "--revoke-seat" else a[0]
ans = script.get(key, script.get("*", {}))
sys.stdout.write(ans.get("out", ""))
sys.stderr.write(ans.get("err", ""))
sys.exit(ans.get("code", 0))
'''

COMMANDS = [
    # name, command, argv extras, mint script, before
    ("setup", "setup", {},
     {"*": {"out": "sk-made-up-key-for-a-test\n"}}, None),
    ("setup-twice", "setup", {}, {"*": {"out": "sk-x\n"}}, "setup"),
    ("setup-mint-fails", "setup", {},
     {"game-bea": {"err": "  Key with alias 'game-bea' already exists.\n",
                   "code": 1},
      "*": {"out": "sk-ok\n"}}, None),
    ("setup-no-key", "setup", {}, {"*": {"out": "nope\n"}}, None),
    ("setup-silent-fail", "setup", {}, {"*": {"code": 3}}, None),
    ("reset-none", "reset", {},
     {"*": {"err": "{'error': 'No keys found'}\n", "code": 1}}, None),
    ("reset-all", "reset", {}, {"*": {}}, "setup"),
    ("reset-one", "reset", {},
     {"*": {"err": "No keys found\n", "code": 1}, "game-fern": {}},
     None),
    ("reset-fails", "reset", {},
     {"*": {"err": "cannot reach the proxy\n  at\thttp://x\n",
            "code": 1}, "game-bea": {}}, "setup"),
    ("reset-exit", "reset", {}, {"*": {"code": 7}}, None),
    ("run-no-office", "run", {"days": 1, "quiet": False}, {}, None),
    ("board-no-office", "board", {"day": None, "seat": None}, {}, None),
    ("run-week-over", "run", {"days": 2, "quiet": True}, {},
     "week-over"),
    ("board-no-day", "board", {"day": None, "seat": None}, {}, "setup"),
    ("board-seat-no-day", "board", {"day": None, "seat": "fern"}, {},
     "setup"),
    ("board-day-9", "board", {"day": 9, "seat": None}, {}, "week-over"),
    ("board-seat-day-9", "board", {"day": 9, "seat": "fern"}, {},
     "week-over"),
    ("board-seat-unknown", "board", {"day": None, "seat": "zed"}, {},
     "week-over"),
    ("board-seat-quote", "board", {"day": None, "seat": "o'neil"}, {},
     "week-over"),
]


def command_cases(tmp):
    root = os.path.join(tmp, "cmd")
    out = []
    fake = os.path.join(tmp, "fake-mint")
    with open(fake, "w") as f:
        f.write(FAKE.replace("PYTHON", sys.executable))
    os.chmod(fake, os.stat(fake).st_mode | stat.S_IEXEC)
    for name, cmd, extra, script, before in COMMANDS:
        shutil.rmtree(root, ignore_errors=True)
        game = os.path.join(root, "game")
        os.makedirs(game)
        shutil.copy(os.path.join(common.REPO, "game", "rules.toml"), game)
        office.ROOT, office.GAME = pathlib.Path(root), pathlib.Path(game)
        office.DB = office.GAME / "office.db"
        office.KEYS = office.GAME / "keys"
        office.MINT = pathlib.Path(fake)
        office.secrets.token_hex = lambda n: "ab" * n
        events = []
        office.time = Clock(1700000000.75, events)
        log = os.path.join(tmp, "mint.log")
        rules = office.load_rules(office.GAME / "rules.toml")
        if before:
            os.environ["GOLDEN_MINT"] = json.dumps({"*": {"out": "sk-k\n"}})
            os.environ["GOLDEN_MINT_LOG"] = log
            run(office.cmd_setup, rules, None)
        if before == "week-over":
            db = sqlite3.connect(office.DB)
            db.execute("UPDATE meta SET v = '5' WHERE k = 'day'")
            db.commit()
            db.close()
        if os.path.exists(log):
            os.remove(log)
        os.environ["GOLDEN_MINT"] = json.dumps(script)
        os.environ["GOLDEN_MINT_LOG"] = log
        fn = {"setup": office.cmd_setup, "run": office.cmd_run,
              "board": office.cmd_board, "reset": office.cmd_reset}[cmd]
        r = run(fn, rules, argparse.Namespace(**extra))
        calls = []
        if os.path.exists(log):
            with open(log) as f:
                calls = [json.loads(line)["argv"] for line in f]
        keys = sorted(os.listdir(office.KEYS)) if office.KEYS.exists() \
            else []
        files = sorted(os.listdir(game))
        db = None
        if office.DB.exists():
            c = sqlite3.connect(office.DB)
            db = {t: dump(c, t) for t in ("meta", "guilds", "players")}
            c.close()
        office.time = office_time
        out.append({"name": name, "cmd": cmd, "args": extra,
                    "script": script, "before": before, **r,
                    "mint_calls": calls, "keys": keys, "files": files,
                    "db": db})
    return out


def main():
    with tempfile.TemporaryDirectory() as tmp:
        data = {"python": common.version(),
                "weeks": week_cases(tmp),
                "reference": reference_cases(tmp),
                "helpers": helper_cases(),
                "commands": command_cases(tmp)}
    common.write("game", "office_score.json", data)


if __name__ == "__main__":
    main()

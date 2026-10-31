#!/usr/bin/env python3
"""Office game on top of the proxy. Seats are players, AI budget is
mana, finished work is the score. See game/README.md.

  python3 game/office.py setup         mint game seats, make office.db
  python3 game/office.py run [-d N]    play N practice days with bots
  python3 game/office.py board         both leaderboards, side by side
  python3 game/office.py board -s fern one seat, quest by quest
  python3 game/office.py reset         weekly reset: revoke and wipe

Python 3.11 or newer, standard library only. The game never sees the
master key: it mints with ./mint-key.sh, calls the proxy with seat
keys, and reads the proxy's Postgres in a READ ONLY transaction
through psql.
"""
import argparse
import json
import math
import os
import random
import re
import secrets
import signal
import sqlite3
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
from collections import namedtuple
from pathlib import Path

try:
    import tomllib  # new in Python 3.11
except ModuleNotFoundError:
    sys.exit("office.py needs Python 3.11 or newer (python3 --version)")

GAME = Path(__file__).resolve().parent
ROOT = GAME.parent
DB = GAME / "office.db"
KEYS = GAME / "keys"
MINT = ROOT / "mint-key.sh"
UNITS = {"s": 1, "m": 60, "h": 3600, "d": 86400}
WORDS = ("please draft review the quarterly report and fix every typo in "
         "section two then summarise the meeting notes for the team with "
         "clear next steps owners and dates").split()

SCHEMA = """
CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT);
CREATE TABLE guilds (name TEXT PRIMARY KEY);
CREATE TABLE players (seat TEXT PRIMARY KEY, name TEXT UNIQUE,
  persona TEXT, guild TEXT REFERENCES guilds(name));
-- state: open, working, done, unfinished, expired
CREATE TABLE quests (id TEXT PRIMARY KEY, day INT, size TEXT,
  value REAL, calls INT, par_mana REAL, seat TEXT, state TEXT);
-- What a player says it handed in. The scorekeeper checks it.
CREATE TABLE completions (quest TEXT PRIMARY KEY, seat TEXT, day INT,
  attempts INT, approved INT, calls INT);
CREATE TABLE days (day INT PRIMARY KEY, tag TEXT, started REAL,
  worked REAL, expected INT, logged INT, flush_s REAL);
-- code and kind: the proxy's last refusal (kind: error.type).
CREATE TABLE daylog (day INT, seat TEXT, ok INT, refused INT,
  code INT, kind TEXT, out_at REAL, PRIMARY KEY (day, seat));
-- Filled from LiteLLM_SpendLogs at day end.
CREATE TABLE usage (day INT, seat TEXT, calls INT, tokens INT,
  mana REAL, PRIMARY KEY (day, seat));
CREATE TABLE scores (day INT, quest TEXT PRIMARY KEY, seat TEXT,
  calls INT, mana REAL, eff REAL, bonus REAL, points REAL,
  verdict TEXT);
"""

# One handed-in (or unfinished) quest, as the scorekeeper sees it.
Result = namedtuple("Result", "quest seat calls mana eff verdict value")


def die(msg):
    print(msg, file=sys.stderr)
    sys.exit(1)


def seconds(text):
    m = re.fullmatch(r"([1-9][0-9]*)([smhd])", str(text))
    if not m:
        die(f"bad duration {text!r}: use a whole number and s, m, h or d")
    return int(m[1]) * UNITS[m[2]]


def clock(t):
    return time.strftime("%H:%M:%S", time.gmtime(t))


def money(usd):
    return f"{usd:.8f}".rstrip("0").rstrip(".")


# --- Rules -----------------------------------------------------------

def load_rules(path):
    try:
        with open(path, "rb") as f:
            r = tomllib.load(f)
    except (OSError, tomllib.TOMLDecodeError) as e:
        die(f"cannot read {path}: {e}")
    length = r["day"]["length"]
    r["day"]["secs"] = seconds(length)
    n, unit = int(length[:-1]), length[-1]
    # LiteLLM lines resets up with the clock, so a day must split the
    # next bigger unit evenly, or days would come out uneven.
    if {"s": 60, "m": 60, "h": 24, "d": 1}[unit] % n:
        die(f"day.length {length!r} must split the next unit evenly, "
            "like 1m, 2m, 5m, 1h or 1d")
    r["day"]["close_s"] = seconds(r["day"]["close"])
    if r["day"]["close_s"] * 2 > r["day"]["secs"]:
        die("day.close must be under half of day.length")
    names = set()
    for s in r["roster"]["seats"]:
        if not re.fullmatch(r"[a-z][a-z0-9]{0,5}", s["name"]):
            die(f"seat name {s['name']!r}: 1-6 lowercase letters/digits")
        if s["persona"] not in r["personas"]:
            die(f"seat {s['name']}: no persona {s['persona']!r}")
        if s["name"] in names:
            die(f"seat name {s['name']!r} is used twice")
        names.add(s["name"])
    if not re.fullmatch(r"[a-z0-9-]*", r["office"]["seat_prefix"]):
        die("office.seat_prefix: lowercase letters, digits and - only")
    return r


# --- The proxy and its database --------------------------------------

def proxy_url():
    url = os.environ.get("LVG_PROXY_URL")
    if not url:
        # Read only this one line; the rest of seats.env stays put.
        try:
            for line in (ROOT / "seats.env").read_text().splitlines():
                if line.startswith("PROXY_URL="):
                    url = line.split("=", 1)[1].strip()
        except OSError:
            pass
    return (url or "http://localhost:4000").rstrip("/")


def lit(s):
    return "'" + str(s).replace("'", "''") + "'"


def pg(sql):
    """One SELECT on the proxy's Postgres, READ ONLY. Returns rows."""
    script = ("BEGIN TRANSACTION READ ONLY;\n"
              f"SELECT coalesce(json_agg(q), '[]') FROM ({sql}) q;\n"
              "COMMIT;\n")
    cmd = ["docker", "compose", "exec", "-T", "db", "psql", "-U",
           "litellm", "-d", "litellm", "-X", "-q", "-A", "-t", "-v",
           "ON_ERROR_STOP=1"]
    p = subprocess.run(cmd, input=script, capture_output=True,
                       text=True, cwd=ROOT)
    if p.returncode != 0:
        die("cannot read the proxy database: " + p.stderr.strip())
    return json.loads(p.stdout.strip() or "[]")


def seat_state(seats):
    return {r["seat"]: r for r in pg(
        'SELECT key_alias AS seat, spend, '
        'extract(epoch FROM budget_reset_at) AS reset_at '
        'FROM "LiteLLM_VerificationToken" '
        f'WHERE key_alias IN ({", ".join(lit(s) for s in seats)})')}


def spend_where(office, tag):
    # Tagged, answered requests of one game day. The time bound only
    # keeps the scan short.
    tags = json.dumps([f"lvg:{office}", f"day:{tag}"])
    return (f"request_tags @> {lit(tags)}::jsonb AND status = 'success' "
            "AND \"startTime\" > (now() AT TIME ZONE 'utc') "
            "- interval '9 days'")


def mint(alias, rules):
    usd = rules["mana"]["daily"] * rules["mana"]["usd"]
    cmd = [str(MINT), alias,
           "--models", ",".join(rules["office"]["models"]),
           "--budget", money(usd),
           "--duration", rules["office"]["key_life"],
           "--budget-duration", rules["day"]["length"]]
    p = subprocess.run(cmd, capture_output=True, text=True, cwd=ROOT)
    key = p.stdout.strip()
    if p.returncode != 0 or not key.startswith("sk-"):
        return p.stderr.strip() or "mint-key.sh gave no key"
    KEYS.mkdir(mode=0o700, exist_ok=True)
    fd = os.open(KEYS / f"{alias}.key",
                 os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        f.write(key + "\n")
    return None


def revoke(alias):
    """Revoke one seat: "revoked", "no key", or why it failed. The key
    file goes only once the proxy has no key for the seat."""
    p = subprocess.run([str(MINT), "--revoke-seat", alias],
                       capture_output=True, text=True, cwd=ROOT)
    err = " ".join(p.stderr.split())
    if p.returncode == 0:
        result = "revoked"
    elif "No keys found" in err:  # LiteLLM's 404 for an unknown seat
        result = "no key"
    else:
        return err or f"mint-key.sh exit {p.returncode}"
    (KEYS / f"{alias}.key").unlink(missing_ok=True)
    return result


def chat(url, key, body):
    req = urllib.request.Request(
        url + "/v1/chat/completions", data=json.dumps(body).encode(),
        headers={"Authorization": "Bearer " + key,
                 "Content-Type": "application/json",
                 "User-Agent": "lvg-office/1"})
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            r.read()  # streamed or not, read to the end
            return r.status, ""
    except urllib.error.HTTPError as e:
        try:
            kind = json.loads(e.read())["error"]["type"] or ""
        except (ValueError, KeyError, TypeError):
            kind = ""
        return e.code, str(kind)  # never the message: it names the key
    except OSError as e:
        return 0, type(e).__name__


# --- Game state ------------------------------------------------------

class Office:
    def __init__(self, rules):
        if not DB.exists():
            die("no office yet: run `python3 game/office.py setup`")
        self.rules = rules
        self.db = sqlite3.connect(DB, check_same_thread=False,
                                  isolation_level=None)
        self.db.row_factory = sqlite3.Row
        self.lock = threading.Lock()
        self.id = self.meta("office")

    def q(self, sql, *args):
        with self.lock:
            return self.db.execute(sql, args).fetchall()

    def meta(self, k, v=None):
        if v is None:
            row = self.q("SELECT v FROM meta WHERE k = ?", k)
            return row[0]["v"] if row else None
        self.q("INSERT OR REPLACE INTO meta VALUES (?, ?)", k, str(v))

    def players(self):
        return self.q("SELECT * FROM players ORDER BY seat")

    def claim(self, seat, day, rng):
        with self.lock:
            ids = [r[0] for r in self.db.execute(
                "SELECT id FROM quests WHERE day = ? AND state = 'open' "
                "ORDER BY id", (day,))]
            if not ids:
                return None
            qid = rng.choice(ids)
            self.db.execute("UPDATE quests SET state = 'working', "
                            "seat = ? WHERE id = ?", (seat, qid))
            return self.db.execute("SELECT * FROM quests WHERE id = ?",
                                   (qid,)).fetchone()


def post_quests(office, day):
    rules = office.rules
    sizes = rules["quests"]["sizes"]
    names = sorted(sizes)
    rng = random.Random(f"{rules['sim']['seed']}-quests-{day}")
    n = rules["quests"]["per_seat"] * len(rules["roster"]["seats"])
    for i in range(n):
        size = rng.choices(names, [sizes[s]["weight"] for s in names])[0]
        s = sizes[size]
        office.q("INSERT INTO quests VALUES (?,?,?,?,?,?,NULL,'open')",
                 f"d{day}q{i + 1:02d}", day, size, s["value"],
                 s["calls"], s["par_mana"])
    return n


# --- Bots ------------------------------------------------------------

class Bot(threading.Thread):
    def __init__(self, office, player, day, tag, deadline, url):
        super().__init__(daemon=True)
        self.office, self.day, self.tag = office, day, tag
        self.deadline, self.url = deadline, url
        self.seat = player["seat"]
        self.persona = office.rules["personas"][player["persona"]]
        self.rng = random.Random(
            f"{office.rules['sim']['seed']}-{day}-{player['name']}")
        self.key = (KEYS / f"{self.seat}.key").read_text().strip()
        self.ok = self.refused = self.code = 0
        self.kind = ""
        self.out_at = None
        self.stopped = False

    def run(self):
        try:
            done = 0
            while done < self.persona["quests"] and not self.stopped:
                q = self.office.claim(self.seat, self.day, self.rng)
                if q is None:
                    break  # no quests left today
                self.work(q)
                done += 1
        finally:
            self.office.q(
                "INSERT OR REPLACE INTO daylog VALUES (?,?,?,?,?,?,?)",
                self.day, self.seat, self.ok, self.refused, self.code,
                self.kind, self.out_at)

    def work(self, q):
        ps, rng, rules = self.persona, self.rng, self.office.rules
        model = "pro" if rng.random() < ps["pro"] else "local"
        steps, per_step, retry = q["calls"], ps["per_step"], ps["retry"]
        if rng.random() < ps["bluff"]:
            steps, per_step, retry = 1, 1, 0.0  # hand in after one call
        chance = ps["skill"]
        if model == "pro":
            chance += rules["sim"]["pro_boost"]
        attempts = calls = 0
        while True:
            attempts += 1
            for step in range(steps):
                n = per_step + (rng.random() < retry)
                for _ in range(n):
                    if not self.call(q, model, step):
                        self.office.q("UPDATE quests SET state = "
                                      "'unfinished' WHERE id = ?", q["id"])
                        return
                    calls += 1
            approved = rng.random() < min(0.98, chance)
            if approved or attempts >= rules["quests"]["max_attempts"]:
                break
            steps = math.ceil(q["calls"] * rules["quests"]["redo_share"])
            per_step, retry = ps["per_step"], ps["retry"]
        self.office.q("UPDATE quests SET state = 'done' WHERE id = ?",
                      q["id"])
        self.office.q("INSERT INTO completions VALUES (?,?,?,?,?,?)",
                      q["id"], self.seat, self.day, attempts,
                      int(approved), calls)

    def call(self, q, model, step):
        ps = self.persona
        pause = self.rng.uniform(*ps["pause"])
        if self.stopped or time.time() + pause >= self.deadline:
            self.stopped = True
            return False
        time.sleep(pause)
        size = self.rng.randint(*ps["chars"])
        text = f"Quest {q['id']}, step {step + 1}: "
        while len(text) < size:
            text += self.rng.choice(WORDS) + " "
        body = {"model": model, "max_tokens": ps["reply"],
                "messages": [{"role": "user", "content": text}],
                # Tags reach LiteLLM_SpendLogs.request_tags.
                "metadata": {"tags": [f"lvg:{self.office.id}",
                                      f"day:{self.tag}",
                                      f"seat:{self.seat}",
                                      f"quest:{q['id']}"]}}
        if ps["stream"]:
            body["stream"] = True
            body["stream_options"] = {"include_usage": True}
        for tries in range(3):
            status, kind = chat(self.url, self.key, body)
            if status == 200:
                self.ok += 1
                return True
            if status == 422 or kind == "budget_exceeded":
                break  # the proxy said no; asking again will not help
            time.sleep(1 + tries)
        self.refused += 1
        self.code, self.kind = status, kind
        self.out_at = time.time()
        self.stopped = True  # idle for the rest of the day
        return False


# --- Day loop --------------------------------------------------------

def wait_for_day(office, players, day):
    """Start a day right after LiteLLM refills every seat's mana."""
    seats = [p["seat"] for p in players]
    state = seat_state(seats)
    missing = [s for s in seats if s not in state]
    if missing:
        die("no key in the proxy for: " + ", ".join(missing) +
            ". Run `office.py reset`, then `office.py setup`.")
    start = min(r["reset_at"] for r in state.values())
    # A fresh period began moments ago and nobody has spent yet (say,
    # right after setup): start now instead of waiting a whole day.
    began = start - office.rules["day"]["secs"]
    if (time.time() - began < office.rules["day"]["secs"] / 4
            and all(r["spend"] <= 0 for r in state.values())):
        print(f"Day {day} starts now; mana resets at {clock(start)} "
              "UTC.", flush=True)
        return time.time(), start
    wait = start - time.time()
    soon = f"in {wait:.0f} s" if wait >= 1 else "now"
    print(f"Day {day} starts at {clock(start)} UTC, when LiteLLM "
          f"refills the mana ({soon}).", flush=True)
    if wait > 0:
        time.sleep(wait)
    # The reset job runs every few seconds; wait until it has run for
    # every seat. If it is late, re-mint those seats (a fresh key has
    # a fresh budget), so the day still starts fair.
    give_up = time.time() + 60
    while True:
        state = seat_state(seats)
        late = [s for s in seats if s not in state
                or state[s]["reset_at"] <= start + 1]
        if not late:
            break
        if time.time() > give_up:
            print(f"  reset job late; re-minting {len(late)} seats",
                  flush=True)
            for s in late:
                gone = revoke(s)
                if gone not in ("revoked", "no key"):
                    die(f"revoke {s} failed: {gone}")
                err = mint(s, office.rules)
                if err:
                    die(f"re-mint {s} failed: {err}")
            break
        time.sleep(1)
    state = seat_state(seats)
    return time.time(), min(r["reset_at"] for r in state.values())


def play_day(office, day, url):
    rules = office.rules
    players = office.players()
    for p in players:
        if not (KEYS / f"{p['seat']}.key").exists():
            die(f"missing game/keys/{p['seat']}.key: run `office.py "
                "reset`, then `office.py setup`.")
    for t in ("quests", "completions", "daylog", "usage", "scores",
              "days"):  # a day that never finished is played again
        office.q(f"DELETE FROM {t} WHERE day = ?", day)
    started, ends = wait_for_day(office, players, day)
    tag = f"{day}.{secrets.token_hex(2)}"
    posted = post_quests(office, day)
    deadline = ends - rules["day"]["close_s"]
    print(f"  {posted} quests posted; bots work until {clock(deadline)} "
          f"UTC, mana resets at {clock(ends)}.", flush=True)
    bots = [Bot(office, p, day, tag, deadline, url) for p in players]
    for b in bots:
        b.start()
    for b in bots:  # Ctrl+C here: main() says the day is not scored
        b.join(max(1, deadline + 70 - time.time()))
    worked = time.time() - started
    office.q("UPDATE quests SET state = 'expired' "
             "WHERE day = ? AND state = 'open'", day)
    office.q("UPDATE quests SET state = 'unfinished' "
             "WHERE day = ? AND state = 'working'", day)
    expected = office.q("SELECT coalesce(sum(ok), 0) AS n FROM daylog "
                        "WHERE day = ?", day)[0]["n"]
    office.q("INSERT INTO days VALUES (?,?,?,?,?,NULL,NULL)", day, tag,
             started, worked, expected)
    score_day(office, day)
    office.meta("day", day)
    return started


def wait_for_logs(office, day, tag, expected):
    """LiteLLM writes spend logs in batches. Wait until every answered
    request of the day is in, so the board is complete."""
    t0 = time.time()
    limit = seconds(office.rules["score"]["flush_wait"])
    while True:
        n = pg('SELECT count(*) AS n FROM "LiteLLM_SpendLogs" WHERE '
               + spend_where(office.id, tag))[0]["n"]
        if n >= expected or time.time() - t0 > limit:
            return n, time.time() - t0
        time.sleep(2)


def score_day(office, day):
    rules = office.rules
    usd, sc = rules["mana"]["usd"], rules["score"]
    d = office.q("SELECT * FROM days WHERE day = ?", day)[0]
    logged, waited = wait_for_logs(office, day, d["tag"], d["expected"])
    office.q("UPDATE days SET logged = ?, flush_s = ? WHERE day = ?",
             logged, waited, day)
    rows = pg(
        "SELECT metadata->>'user_api_key_alias' AS alias, "
        "(SELECT t FROM jsonb_array_elements_text(request_tags) t "
        " WHERE t LIKE 'seat:%' LIMIT 1) AS seat, "
        "(SELECT t FROM jsonb_array_elements_text(request_tags) t "
        " WHERE t LIKE 'quest:%' LIMIT 1) AS quest, "
        "count(*) AS calls, sum(spend) AS spend, "
        "sum(total_tokens) AS tokens FROM \"LiteLLM_SpendLogs\" WHERE "
        + spend_where(office.id, d["tag"]) + " GROUP BY 1, 2, 3")
    players = {p["seat"]: p for p in office.players()}
    work, used = {}, {}
    for r in rows:
        seat, mana = r["alias"], r["spend"] / usd
        if seat not in players:
            continue
        u = used.setdefault(seat, {"calls": 0, "tokens": 0, "mana": 0.0})
        u["calls"] += r["calls"]
        u["tokens"] += r["tokens"]
        u["mana"] += mana
        # Work counts only if the key that paid is the seat it claims.
        if r["seat"] == f"seat:{seat}" and r["quest"]:
            work[(seat, r["quest"].removeprefix("quest:"))] = (
                r["calls"], mana)
    for seat, u in used.items():
        office.q("INSERT INTO usage VALUES (?,?,?,?,?)", day, seat,
                 u["calls"], u["tokens"], u["mana"])
    quests = {q["id"]: q for q in office.q(
        "SELECT * FROM quests WHERE day = ?", day)}
    results = []
    for c in office.q("SELECT * FROM completions WHERE day = ?", day):
        q = quests[c["quest"]]
        calls, mana = work.get((c["seat"], c["quest"]), (0, 0.0))
        if not c["approved"]:
            verdict = "rejected"
        elif calls < q["calls"]:
            verdict = "unbacked"  # handed in without the work behind it
        else:
            verdict = "counted"
        eff = sc["eff_max"] if mana <= 0 else q["par_mana"] / mana
        eff = max(sc["eff_min"], min(sc["eff_max"], eff))
        results.append(Result(c["quest"], c["seat"], calls, mana, eff,
                              verdict, q["value"]))
    for q in quests.values():
        if q["state"] == "unfinished":
            calls, mana = work.get((q["seat"], q["id"]), (0, 0.0))
            results.append(Result(q["id"], q["seat"], calls, mana, 0.0,
                                  "unfinished", 0))
    for r in results:
        bonus = points = 0.0
        if r.verdict == "counted":
            bonus = min(sc["streak_cap"],
                        sc["streak_step"] * streak(office, r.seat, day))
            points = r.value * r.eff * (1 + bonus)
        office.q("INSERT INTO scores VALUES (?,?,?,?,?,?,?,?,?)", day,
                 r.quest, r.seat, r.calls, r.mana, r.eff, bonus, points,
                 r.verdict)


def streak(office, seat, day):
    """Days in a row before `day` with at least one counted quest."""
    n = 0
    for d in range(day - 1, 0, -1):
        if not office.q("SELECT 1 FROM scores WHERE day = ? AND seat = ? "
                        "AND verdict = 'counted' LIMIT 1", d, seat):
            break
        n += 1
    return n


def day_summary(office, day):
    d = office.q("SELECT * FROM days WHERE day = ?", day)[0]
    st = dict(office.q("SELECT verdict, count(*) FROM scores "
                       "WHERE day = ? GROUP BY 1", day))
    qs = dict(office.q("SELECT state, count(*) FROM quests "
                       "WHERE day = ? GROUP BY 1", day))
    print(f"Day {day} done: bots worked {d['worked']:.0f} s, "
          f"{d['expected']} calls answered.")
    note = "" if d["logged"] >= d["expected"] else "  (INCOMPLETE)"
    print(f"  spend logs: {d['logged']} of {d['expected']} rows after "
          f"{d['flush_s']:.0f} s{note}")
    print(f"  quests: {sum(qs.values())} posted, "
          f"{st.get('counted', 0)} counted, "
          f"{st.get('unbacked', 0)} thrown out (too few calls), "
          f"{st.get('rejected', 0)} given up,")
    print(f"          {st.get('unfinished', 0)} unfinished, "
          f"{qs.get('expired', 0)} never picked up")
    outs = office.q("SELECT l.*, p.name FROM daylog l JOIN players p "
                    "USING (seat) WHERE day = ? AND refused > 0 "
                    "ORDER BY out_at", day)
    for o in outs:
        at = o["out_at"] - d["started"]
        if o["kind"] == "budget_exceeded":
            why = "out of mana"
        else:
            why = f"error {o['kind']}" if o["kind"] else "error"
        print(f"  {o['name']}: {why} at {at // 60:.0f}:{at % 60:02.0f} "
              f"into the day (HTTP {o['code']}), idle after that")
    print()


# --- Board -----------------------------------------------------------

def rank_corr(a, b):
    """Spearman rank correlation of two orderings of the same names."""
    n = len(a)
    if n < 2:
        return 0.0
    pos = {x: i for i, x in enumerate(b)}
    d2 = sum((i - pos[x]) ** 2 for i, x in enumerate(a))
    return 1 - 6 * d2 / (n * (n * n - 1))


def scored_days(office, day):
    """The days a board covers: one day, or every scored day."""
    last = int(office.meta("day") or 0)
    if not last:
        print("No day played yet: run `python3 game/office.py run`.")
        return None, last
    if day is not None and not 1 <= day <= last:
        die(f"no day {day} yet: days 1 to {last} are scored.")
    return ([day] if day is not None else list(range(1, last + 1))), last


def heading(office, day, last):
    rules = office.rules
    when = f"day {day}" if day is not None else f"after day {last}"
    print(f"Office {office.id}, practice week: {when} of "
          f"{rules['day']['per_week']} ({rules['day']['length']} days)")
    print(f"1 mana = ${money(rules['mana']['usd'])}; daily mana "
          f"{rules['mana']['daily']:,} per seat")
    print()


def board(office, day=None):
    rules = office.rules
    days, last = scored_days(office, day)
    if not days:
        return
    marks = ",".join("?" * len(days))
    players = office.players()
    pts = {p["seat"]: {"points": 0.0, "done": 0, "eff": 0.0}
           for p in players}
    for r in office.q(f"SELECT * FROM scores WHERE day IN ({marks}) "
                      "AND verdict = 'counted'", *days):
        pts[r["seat"]]["points"] += r["points"]
        pts[r["seat"]]["done"] += 1
        pts[r["seat"]]["eff"] += r["eff"]
    use = {p["seat"]: {"tokens": 0, "mana": 0.0} for p in players}
    for r in office.q(f"SELECT * FROM usage WHERE day IN ({marks})",
                      *days):
        use[r["seat"]]["tokens"] += r["tokens"]
        use[r["seat"]]["mana"] += r["mana"]
    info = {p["seat"]: p for p in players}
    heading(office, day, last)
    guilds = {}
    for g in office.q("SELECT name FROM guilds ORDER BY name"):
        members = [p["seat"] for p in players if p["guild"] == g["name"]]
        if members:
            guilds[g["name"]] = (
                sum(pts[s]["points"] for s in members) / len(members),
                len(members))
    top = max([avg for avg, _ in guilds.values()] + [1])
    print("GUILDS: points per member (finished, efficient work)")
    for name, (avg, size) in sorted(guilds.items(),
                                    key=lambda x: -x[1][0]):
        bar = "#" * round(30 * avg / top)
        print(f"  {name:<8} {size} members {avg:>7.1f}  {bar}")
    print()
    by_pts = sorted(pts, key=lambda s: (-pts[s]["points"],
                                        -pts[s]["done"], s))
    by_tok = sorted(use, key=lambda s: (-use[s]["tokens"], s))
    by_mana = sorted(use, key=lambda s: (-use[s]["mana"], s))
    if not rules["board"]["individuals"]:
        print("(Individual boards are off in rules.toml.)")
        return
    left = ["WHO FINISHED WORK (game score)",
            " # seat   persona  done  eff    pts"]
    for i, s in enumerate(by_pts, 1):
        n = pts[s]["done"]
        eff = pts[s]["eff"] / n if n else 0.0
        left.append(f"{i:>2} {info[s]['name']:<6} "
                    f"{info[s]['persona']:<8} {n:>4} {eff:>4.2f} "
                    f"{pts[s]['points']:>6.1f}")
    right = ["WHO BURNED TOKENS (naive board)",
             " # seat      tokens   mana  done"]
    for i, s in enumerate(by_tok, 1):
        right.append(f"{i:>2} {info[s]['name']:<6} "
                     f"{use[s]['tokens']:>9,} {use[s]['mana']:>6,.0f} "
                     f"{pts[s]['done']:>4}")
    for a, b in zip(left, right):
        print(f"{a:<36}| {b}")
    print()
    t, w = by_tok[0], by_pts[0]
    print("GOODHART CHECK")
    for label, s, what in (
            ("Most tokens:", t, f"{use[t]['tokens']:,} tokens"),
            ("Most mana:", by_mana[0],
             f"{use[by_mana[0]]['mana']:,.0f} mana"),
            ("Top score:", w, f"{use[w]['tokens']:,} tokens")):
        print(f"  {label:<12} {info[s]['name']} ({info[s]['persona']}), "
              f"{what}, {pts[s]['done']} done, "
              f"{pts[s]['points']:.0f} pts")
    if t != w and use[w]["tokens"] and pts[w]["done"]:
        print(f"  {info[t]['name']} burned "
              f"{use[t]['tokens'] / use[w]['tokens']:.1f}x the tokens of "
              f"{info[w]['name']}, for "
              f"{pts[t]['done'] / pts[w]['done']:.2f}x the finished quests")
        print(f"  and {pts[t]['points'] / pts[w]['points']:.2f}x the "
              "points.")
    rho = rank_corr(by_tok, by_pts)
    print(f"  Rank match, tokens vs score: {rho:+.2f} "
          "(+1 same order, 0 none, -1 reversed)")


def seat_board(office, name, day=None):
    """One seat, quest by quest, so every point can be checked."""
    days, last = scored_days(office, day)
    if not days:
        return
    p = office.q("SELECT * FROM players WHERE name = ? OR seat = ?",
                 name, name)
    if not p:
        die(f"no seat {name!r}: see `office.py board` for the names.")
    p = p[0]
    marks = ",".join("?" * len(days))
    rows = office.q(
        "SELECT s.*, q.size, q.value, q.par_mana, q.calls AS need "
        "FROM scores s JOIN quests q ON q.id = s.quest "
        f"WHERE s.seat = ? AND s.day IN ({marks}) "
        "ORDER BY s.day, s.quest", p["seat"], *days)
    heading(office, day, last)
    print(f"{p['name']} ({p['persona']}, guild {p['guild']}): "
          "points = value x eff x (1 + bonus)")
    print(" day quest  size   calls par   mana  eff bonus value    pts "
          "verdict")
    for r in rows:
        print(f"{r['day']:>4} {r['quest']:<6} "
              f"{r['size']:<6} {r['calls']:>2}/{r['need']:<2} "
              f"{r['par_mana']:>3.0f} {r['mana']:>6.1f} {r['eff']:>4.2f} "
              f"{r['bonus']:>+5.0%} {r['value']:>5.0f} "
              f"{r['points']:>6.1f} {r['verdict']}")
    ok = [r for r in rows if r["verdict"] == "counted"]
    print(f"Counted {len(ok)} of {len(rows)}: value "
          f"{sum(r['value'] for r in ok):,.0f}, mana "
          f"{sum(r['mana'] for r in ok):,.1f} (par "
          f"{sum(r['par_mana'] for r in ok):,.0f}), points "
          f"{sum(r['points'] for r in ok):,.1f}.")
    print("calls: tagged calls in the spend logs / the fewest the quest "
          "needs.")


# --- Commands --------------------------------------------------------

def cmd_setup(rules, args):
    if DB.exists():
        die("the office is already set up. For a new week, run\n"
            "`python3 game/office.py reset` first.")
    prefix = rules["office"]["seat_prefix"]
    usd = rules["mana"]["daily"] * rules["mana"]["usd"]
    print(f"Minting {len(rules['roster']['seats'])} game seats: "
          f"{rules['mana']['daily']} mana (${money(usd)}) each, "
          f"refilled every {rules['day']['length']}.")
    for s in rules["roster"]["seats"]:
        err = mint(prefix + s["name"], rules)
        if err:
            die(f"mint {prefix + s['name']} failed: {err}\n"
                "Clean up with `python3 game/office.py reset`.")
        print(f"  {prefix + s['name']:<14} {s['persona']:<9} "
              f"guild {s['guild']}")
    db = sqlite3.connect(DB)
    db.executescript(SCHEMA)
    office = secrets.token_hex(3)
    db.executemany("INSERT INTO meta VALUES (?, ?)", [
        ("office", office), ("day", "0"),
        ("created", time.strftime("%Y-%m-%d %H:%M UTC", time.gmtime()))])
    for g in sorted({s["guild"] for s in rules["roster"]["seats"]}):
        db.execute("INSERT INTO guilds VALUES (?)", (g,))
    for s in rules["roster"]["seats"]:
        db.execute("INSERT INTO players VALUES (?,?,?,?)",
                   (prefix + s["name"], s["name"], s["persona"],
                    s["guild"]))
    db.commit()
    db.close()
    print(f"Office {office} is open. Keys are in game/keys/ (git "
          "ignores *.key).")
    print("Next: python3 game/office.py run --days 2")


def cmd_run(rules, args):
    office = Office(rules)
    url = proxy_url()
    per_week = rules["day"]["per_week"]
    for _ in range(args.days):
        day = int(office.meta("day")) + 1
        if day > per_week:
            print(f"The week is over ({per_week} days). Start a new "
                  "one with\n`office.py reset`, then `office.py setup`.")
            return
        play_day(office, day, url)
        day_summary(office, day)
        if not args.quiet:
            board(office)
            print()


def cmd_board(rules, args):
    if args.seat:
        seat_board(Office(rules), args.seat, args.day)
    else:
        board(Office(rules), args.day)


def cmd_reset(rules, args):
    seats = [rules["office"]["seat_prefix"] + s["name"]
             for s in rules["roster"]["seats"]]
    if DB.exists():
        db = sqlite3.connect(DB)
        seats = sorted(set(seats) | {r[0] for r in db.execute(
            "SELECT seat FROM players")})
        db.close()
    done = {s: revoke(s) for s in seats}
    failed = {s: why for s, why in done.items()
              if why not in ("revoked", "no key")}
    if failed:
        seat, why = next(iter(failed.items()))
        die(f"could not revoke {len(failed)} of {len(seats)} game "
            f"seats. {seat}:\n  {why}\nThe game was not wiped. Is the "
            "proxy up, with this seats.env? (docker compose ps)\n"
            "Then run reset again.")
    n = sum(why == "revoked" for why in done.values())
    print(f"Revoked {n} game seat{'' if n == 1 else 's'}"
          + (f"; {len(seats) - n} had no key." if n < len(seats) else "."))
    for p in GAME.glob("office.db*"):
        p.unlink()
    print("Game state wiped. New week: python3 game/office.py setup")


def positive(text):
    try:
        n = int(text)
    except ValueError:
        n = 0
    if n < 1:
        raise argparse.ArgumentTypeError(f"{text!r}: use 1 or more")
    return n


# What Ctrl+C means, per command.
STOPPED = {
    "setup": "stopped. Clean up with `python3 game/office.py reset`.",
    "run": "stopped; this day is not scored. `run` plays it again.",
    "reset": "stopped. Run `python3 game/office.py reset` again.",
}


def main():
    ap = argparse.ArgumentParser(
        description="Office game on top of the proxy (game/README.md).")
    ap.add_argument("--rules", default=GAME / "rules.toml",
                    help="rules file (default: game/rules.toml)")
    sub = ap.add_subparsers(dest="cmd", required=True)
    sub.add_parser("setup", help="mint game seats, create office.db")
    r = sub.add_parser("run", help="play practice days with bots")
    r.add_argument("-d", "--days", type=positive, default=1,
                   help="days to play (default 1)")
    r.add_argument("-q", "--quiet", action="store_true",
                   help="day summaries only, no board")
    b = sub.add_parser("board", help="show both leaderboards")
    b.add_argument("-d", "--day", type=positive, help="one day only")
    b.add_argument("-s", "--seat", metavar="NAME",
                   help="one seat, quest by quest (e.g. fern)")
    sub.add_parser("reset", help="weekly reset: revoke seats, wipe")
    args = ap.parse_args()
    if hasattr(signal, "SIGPIPE"):  # `board | head` ends quietly
        signal.signal(signal.SIGPIPE, signal.SIG_DFL)
    rules = load_rules(args.rules)
    try:
        {"setup": cmd_setup, "run": cmd_run, "board": cmd_board,
         "reset": cmd_reset}[args.cmd](rules, args)
    except KeyboardInterrupt:
        die("\n" + STOPPED.get(args.cmd, "stopped."))


if __name__ == "__main__":
    main()

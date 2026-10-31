#!/usr/bin/env python3
"""Front Desk: one web page for the proxy's key cards and tally.

  python3 gui/frontdesk.py          # this computer only
  python3 gui/frontdesk.py --lan    # phones and laptops on your network

Runs on the proxy machine, next to seats.env. It reads the master key
from there, like mint-key.sh does, and never sends it to a browser.
Minting and revoking go through ./mint-key.sh. Stdlib only.

In Docker, gui/Dockerfile runs it with --docker. Then Compose passes
seats.env in as environment variables, minting goes through the Go
mint-key, and the tally comes straight from Postgres through psycopg,
the one package the image adds. The password is always asked for.
"""

import argparse
import functools
import gzip
import hashlib
import hmac
import http.client
import ipaddress
import json
import math
import os
import queue
import re
import secrets
import select
import shutil
import socket
import sqlite3
import subprocess
import sys
import threading
import time
from collections import deque
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
from http import HTTPStatus
from http.cookies import SimpleCookie
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import quote, urlsplit

HERE = Path(__file__).resolve().parent
REPO = HERE.parent
STATIC = HERE / "static"
GAME = REPO / "game"

FAST = 2.0          # seconds between checks while someone watches
KEYS_EVERY = 10.0   # card spend lands in batches; our edits show at once
TALLY_EVERY = 5.0   # the proxy writes spend in batches anyway
HISTORY_EVERY = 600.0   # the days before today barely change
FALLBACK_EVERY = 60.0   # the proxy's own tally is heavy for it
SLOW_EVERY = 60.0   # version and model list
IDLE_AFTER = 30.0   # stop checking this long after the last viewer
PING_EVERY = 15.0   # keep live streams open
MAX_STREAMS = 100
MAX_CONNECTIONS = 256   # each open connection holds a thread
MAX_PER_DEVICE = 32     # with --lan: one device can't take them all
READ_TIMEOUT = 10   # seconds a browser gets to send its request
SESSION_HOURS = 12
LOCK_AFTER = 5      # wrong passwords from one device in 10 minutes
FAIL_GAP = 1.0      # seconds between tries of devices that got it wrong

SEAT_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,39}")
BUDGET_RE = re.compile(r"[0-9]{1,6}([.][0-9]{1,2})?")
# What mint-key.sh takes; "m" is minutes and "mo" months.
DURATION_RE = re.compile(r"[1-9][0-9]{0,3}(mo|[smhdw])")
# How often a budget refills: "" is never, a lifetime total.
REFILLS = ("", "1d", "1w", "1mo")
KEY_RE = re.compile(r"sk-[A-Za-z0-9_-]{8,200}")
HOST_RE = re.compile(r"[A-Za-z0-9.-]{1,253}|[0-9A-Fa-f:]{2,39}")
# Key-like text in an error message: keys, key ends, hashes.
SECRET_RE = re.compile(r"sk-\S*|[0-9A-Fa-f]{32,}")
# --docker: the port compose.yaml publishes the proxy on, for seats.
SEAT_PORT = 4000
# --docker: all of mint-key's environment. Not the database password
# or the model keys, which it never needs.
MINT_ENV = ("PATH", "HOME", "LANG", "TZ", "SEATS_ENV", "PROXY_URL",
            "LITELLM_MASTER_KEY", "LITELLM_SALT_KEY", "DEFAULT_MODELS",
            "DEFAULT_BUDGET", "DEFAULT_DURATION")
LOOPBACK = ("localhost", "127.0.0.1", "::1")

# Spend per seat and per model in one round trip, for today (UTC) or
# for the days before it. Cancelled cards keep their seat name via the
# archive. fails: requests the proxy refused.
TALLY_SQL = """
with n as (
  select distinct on (token) token, key_alias from (
    select token, key_alias, 0 o from "LiteLLM_VerificationToken"
    union all
    select token, key_alias, 1 from "LiteLLM_DeletedVerificationToken"
  ) x order by token, o),
s as (
  select coalesce(n.key_alias, '') seat,
    coalesce(d.model_group, d.model, '') model, d.spend,
    d.prompt_tokens + d.completion_tokens tokens,
    d.api_requests reqs, d.failed_requests fails
  from "LiteLLM_DailyUserSpend" d left join n on n.token = d.api_key
  where d.date {op} to_char(now() at time zone 'UTC', 'YYYY-MM-DD')),
g as (
  select case when grouping(seat) = 0 then 'seat' else 'model' end k,
    coalesce(seat, model) name, sum(spend) spend, sum(tokens) tokens,
    sum(reqs) reqs, sum(fails) fails
  from s group by grouping sets ((seat), (model)))
select json_build_object(
  'day', to_char(now() at time zone 'UTC', 'YYYY-MM-DD'),
  'rows', coalesce(json_agg(g), '[]'))
from g;
"""
TODAY_SQL = TALLY_SQL.format(op="=")
HISTORY_SQL = TALLY_SQL.format(op="<")
COUNTS = ("spend", "tokens", "reqs", "fails")


def log(msg):
    # One write per line, so lines from many threads never mix.
    sys.stderr.write(f"{time.strftime('%H:%M:%S')} {msg}\n")
    sys.stderr.flush()


def read_env(path):
    """NAME=value lines, as in seats.env.example. No shell tricks."""
    env = {}
    for line in Path(path).read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        name, value = line.split("=", 1)
        name = name.removeprefix("export ").strip()
        value = value.strip()
        if len(value) > 1 and value[0] == value[-1] and value[0] in "'\"":
            value = value[1:-1]
        env[name] = value
    return env


def parse_time(text):
    if not text:
        return None
    t = datetime.fromisoformat(text)
    return t if t.tzinfo else t.replace(tzinfo=timezone.utc)


def utc_text(text):
    """A time from the proxy, with its time zone spelled out, so a
    browser never reads it as local time. None if it isn't one."""
    return _utc_text(text) if isinstance(text, str) else None


# Remembered: every check asks again for each card's next refill.
@functools.lru_cache(maxsize=4096)
def _utc_text(text):
    try:
        t = parse_time(text)
    except ValueError:
        return None
    return t.isoformat() if t else None


def lan_ip():
    """This machine's address on its main network. Sends nothing."""
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
            s.connect(("192.0.2.1", 9))
            return s.getsockname()[0]
    except OSError:
        return None


def make_password():
    """16 easy-to-type characters, about 79 bits."""
    abc = "abcdefghjkmnpqrstuvwxyz23456789"
    raw = "".join(secrets.choice(abc) for _ in range(16))
    return "-".join(raw[i:i + 4] for i in range(0, 16, 4))


def merge_tally(hist, today):
    """All time = the days before + today. t_ fields: today only."""
    rows = {}
    for src, is_today in ((hist, False), (today, True)):
        for r in src["rows"]:
            o = rows.setdefault((r["k"], r["name"]), {
                "k": r["k"], "name": r["name"],
                **{f: 0 for f in COUNTS},
                **{"t_" + f: 0 for f in COUNTS}})
            for f in COUNTS:
                o[f] += r[f] or 0
                if is_today:
                    o["t_" + f] = r[f] or 0
    return {"day": today["day"], "rows": list(rows.values())}


class Settings:
    """seats.env, reread when it changes, like mint-key.sh does. Given
    environ instead (with --docker), the same settings from there:
    Compose passes seats.env in as environment variables, once."""

    def __init__(self, path, environ=None):
        self.environ = environ
        self.path = Path(path).resolve() if environ is None else None
        self.mtime = None
        self.load()

    def load(self):
        if self.environ is None:
            mtime = self.path.stat().st_mtime
            env = read_env(self.path)
            self.mtime = mtime
        else:
            env = dict(self.environ)
        self.master = env.get("LITELLM_MASTER_KEY", "")
        self.db_url = env.get("DATABASE_URL", "")
        url = urlsplit(env.get("PROXY_URL") or "http://localhost:4000")
        self.https = url.scheme == "https"
        self.host = url.hostname or "localhost"
        self.port = url.port or (443 if self.https else 80)
        self.defaults = {
            "models": [m.strip() for m in
                       env.get("DEFAULT_MODELS", "local").split(",")
                       if m.strip()],
            "budget": env.get("DEFAULT_BUDGET", "5"),
            "duration": env.get("DEFAULT_DURATION", "30d"),
        }
        # Every value that must never reach a browser or a log.
        self.hidden = [v for k, v in env.items() if len(v) >= 6 and (
            k == "DATABASE_URL" or
            any(w in k for w in ("KEY", "PASSWORD", "SECRET", "TOKEN")))]
        self.warnings = []
        if "change-me" in self.master + env.get("LITELLM_SALT_KEY", ""):
            tool = "mint-key.sh" if self.environ is None else "mint-key"
            self.warnings.append(
                f"seats.env still has change-me values, so {tool} "
                "will refuse to run. Do README Step 3a.")

    def reload_if_changed(self):
        if self.environ is not None:
            return  # a new seats.env means a new container
        try:
            if self.path.stat().st_mtime != self.mtime:
                self.load()
                log("reread seats.env")
        except OSError:
            pass

    def scrub(self, text):
        for v in self.hidden:
            text = text.replace(v, "[hidden]")
        return text


class ProxyError(Exception):
    pass


MASTER_REFUSED = ("The proxy did not accept the master key. If seats.env "
                  "changed, run: docker compose up -d --force-recreate "
                  "proxy")
# In Docker, Front Desk keeps the seats.env it started with, so it may
# be the one out of date. This line makes both new, as needed.
MASTER_REFUSED_DOCKER = ("Front Desk and the proxy have different master "
                         "keys. If seats.env changed, run: docker compose "
                         "--profile frontdesk up -d")


class Proxy:
    """Keep-alive HTTP to LiteLLM, for one thread at a time."""

    def __init__(self, settings):
        self.s = settings
        self.conn = None

    def get(self, path, auth=True, timeout=5):
        headers = {"Authorization": "Bearer " + self.s.master} if auth \
            else {}
        for attempt in (0, 1):
            if self.conn is None:
                cls = http.client.HTTPSConnection if self.s.https \
                    else http.client.HTTPConnection
                self.conn = cls(self.s.host, self.s.port)
            self.conn.timeout = timeout
            if self.conn.sock:
                self.conn.sock.settimeout(timeout)
            try:
                self.conn.request("GET", path, headers=headers)
                r = self.conn.getresponse()
                return r.status, r.read()
            except (OSError, http.client.HTTPException) as e:
                self.conn.close()
                self.conn = None
                # A stale keep-alive socket fails once: retry fresh.
                # A slow proxy is not retried; it just waits longer.
                if attempt or isinstance(e, TimeoutError):
                    raise

    def json(self, path):
        status, body = self.get(path)
        if status == 401:
            raise ProxyError(MASTER_REFUSED if self.s.environ is None
                             else MASTER_REFUSED_DOCKER)
        if status != 200:
            hint = " Its database may be down: run docker compose ps." \
                if status >= 500 else ""
            raise ProxyError(f"The proxy answered {status} to "
                             f"{path.split('?')[0]}.{hint}")
        return json.loads(body)

    def close(self):
        if self.conn:
            self.conn.close()
            self.conn = None


class TallyError(Exception):
    pass


class Stale(TallyError):
    """A question failed on a connection kept from before."""


class Psql:
    """One long-lived psql in the db container, read only.

    Starting `docker compose exec` costs ~150 ms, so it is started
    once and fed queries over stdin, a few ms each.
    """

    def __init__(self, repo):
        self.repo = repo
        self.p = None
        self.out = None
        self.lock = threading.Lock()
        self.retry_at = 0.0

    def start(self):
        cmd = ["docker", "compose", "exec", "-T", "db", "psql", "-X",
               "-q", "-A", "-t", "-U", "litellm", "-d", "litellm",
               "-v", "VERBOSITY=terse"]
        try:
            self.p = subprocess.Popen(
                cmd, cwd=self.repo, stdin=subprocess.PIPE,
                stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                text=True, bufsize=1)
        except OSError as e:
            raise TallyError(f"cannot run docker: {e.strerror}")
        self.out = queue.Queue()
        threading.Thread(target=self.read, args=(self.p, self.out),
                         daemon=True).start()
        self.p.stdin.write(
            "set default_transaction_read_only = on;\n"
            "set statement_timeout = '5s';\n"
            "set application_name = 'frontdesk';\n")

    @staticmethod
    def read(p, out):
        for line in p.stdout:
            out.put(line)
        out.put(None)

    def stop(self):
        if self.p:
            self.p.kill()
            self.p = None

    def query(self, sql, timeout=8.0):
        with self.lock:
            if time.monotonic() < self.retry_at:
                raise TallyError("waiting before trying psql again")
            try:
                return self._query(sql, timeout)
            except TallyError:
                self.stop()
                self.retry_at = time.monotonic() + 30
                raise

    def _query(self, sql, timeout):
        if self.p is None or self.p.poll() is not None:
            self.start()
        mark = "__fd_" + secrets.token_hex(4)
        try:
            self.p.stdin.write(
                f"begin read only;\n{sql.strip()}\ncommit;\n"
                f"\\echo {mark}\n")
            self.p.stdin.flush()
        except OSError:
            pass  # it died; its last words are in the queue
        lines, end = [], time.monotonic() + timeout
        while True:
            try:
                line = self.out.get(timeout=max(0.1,
                                                end - time.monotonic()))
            except queue.Empty:
                raise TallyError("psql did not answer in time")
            if line is None:
                said = " ".join(x.strip() for x in lines[-2:])
                raise TallyError(said or "psql stopped")
            if line.strip() == mark:
                break
            lines.append(line)
        bad = [x for x in lines if x.startswith(("ERROR", "FATAL"))]
        if bad:
            raise TallyError(bad[0].strip())
        text = "".join(lines)  # json_agg spreads over many lines
        if "{" not in text:
            raise TallyError("psql gave no answer")
        return json.loads(text[text.index("{"):])


class Pg:
    """With --docker: the same questions as Psql, read only too, asked
    of Postgres at DATABASE_URL through psycopg, which only the Docker
    image has. Same answers: psycopg reads the JSON with json.loads.

    Each question runs in a helper thread, so one the database never
    answers (a paused container, say) times out, as with psql.
    """

    def __init__(self, url):
        self.url = url
        self.conn = None    # idle between questions
        self.lock = threading.Lock()
        self.retry_at = 0.0
        self.fails = 0      # failed tries in a row

    def connect(self):
        import psycopg  # in the Docker image only: see gui/Dockerfile
        conn = psycopg.connect(
            self.url, autocommit=True, connect_timeout=5,
            application_name="frontdesk",
            options="-c default_transaction_read_only=on "
                    "-c statement_timeout=5s")
        conn.read_only = True  # each transaction says BEGIN READ ONLY
        return conn

    def work(self, conn, sql, box):
        try:
            if conn is None:
                conn = self.connect()
            with conn.transaction():
                # prepare=True: one statement per question, never more.
                got = conn.execute(sql, prepare=True).fetchone()[0]
        except Exception as e:  # psycopg.Error, or no psycopg at all
            got = e
        try:
            box.put_nowait((conn, got))
        except queue.Full:  # query() gave up on this one: tidy up
            if conn is not None:
                conn.close()

    def query(self, sql, timeout=8.0):
        with self.lock:
            if time.monotonic() < self.retry_at:
                raise TallyError("waiting before trying the database "
                                 "again")
            try:
                try:
                    rows = self._query(sql, timeout)
                except Stale:
                    # The connection kept from last time broke, as when
                    # the database restarts. A new one may work now.
                    self.drop()
                    rows = self._query(sql, timeout)
            except TallyError:
                self.drop()
                # A database that is just restarting is back within
                # seconds: try again soon once, then every 30 s.
                self.fails += 1
                self.retry_at = time.monotonic() + (
                    3 if self.fails == 1 else 30)
                raise
            self.fails = 0
            return rows

    def _query(self, sql, timeout):
        if not self.url:
            raise TallyError("DATABASE_URL is not set in seats.env")
        kept = self.conn is not None
        box = queue.Queue(1)
        threading.Thread(target=self.work, args=(self.conn, sql, box),
                         daemon=True).start()
        try:
            conn, got = box.get(timeout=timeout)
        except queue.Empty:
            try:
                box.put_nowait(None)  # the late answer finds it full
            except queue.Full:
                conn, got = box.get_nowait()  # it came just now
            else:
                self.conn = None  # still busy: its thread closes it
                raise TallyError("the database did not answer in time")
        self.conn = conn
        if isinstance(got, Exception):
            said = str(got).strip().splitlines()
            raise (Stale if kept else TallyError)(
                said[0] if said else type(got).__name__)
        if not isinstance(got, dict):
            raise TallyError("the database gave no answer")
        return got

    def drop(self):
        conn, self.conn = self.conn, None
        if conn is not None:
            conn.close()

    def stop(self):
        # A question still running keeps its connection until it ends.
        if self.lock.acquire(blocking=False):
            try:
                self.drop()
            finally:
                self.lock.release()


def seat_state(k, now):
    if k.get("blocked"):
        return "blocked"
    exp = parse_time(k.get("expires"))
    if exp and exp <= now:
        return "expired"
    budget = k.get("max_budget")
    if budget is not None and (k.get("spend") or 0) >= budget:
        # A budget that refills on a schedule is only waiting.
        return "waiting" if k.get("budget_duration") else "spent"
    return "ok"


def dollars(x):
    """Spend for the page. A refill can leave -1e-18: that is $0."""
    return max(0.0, round(x or 0, 6))


class GameSeats:
    """The office game's seats, found the way `office.py reset` finds
    them: its prefix plus each roster name in rules.toml, and every
    player in office.db. Each file is reread only when it changes: a
    running game writes office.db many times a minute."""

    def __init__(self, folder):
        self.rules = folder / "rules.toml"
        self.db = folder / "office.db"
        self.rules_at = self.db_at = -1  # -1: not read yet
        self.prefix = ""
        self.roster = set()
        self.players = set()
        self.seats = frozenset()

    def get(self):
        at = self.mtime(self.rules)
        if at != self.rules_at:
            self.rules_at = at
            self.load_rules()
        at = self.mtime(self.db)
        if at != self.db_at:
            self.db_at = at
            self.load_players(at is not None)
        self.seats = frozenset(self.roster | self.players)
        return self.seats

    @staticmethod
    def mtime(path):
        try:
            return path.stat().st_mtime_ns
        except OSError:
            return None

    def load_rules(self):
        import tomllib  # new in 3.11: main() says so on older ones
        try:
            with open(self.rules, "rb") as f:
                rules = tomllib.load(f)
            self.prefix = str(rules["office"]["seat_prefix"])
            self.roster = {self.prefix + str(s["name"])
                           for s in rules["roster"]["seats"]}
        except (OSError, ValueError, KeyError, TypeError):
            self.prefix, self.roster = "", set()

    def load_players(self, there):
        if not there:
            self.players = set()
            return
        try:
            db = sqlite3.connect(self.db.as_uri() + "?mode=ro",
                                 uri=True, timeout=0.2)
            try:
                self.players = {r[0] for r in db.execute(
                    "SELECT seat FROM players")}
            finally:
                db.close()
        except sqlite3.Error:
            self.db_at = -1  # busy or half made: next time

    def owns(self, seat):
        """True if a new card by this name would clash with the game."""
        low = seat.lower()
        return bool(self.prefix) and low.startswith(self.prefix) or \
            low in {s.lower() for s in self.seats}


class Desk:
    """Checks the proxy once for every viewer and keeps the answer."""

    def __init__(self, settings, psql, lan):
        self.settings = settings
        self.proxy = Proxy(settings)
        self.psql = psql
        self.lan = lan
        self.cond = threading.Condition()
        self.kick = threading.Event()
        self.lock = threading.Lock()  # raw_keys, overrides, publish
        self.pool = ThreadPoolExecutor(1)
        self.secret = secrets.token_bytes(16)
        self.state = None
        self.body = b""
        self.gz = b""
        self.etag = ""
        self.version = 0
        self.starts = 0        # checks begun
        self.checks = 0        # checks finished, ok or not
        self.mine = 0          # number of the check now running
        self.checked = 0.0
        self.viewers = 0
        self.seen = 0.0
        self.tokens = {}       # card id -> token hash, never sent out
        self.raw_keys = []
        self.overrides = {}    # seat -> (its keys, check number)
        self.notes = []
        self.models = []
        self.litellm = None
        self.db = None
        self.slow_at = -SLOW_EVERY
        self.keys_at = -KEYS_EVERY
        self.force_keys = False
        self.tally_at = -TALLY_EVERY
        self.tally = None
        self.hist = None       # tally of the days before today
        self.hist_at = 0.0
        self.fallback_at = -FALLBACK_EVERY
        self.games = GameSeats(GAME)
        self.game = frozenset()  # seat names the office game uses

    def run(self):
        last = 0.0
        while True:
            # Wake each second to see if anyone is watching; check
            # every FAST seconds while they are, at once when kicked.
            kicked = self.kick.wait(1.0)
            busy = self.viewers or \
                time.monotonic() - self.seen < IDLE_AFTER
            if not kicked and not (busy and
                                   time.monotonic() - last >= FAST):
                continue
            self.kick.clear()
            last = time.monotonic()
            with self.cond:
                self.starts += 1
                self.mine = self.starts
            try:
                self.refresh()
            except Exception as e:  # keep the desk running
                log("check failed: " + self.settings.scrub(repr(e))[:200])
            with self.cond:
                self.checks += 1
                self.cond.notify_all()

    def fresh(self, wait=4.0):
        """Make sure the shared answer is at most FAST seconds old."""
        self.seen = time.monotonic()
        with self.cond:
            if self.body and time.time() - self.checked < FAST + 1:
                return
            n = self.checks
            self.kick.set()
            self.cond.wait_for(lambda: self.checks != n, timeout=wait)

    def refresh_now(self, wait=5.0):
        """Wait for a full check that began after this call."""
        with self.cond:
            target = self.starts + 1
            self.force_keys = True
            self.kick.set()
            self.cond.wait_for(lambda: self.checks >= target,
                               timeout=wait)

    def refresh(self):
        s = self.settings
        s.reload_if_changed()
        now = time.monotonic()
        job = None
        if now - self.tally_at >= TALLY_EVERY:
            self.tally_at = now
            job = self.pool.submit(self.tally_query)
        notes = list(s.warnings)
        try:
            status, _ = self.proxy.get("/health/liveliness", auth=False,
                                       timeout=2)
            up = status == 200
        except (OSError, http.client.HTTPException) as e:
            up = False
            notes.append(f"Details: nothing answers at {s.host}:"
                         f"{s.port} ({e.__class__.__name__}).")
        if up:
            try:
                if now - self.slow_at >= SLOW_EVERY or not self.models:
                    self.load_slow()
                    self.slow_at = now
                if self.force_keys or now - self.keys_at >= KEYS_EVERY:
                    self.force_keys = False
                    self.set_keys(self.load_keys())
                    self.keys_at = now
                    self.load_game()
            except (OSError, ValueError, ProxyError,
                    http.client.HTTPException) as e:
                notes.append(str(e) if isinstance(e, ProxyError) else
                             "Could not read the key cards: " +
                             e.__class__.__name__)
        if job:
            try:
                self.tally = dict(job.result(timeout=10),
                                  source="database")
            except Exception as e:
                why = s.scrub(str(e))[:160]
                # Proxy down too? Keep the last tally, like the cards.
                if up and now - self.fallback_at >= FALLBACK_EVERY:
                    self.fallback_at = now
                    self.tally = self.http_tally(why)
        with self.lock:
            self.notes = notes
            self.publish(self.build(up, notes))

    def tally_query(self):
        """Today on every check; the days before only every
        HISTORY_EVERY seconds: they are most of the work."""
        today = self.psql.query(TODAY_SQL)
        if self.hist is None or self.hist["day"] != today["day"] or \
                time.monotonic() - self.hist_at >= HISTORY_EVERY:
            self.hist = self.psql.query(HISTORY_SQL)
            self.hist_at = time.monotonic()
            if self.hist["day"] != today["day"]:  # midnight just passed
                today = self.psql.query(TODAY_SQL)
        return merge_tally(self.hist, today)

    def set_keys(self, keys):
        """Take a fresh card list, unless it began before one of our
        own changes: then that seat's cards come from the change."""
        with self.lock:
            for seat, (ks, at) in list(self.overrides.items()):
                if self.mine <= at:
                    keys = [k for k in keys
                            if k.get("key_alias") != seat] + ks
                else:
                    del self.overrides[seat]
            self.raw_keys = keys

    def load_game(self):
        """Which cards the office game made. Read with the cards, since
        only they use it."""
        try:
            self.game = self.games.get()
        except Exception as e:  # the game is optional: never fail here
            log("could not read the game's seats: " + repr(e)[:160])

    def show_change(self, seat, gone):
        """Put a new or cancelled card in the shared answer now, with
        one small lookup instead of a whole check."""
        keys = []
        if not gone:
            px = Proxy(self.settings)
            try:
                keys = px.json("/key/list?return_full_object=true"
                               "&key_alias=" + quote(seat))["keys"]
            except (OSError, ValueError, KeyError, ProxyError,
                    http.client.HTTPException):
                return self.refresh_now()  # the card exists; just slower
            finally:
                px.close()
        with self.lock:
            self.overrides[seat] = (keys, self.starts)
            self.raw_keys = [k for k in self.raw_keys
                             if k.get("key_alias") != seat] + keys
            self.publish(self.build(True, self.notes))

    def load_slow(self):
        info = self.proxy.json("/health/readiness/details")
        self.litellm = info.get("litellm_version")
        self.db = info.get("db")
        models = self.proxy.json("/v1/models").get("data") or []
        self.models = sorted(m["id"] for m in models if m.get("id"))

    def load_keys(self):
        keys, page = [], 1
        while page <= 50:
            j = self.proxy.json("/key/list?return_full_object=true"
                                f"&size=100&page={page}")
            keys += j.get("keys") or []
            if page >= (j.get("total_pages") or 1):
                break
            page += 1
        # A card made or cancelled between pages can shift another
        # across a page edge: drop repeats.
        seen, out = set(), []
        for k in keys:
            if k.get("token") not in seen:
                seen.add(k.get("token"))
                out.append(k)
        return out

    def http_tally(self, why):
        """Fallback without psql: today from the proxy's own API, and
        for all time only what it still knows, at least today."""
        day = datetime.now(timezone.utc).strftime("%Y-%m-%d")
        rows = {}

        def add(kind, name, spend=0, today=None):
            r = rows.setdefault((kind, name), {
                "k": kind, "name": name, "spend": None, "tokens": None,
                "reqs": None, "fails": None, "t_spend": 0, "t_tokens": 0,
                "t_reqs": 0, "t_fails": 0})
            if today:
                r["t_spend"] += today.get("spend") or 0
                r["t_tokens"] += today.get("total_tokens") or 0
                r["t_reqs"] += today.get("api_requests") or 0
                r["t_fails"] += today.get("failed_requests") or 0
            else:
                r["spend"] = (r["spend"] or 0) + spend
        try:
            j = self.proxy.json(f"/user/daily/activity?start_date={day}"
                                f"&end_date={day}&page_size=1000")
            for res in j.get("results") or []:
                groups = (res.get("breakdown") or {}).get(
                    "model_groups") or {}
                for model, m in groups.items():
                    add("model", model, today=m.get("metrics"))
                    for kb in (m.get("api_key_breakdown") or {}).values():
                        alias = (kb.get("metadata") or {}).get(
                            "key_alias") or ""
                        add("seat", alias, today=kb.get("metrics"))
        except (OSError, ValueError, ProxyError,
                http.client.HTTPException):
            pass
        for k in self.raw_keys:
            add("seat", k.get("key_alias") or "", k.get("spend") or 0)
        # A card that refills keeps only its spend since the last
        # refill, a cancelled one none, and the proxy keeps no request
        # or token totals at all: all time is at least today.
        for r in rows.values():
            for f in ("spend", "tokens", "reqs", "fails"):
                r[f] = max(r[f] or 0, r["t_" + f])
        return {"day": day, "rows": list(rows.values()),
                "source": "proxy", "why": why}

    def build(self, up, notes):
        now = datetime.now(timezone.utc)
        cards, tokens = [], {}
        for k in self.raw_keys:
            token = k.get("token") or ""
            cid = hmac.new(self.secret, token.encode(),
                           "sha256").hexdigest()[:16]
            tokens[cid] = token
            hint = (k.get("key_name") or "")[-4:]
            cards.append({
                "id": cid,
                "seat": k.get("key_alias") or "",
                "ends": hint,
                "models": k.get("models") or [],
                "budget": k.get("max_budget"),
                "spent": dollars(k.get("spend")),
                # Set if the budget refills: how often, and when next.
                "refills": k.get("budget_duration") or None,
                "resets": utc_text(k.get("budget_reset_at")),
                "expires": k.get("expires"),
                "created": k.get("created_at"),
                "used": k.get("last_active"),
                "state": seat_state(k, now),
                "game": (k.get("key_alias") or "") in self.game,
            })
        cards.sort(key=lambda c: (c["seat"] == "", c["seat"].lower()))
        self.tokens = tokens
        tally = None
        if self.tally:
            live = {c["seat"] for c in cards}
            rows = self.tally.get("rows") or []
            for r in rows:
                for f in ("spend", "t_spend"):
                    if r.get(f) is not None:
                        r[f] = dollars(r[f])
            tally = {
                "day": self.tally.get("day"),
                "source": self.tally.get("source"),
                "why": self.tally.get("why"),
                "seats": [dict(r, live=r["name"] in live)
                          for r in rows if r["k"] == "seat"],
                "models": [r for r in rows if r["k"] == "model"],
            }
        return {
            # Only what changes when something real changes: each new
            # version is announced to every open page.
            "desk": {"up": up, "litellm": self.litellm,
                     "db": self.db, "lan": self.lan,
                     "notes": [self.settings.scrub(n) for n in notes]},
            "models": self.models,
            "defaults": self.settings.defaults,
            "cards": cards,
            "tally": tally,
        }

    def publish(self, state):
        body = json.dumps(state, separators=(",", ":")).encode()
        etag = '"' + hashlib.blake2b(body, digest_size=8).hexdigest() + '"'
        with self.cond:
            if etag != self.etag:
                self.state, self.body, self.etag = state, body, etag
                self.gz = gzip.compress(body, 6, mtime=0)
                self.version += 1
                self.cond.notify_all()
            self.checked = time.time()


class Refused(Exception):
    """A login that was not even checked. wait: seconds to retry."""

    def __init__(self, msg, wait):
        super().__init__(msg)
        self.wait = wait


class WrongPassword(ValueError):
    """A password that was checked and was wrong: it counts."""


class Locked(WrongPassword):
    """The wrong password that locks its device for 10 minutes."""


def digest(password):
    # "surrogatepass": JSON may carry a lone "\ud800", which plain
    # UTF-8 refuses. Such a password is simply wrong.
    return hashlib.sha256(password.encode("utf-8",
                                          "surrogatepass")).digest()


class Gate:
    """Who may use the desk. Without a password, everyone who can
    reach 127.0.0.1 (this computer) may."""

    def __init__(self, password):
        self.password = password
        self.want = digest(password) if password is not None else None
        self.sessions = {}  # cookie -> (expires, csrf, address)
        # Failure times come from the monotonic clock: a system clock
        # set back must not lock anyone out.
        self.fails = {}     # address -> recent failure times
        self.last_fail = float("-inf")  # the last one, any device
        self.open_csrf = secrets.token_urlsafe(24)
        self.lock = threading.Lock()

    def csrf_for(self, cookie, addr):
        """The session's CSRF token, or None if not logged in. A
        session works only from the address that logged in."""
        if self.password is None:
            return self.open_csrf
        if not cookie:
            return None
        with self.lock:
            s = self.sessions.get(cookie)
            if s and s[0] <= time.time():
                del self.sessions[cookie]
            elif s and s[2] == addr:
                return s[1]
        return None

    def login(self, addr, password):
        """(cookie, csrf), or raises WrongPassword with a plain reason,
        or Refused before the password is even checked."""
        now, tick = time.time(), time.monotonic()
        with self.lock:
            for a in [a for a, q in self.fails.items()
                      if not q or q[-1] < tick - 600]:
                del self.fails[a]
            mine = self.fails.setdefault(addr, deque())
            if len(mine) >= LOCK_AFTER:
                # Locked for 10 minutes from the try that locked it: the
                # cleanup above keeps the record until then.
                wait = max(1, math.ceil(mine[-1] + 600 - tick))
                mins = math.ceil(wait / 60)
                mins = "1 minute" if mins == 1 else f"{mins} minutes"
                raise Refused(f"Too many wrong passwords. Wait {mins}.",
                              wait)
            while mine and mine[0] < tick - 600:
                mine.popleft()
            # Devices that got it wrong lately get one try a second
            # between them all: far too slow to guess. A device with no
            # wrong tries is always let through, so nobody is kept out.
            if mine and tick - self.last_fail < FAIL_GAP:
                raise Refused("Too many tries just now. Wait a second "
                              "and try again.", 1)
            if not hmac.compare_digest(digest(password), self.want):
                mine.append(tick)
                self.last_fail = tick
                left = LOCK_AFTER - len(mine)
                if left <= 0:
                    raise Locked("Wrong password. That was the last "
                                 "try: wait 10 minutes.")
                tries = "1 try" if left == 1 else f"{left} tries"
                raise WrongPassword(f"Wrong password. {tries} left "
                                    "before a 10-minute lock.")
            self.fails.pop(addr, None)
            for c in [c for c, s in self.sessions.items() if s[0] < now]:
                del self.sessions[c]
            if len(self.sessions) >= 100:
                oldest = min(self.sessions, key=self.sessions.get)
                del self.sessions[oldest]
            cookie = secrets.token_urlsafe(32)
            csrf = secrets.token_urlsafe(24)
            self.sessions[cookie] = (now + SESSION_HOURS * 3600, csrf,
                                     addr)
            return cookie, csrf

    def logout(self, cookie):
        with self.lock:
            self.sessions.pop(cookie, None)


class App:
    def __init__(self, args, settings, desk, gate):
        self.args = args
        self.settings = settings
        self.desk = desk
        self.gate = gate
        self.lan = args.lan
        self.docker = args.docker
        if self.docker:
            self.tool = "mint-key"  # the Go one, in the image
            self.mint = Path(shutil.which("mint-key") or
                             "/usr/local/bin/mint-key")
        else:
            self.tool = "mint-key.sh"
            self.mint = REPO / "mint-key.sh"
        self.work = threading.Lock()  # one mint or revoke at a time
        # One name per port, so two desks on one machine don't mix.
        self.cookie_name = f"fd_{args.public_port}"
        self.local_hosts = {f"{h}:{args.port}" for h in
                            ("127.0.0.1", "localhost", "[::1]")}
        if self.docker:
            # The container's own name means nothing to a browser.
            self.names = {"localhost", *(n.lower() for n in args.host)}
        else:
            me = socket.gethostname().lower()
            self.names = {"localhost", me, me + ".local",
                          me.split(".")[0] + ".local",
                          *(n.lower() for n in args.host)}
        self.static = {}
        types = {".html": "text/html; charset=utf-8",
                 ".js": "text/javascript; charset=utf-8",
                 ".css": "text/css; charset=utf-8",
                 ".svg": "image/svg+xml"}
        for f in STATIC.iterdir():
            if f.suffix in types:
                body = f.read_bytes()
                tag = hashlib.blake2b(body, digest_size=8).hexdigest()
                self.static["/" + f.name] = (
                    body, gzip.compress(body, 9, mtime=0), f'"{tag}"',
                    types[f.suffix])
        self.static["/"] = self.static["/index.html"]

    def host_ok(self, host):
        """Blocks DNS rebinding: another site's name pointed at this
        machine. Local: 127.0.0.1 only. --lan: an IP address, or a
        name this machine goes by (or one given with --host).
        --docker: an IP address, localhost, or a name listed in
        FRONTDESK_HOSTS, on any port, since Docker picks the port."""
        if not self.lan and not self.docker:
            return host in self.local_hosts
        try:
            u = urlsplit("//" + host)
            name, port = u.hostname, u.port or 80
        except ValueError:
            return False
        if not name or (not self.docker and port != self.args.port):
            return False
        if name in self.names:
            return True
        try:
            ipaddress.ip_address(name)
        except ValueError:
            return False
        return True

    def run_mint(self, args):
        if self.docker:
            # Settings come from the environment, as they came to us;
            # SEATS_ENV names the image's empty stand-in file.
            env = {k: v for k, v in os.environ.items() if k in MINT_ENV}
        else:
            env = dict(os.environ, SEATS_ENV=str(self.settings.path))
        return subprocess.run([str(self.mint), *args], cwd=REPO, env=env,
                              capture_output=True, text=True, timeout=30)

    def plain_error(self, text):
        """mint-key.sh's complaint in plain words, minus anything that
        looks like a key or a hash."""
        t = SECRET_RE.sub("[hidden]", self.settings.scrub(text or ""))
        last = [x for x in t.strip().splitlines() if x.strip()][-1:]
        log(f"{self.tool} said: " + (last[0][:300] if last else
                                     "nothing"))
        if "already exists" in t:
            return ("That seat name is taken. Cancel its card first, or "
                    "pick a new name, like laptop-2.")
        if "Authentication Error" in t or "error: 401" in t:
            return MASTER_REFUSED_DOCKER if self.docker else MASTER_REFUSED
        if "No keys found" in t or "error: 404" in t:
            return "No such key card. It may be cancelled already."
        # The Go mint-key: the proxy got the request, but was too slow.
        if "may have made the key anyway" in t:
            return ("The proxy took too long to answer, and may have made "
                    "the card anyway. If it shows up in a few seconds, "
                    "cancel it and make it again: its key was never "
                    "shown.")
        if "may have revoked it anyway" in t:
            return ("The proxy took too long to answer, and may have "
                    "cancelled the card anyway. Check again in a few "
                    "seconds.")
        if "Failed to connect" in t or "(7)" in t or "(52)" in t:
            return ("The proxy is not answering. Is it running? Check "
                    "with: docker compose ps")
        if "change-me" in t:
            return "seats.env still has change-me values. Do Step 3a."
        return f"{self.tool} said: " + (last[0][:300] if last else
                                        "nothing (it failed)")


SECURITY_HEADERS = (
    ("Content-Security-Policy",
     "default-src 'self'; img-src 'self' data:; style-src 'self'; "
     "script-src 'self'; connect-src 'self'; base-uri 'none'; "
     "form-action 'self'; frame-ancestors 'none'"),
    ("X-Content-Type-Options", "nosniff"),
    ("X-Frame-Options", "DENY"),
    ("Referrer-Policy", "no-referrer"),
)


class Server(ThreadingHTTPServer):
    daemon_threads = True
    # socketserver's default backlog is 5: a burst of page loads then
    # waits 1-3 s for SYN retries (measured: 50 at once, 4.5 s worst).
    request_queue_size = 128
    per_device = None   # most open connections from one address

    def __init__(self, *args):
        super().__init__(*args)
        self.slots = threading.BoundedSemaphore(MAX_CONNECTIONS)
        self.open = {}  # address -> its open connections
        self.open_lock = threading.Lock()

    def take(self, addr):
        """Room for one more connection from addr? Then it is held."""
        if not self.slots.acquire(blocking=False):
            return False
        if self.per_device:
            with self.open_lock:
                n = self.open.get(addr, 0)
                if n >= self.per_device:
                    self.slots.release()
                    return False
                self.open[addr] = n + 1
        return True

    def give_back(self, addr):
        if self.per_device:
            with self.open_lock:
                n = self.open.pop(addr, 1) - 1
                if n > 0:
                    self.open[addr] = n
        self.slots.release()

    def process_request(self, request, client_address):
        # One thread per connection, so cap them, in all and per
        # device: past a cap, a new connection is closed at once.
        if not self.take(client_address[0]):
            self.shutdown_request(request)
            return
        try:
            super().process_request(request, client_address)
        except Exception:
            self.give_back(client_address[0])
            raise

    def process_request_thread(self, request, client_address):
        try:
            super().process_request_thread(request, client_address)
        finally:
            self.give_back(client_address[0])

    def handle_error(self, request, client_address):
        # A device that hung up before its answer went out is no news.
        # socketserver would print a traceback for each one, so any
        # device could fill the terminal.
        e = sys.exc_info()[1]
        if isinstance(e, OSError):
            return
        log("error: " + self.app.settings.scrub(repr(e))[:200])


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "FrontDesk"
    sys_version = ""
    wbufsize = 1 << 16  # headers and body leave in one packet
    timeout = READ_TIMEOUT

    def setup(self):
        super().setup()
        # No 40 ms Nagle stall on keep-alive (measured: 438 -> 10k rps).
        self.connection.setsockopt(socket.IPPROTO_TCP,
                                   socket.TCP_NODELAY, 1)

    def log_message(self, *args):
        pass  # requests are not logged: nothing secret ends up there

    @property
    def app(self):
        return self.server.app

    def send(self, code, body=b"", ctype="application/json",
             headers=(), gz=None, cache="no-store"):
        if isinstance(body, (dict, list)):
            body = json.dumps(body).encode()
        zipped = gz is not None and \
            "gzip" in self.headers.get("Accept-Encoding", "")
        data = gz if zipped else body
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", cache)
        if gz is not None:
            self.send_header("Vary", "Accept-Encoding")
        if zipped:
            self.send_header("Content-Encoding", "gzip")
        for k, v in (*SECURITY_HEADERS, *headers):
            self.send_header(k, v)
        if self.command == "POST" or self.close_connection:
            # A refused POST may leave its body unread: don't reuse.
            self.close_connection = True
            self.send_header("Connection", "close")
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(data)

    def fail(self, code, msg, headers=(), field=None):
        body = {"error": msg}
        if field:
            body["field"] = field  # the form field to fix
        self.send(code, body, headers=headers)

    def send_error(self, code, message=None, explain=None):
        """http.server's own errors (a bad request line, a method we
        don't serve): short JSON with the usual safety headers."""
        self.close_connection = True
        try:
            phrase = HTTPStatus(code).phrase
        except ValueError:
            phrase = "Error"
        self.fail(code, phrase + ".")

    def cookie(self):
        c = SimpleCookie()
        try:
            c.load(self.headers.get("Cookie", ""))
        except Exception:
            return None
        name = self.app.cookie_name
        return c[name].value if name in c else None

    def addr(self):
        return self.client_address[0]

    def host_ok(self):
        return self.app.host_ok(self.headers.get("Host", ""))

    def wrong_host(self):
        if self.app.docker:
            return self.fail(421, "Open Front Desk by this computer's "
                             "IP address, like http://192.168.1.20:"
                             f"{self.app.args.public_port}, or add the "
                             "name you used to FRONTDESK_HOSTS in "
                             ".env (gui/README.md).")
        if self.app.lan:
            return self.fail(421, "Open Front Desk by this computer's "
                             "address, like http://192.168.1.20:"
                             f"{self.app.args.port}, or start it with "
                             "--host NAME.")
        return self.fail(421, "Open Front Desk at http://127.0.0.1:"
                         f"{self.app.args.port}")

    def do_GET(self):
        try:
            self.route_get(self.path.split("?", 1)[0])
        except (BrokenPipeError, ConnectionResetError, TimeoutError):
            # Gone, or too slow to send its request: just hang up.
            self.close_connection = True
        except Exception as e:
            log("error: " + self.app.settings.scrub(repr(e))[:200])
            self.fail(500, "Something went wrong at the desk.")

    def do_HEAD(self):
        # Headers only, for the page's own files.
        if self.path.split("?", 1)[0] in self.app.static:
            return self.do_GET()
        self.fail(405, "Use GET.")

    def do_POST(self):
        try:
            self.route_post(self.path.split("?", 1)[0])
        except (BrokenPipeError, ConnectionResetError, TimeoutError):
            # Gone, or too slow to send its request: just hang up.
            self.close_connection = True
        except Exception as e:
            log("error: " + self.app.settings.scrub(repr(e))[:200])
            self.fail(500, "Something went wrong at the desk. " + (
                "See its log: docker compose logs frontdesk"
                if self.app.docker else
                "See the terminal where Front Desk runs."))

    def route_get(self, path):
        app = self.app
        if not self.host_ok():
            return self.wrong_host()
        if path in app.static:
            body, gz, etag, ctype = app.static[path]
            if self.headers.get("If-None-Match") == etag:
                return self.send(304, cache="no-cache",
                                 headers=(("ETag", etag),))
            return self.send(200, body, ctype, gz=gz, cache="no-cache",
                             headers=(("ETag", etag),))
        csrf = app.gate.csrf_for(self.cookie(), self.addr())
        if path == "/api/session":
            return self.send(200, {
                "lan": app.lan or app.docker and self.over_network(),
                "docker": app.docker,
                "need_login": app.gate.password is not None,
                "csrf": csrf,
                "seat_url": self.seat_url() if csrf else None})
        if path not in ("/api/state", "/api/events"):
            return self.fail(404, "Nothing here.")
        if csrf is None:
            return self.fail(401, "Log in first.")
        if path == "/api/events":
            return self.events()
        app.desk.fresh()
        d = app.desk
        etag, body, gz = d.etag, d.body, d.gz
        if self.headers.get("If-None-Match") == etag:
            return self.send(304, cache="no-cache, private",
                             headers=(("ETag", etag),))
        self.send(200, body, gz=gz, cache="no-cache, private",
                  headers=(("ETag", etag),))

    def events(self):
        """Server-Sent Events: a short 'changed' on every new state,
        a tick each PING_EVERY seconds. The page then fetches
        /api/state, which is gzipped once for every viewer and
        answers 304 when nothing is new. One poller feeds it all."""
        d = self.app.desk
        with d.cond:
            full = d.viewers >= MAX_STREAMS
            if not full:
                d.viewers += 1
        if full:
            return self.fail(503, "Too many open pages.")
        self.close_connection = True
        try:
            d.fresh()
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-store")
            self.send_header("Connection", "close")
            for k, v in SECURITY_HEADERS:
                self.send_header(k, v)
            self.end_headers()
            self.wfile.write(b"retry: 3000\n\n")
            sent, wrote = None, time.monotonic()
            cookie, addr = self.cookie(), self.addr()
            while self.app.gate.csrf_for(cookie, addr) is not None:
                # Wake each second to notice a closed page quickly,
                # not only at the next write.
                with d.cond:
                    d.cond.wait_for(lambda: d.version != sent,
                                    timeout=1.0)
                    v, checked = d.version, d.checked
                if self.gone():
                    break
                if v != sent:
                    sent = v
                    msg = b'event: changed\ndata: {"v":%d}\n\n' % v
                elif time.monotonic() - wrote >= PING_EVERY:
                    age = max(0.0, time.time() - checked)
                    msg = b'event: tick\ndata: {"age":%.1f}\n\n' % age
                else:
                    continue
                self.wfile.write(msg)
                self.wfile.flush()
                wrote = d.seen = time.monotonic()
        except OSError:
            pass
        finally:
            with d.cond:
                d.viewers -= 1

    def gone(self):
        """True once the browser closed this stream."""
        try:
            ready, _, _ = select.select([self.connection], [], [], 0)
            return bool(ready) and not self.connection.recv(
                1, socket.MSG_PEEK)
        except OSError:
            return True

    def read_json(self):
        if self.headers.get("Content-Type", "").split(";")[0] != \
                "application/json":
            raise ValueError("Send JSON.")
        n = int(self.headers.get("Content-Length") or 0)
        if n < 0 or n > 8192:
            raise ValueError("Too much data.")
        raw = self.rfile.read(n) or b"{}"
        try:
            data = json.loads(raw)
        except RecursionError:  # nested too deep, on Python 3.11
            raise ValueError("Send valid JSON.") from None
        if not isinstance(data, dict):
            raise ValueError("Send a JSON object.")
        return data

    def route_post(self, path):
        app = self.app
        if not self.host_ok():
            return self.wrong_host()
        origin = self.headers.get("Origin")
        host = self.headers.get("Host", "")
        if origin and origin != "http://" + host:
            return self.fail(403, "Requests from other sites are "
                             "refused.")
        try:
            data = self.read_json()
        except ValueError as e:
            return self.fail(400, str(e) if str(e).endswith(".")
                             else "Send valid JSON.")
        if path == "/api/login":
            return self.login(data)
        csrf = app.gate.csrf_for(self.cookie(), self.addr())
        if csrf is None:
            return self.fail(401, "Log in first.")
        given = self.headers.get("X-CSRF-Token", "")
        if not hmac.compare_digest(given.encode(), csrf.encode()):
            return self.fail(403, "Reload the page and try again.")
        if path == "/api/logout":
            app.gate.logout(self.cookie())
            return self.send(200, {"ok": True}, headers=(
                ("Set-Cookie", f"{app.cookie_name}=; Max-Age=0; Path=/; "
                 "HttpOnly; SameSite=Strict"),))
        if path == "/api/mint":
            return self.mint(data)
        if path == "/api/revoke":
            return self.revoke(data)
        self.fail(404, "Nothing here.")

    def login(self, data):
        app, gate = self.app, self.app.gate
        if gate.password is None:
            return self.send(200, {"csrf": gate.open_csrf,
                                   "seat_url": self.seat_url()})
        pw = data.get("password")
        if not isinstance(pw, str) or len(pw) > 200:
            return self.fail(400, "Type the password.")
        try:
            cookie, csrf = gate.login(self.addr(), pw)
        except Refused as e:
            # Not logged: a locked device could flood the terminal. The
            # lock was logged once, as it began.
            return self.fail(429, str(e), headers=(("Retry-After",
                                                     str(e.wait)),))
        except WrongPassword as e:
            locked = isinstance(e, Locked)
            log(f"wrong password from {self.addr()}" +
                (": locked for 10 minutes" if locked else ""))
            return self.fail(401, str(e))
        log(f"logged in: {self.addr()}")
        line = (f"{app.cookie_name}={cookie}; Max-Age="
                f"{SESSION_HOURS * 3600}; Path=/; HttpOnly; "
                "SameSite=Strict")
        self.send(200, {"csrf": csrf, "seat_url": self.seat_url()},
                  headers=(("Set-Cookie", line),))

    def host_name(self):
        """The name or address this viewer typed, or "" if odd."""
        try:
            name = urlsplit("//" + self.headers.get("Host", "")).hostname
        except ValueError:
            return ""
        return name if name and HOST_RE.fullmatch(name) else ""

    def over_network(self):
        """With --docker: did this page come over a network? Then it
        warns of plain http. Behind Docker's port mapping, the address
        the viewer typed is all there is to go by."""
        name = self.host_name()
        try:
            return not ipaddress.ip_address(name).is_loopback
        except ValueError:
            return name != "localhost"

    def seat_url(self):
        """The base URL a seat types in: this machine, proxy port."""
        app = self.app
        if app.args.seat_url:
            return app.args.seat_url
        if app.docker:
            # A container can't see the computer's own address. The
            # proxy is published on the one this viewer typed, so use
            # it; the page says to set FRONTDESK_SEAT_URL if wrong.
            host, port = self.host_name() or "<proxy-ip>", SEAT_PORT
        else:
            host = None
            if app.lan:
                # The address this viewer used already works on the LAN.
                name = self.host_name()
                if name and name not in LOOPBACK:
                    host = name
            host, port = host or lan_ip() or "<proxy-ip>", \
                app.settings.port
        if ":" in host:
            host = f"[{host}]"
        return f"http://{host}:{port}"

    def mint(self, data):
        app, d = self.app, self.app.desk
        d.fresh()
        state = d.state or {}
        seat = data.get("seat")
        seat = seat.strip() if isinstance(seat, str) else ""
        if not SEAT_RE.fullmatch(seat):
            return self.fail(400, "Seat names use letters, digits, "
                             "dots, dashes and underscores, up to 40, "
                             "with no spaces.", field="seat")
        # The game mints its own seats; one made here would clash.
        if d.games.owns(seat):
            pre = d.games.prefix
            what = f"Names that start with {pre} are" if pre and \
                seat.lower().startswith(pre) else "That name is"
            return self.fail(400, f"{what} kept for the office game. "
                             "Pick another name.", field="seat")
        if not (state.get("desk") or {}).get("up"):
            return self.fail(503, "The proxy is not answering, so no "
                             "card can be made right now.")
        models = data.get("models")
        if not isinstance(models, list) or not models or not all(
                isinstance(m, str) and m in state["models"]
                for m in models):
            return self.fail(400, "Pick at least one model from the "
                             "list.", field="models")
        models = sorted(set(models))
        budget = str(data.get("budget", "")).strip()
        if not BUDGET_RE.fullmatch(budget) or float(budget) <= 0:
            return self.fail(400, "Budget is a number of US dollars "
                             "above 0, like 5 or 2.50.", field="budget")
        refill = data.get("refill", "")
        if not isinstance(refill, str) or refill not in REFILLS:
            return self.fail(400, "Pick how often the budget refills: "
                             "never, every day, every week or every "
                             "month.", field="refill")
        duration = str(data.get("duration", "")).strip()
        if not DURATION_RE.fullmatch(duration):
            return self.fail(400, "Pick how long the card lasts.",
                             field="duration")
        # Sam and sam would split one person's cards and tally.
        taken = next((c["seat"] for c in state["cards"]
                      if c["seat"].lower() == seat.lower()), None)
        if taken:
            return self.fail(409, f"{taken} already has a key card. "
                             "Cancel it first, or pick a new name.",
                             field="seat")
        args = [seat, "--models", ",".join(models), "--budget", budget,
                "--duration", duration]
        if refill:
            args += ["--budget-duration", refill]
        with app.work:
            r = app.run_mint(args)
        key = r.stdout.strip()
        if r.returncode != 0 or not KEY_RE.fullmatch(key):
            log(f"mint failed for seat {seat}")
            msg = app.plain_error(r.stderr)
            return self.fail(400, msg, field="seat" if "taken" in msg
                             else None)
        log(f"minted a key card for seat {seat}")
        d.show_change(seat, gone=False)
        self.send(200, {"seat": seat, "key": key, "models": models,
                        "budget": budget, "duration": duration,
                        "refill": refill, "base_url": self.seat_url()})

    def revoke(self, data):
        app, d = self.app, self.app.desk
        d.fresh()
        cards = (d.state or {}).get("cards") or []
        seat, cid = data.get("seat"), data.get("id")
        card = next((c for c in cards if c["id"] == cid), None) or \
            next((c for c in cards if seat and c["seat"] == seat), None)
        if card is None:
            return self.fail(404, "No key card like that. It may be "
                             "cancelled already.")
        if card["seat"]:
            args, what = ["--revoke-seat", card["seat"]], card["seat"]
        else:
            token = d.tokens.get(card["id"])
            if not token:
                return self.fail(404, "That card is gone. Reload.")
            args, what = ["--revoke", token], "(no seat name)"
        with app.work:
            r = app.run_mint(args)
        if r.returncode != 0:
            log(f"revoke failed for seat {what}")
            return self.fail(400, app.plain_error(r.stderr))
        log(f"cancelled the key card for seat {what}")
        if card["seat"]:
            d.show_change(card["seat"], gone=True)
        else:
            d.refresh_now()
        self.send(200, {"ok": True, "seat": card["seat"]})


AGAIN = "then run: docker compose --profile frontdesk up -d"


def docker_settings(args):
    """--docker: Compose hands seats.env over as environment variables
    (env_file in compose.yaml), FRONTDESK_PASSWORD among them. The
    other FRONTDESK_ settings come from its shell or .env: the port it
    shows the page on, the names it may go by, the seats' base URL."""
    settings = Settings(None, os.environ)
    if not settings.master:
        sys.exit("LITELLM_MASTER_KEY is not set. Put it in seats.env, "
                 "next to compose.yaml (README Step 3a), " + AGAIN)
    port = os.environ.get("FRONTDESK_PORT", "")
    args.public_port = int(port) if port.isdigit() and \
        0 < int(port) < 65536 else args.port
    hosts = os.environ.get("FRONTDESK_HOSTS", "")
    args.host += [n for n in re.split(r"[\s,]+", hosts) if n]
    args.seat_url = args.seat_url or \
        os.environ.get("FRONTDESK_SEAT_URL") or None
    return settings


def docker_banner(args, password, shown):
    port = args.public_port
    bind = os.environ.get("FRONTDESK_BIND", "127.0.0.1")
    try:
        local = ipaddress.ip_address(bind).is_loopback
    except ValueError:
        local = bind == "localhost"
    print("Front Desk is open, in Docker.")
    print(f"  On this computer: http://127.0.0.1:{port}")
    if local:
        print("  Other devices: put FRONTDESK_BIND=0.0.0.0 in .env "
              "(gui/README.md)")
    else:
        print(f"  On your network:  http://<proxy-ip>:{port}")
        print("  Plain http: use it only on a network you trust.")
    if shown:
        print(f"  Password, new at each start: {password}")
        print("  To keep one, set FRONTDESK_PASSWORD in seats.env (12 "
              "characters or more), " + AGAIN)
    else:
        print("  Password: the one in FRONTDESK_PASSWORD in seats.env")
    print("Stop it with: docker compose stop frontdesk", flush=True)


def main():
    if sys.version_info < (3, 11):
        sys.exit("Front Desk needs Python 3.11 or newer.")
    ap = argparse.ArgumentParser(
        description="Front Desk: a web page for the proxy's key cards.")
    ap.add_argument("--lan", action="store_true",
                    help="let phones and laptops on your network in "
                    "(asks for a password)")
    ap.add_argument("--port", type=int, default=8040,
                    help="port for the page (default 8040)")
    ap.add_argument("--login", action="store_true",
                    help="ask for a password on this computer too")
    ap.add_argument("--host", metavar="NAME", action="append", default=[],
                    help="with --lan, a name for this machine that "
                    "browsers may use, besides its IP address")
    ap.add_argument("--seat-url", metavar="URL",
                    help="base URL shown to seats, if the guess is wrong")
    ap.add_argument("--env", metavar="FILE",
                    default=os.environ.get("SEATS_ENV",
                                           str(REPO / "seats.env")),
                    help="secrets file (default: seats.env)")
    ap.add_argument("--docker", action="store_true",
                    help="how gui/Dockerfile runs it: settings from "
                    "the environment, on 0.0.0.0, with a password")
    args = ap.parse_args()

    if args.docker:
        settings = docker_settings(args)
    else:
        args.public_port = args.port
        try:
            settings = Settings(args.env)
        except OSError:
            sys.exit(f"missing {args.env}: do README Step 3 first")
        if not settings.master:
            sys.exit(f"LITELLM_MASTER_KEY: not set in {settings.path}")

    # Read once, then gone from the environment: no child gets it.
    password = os.environ.pop("FRONTDESK_PASSWORD", None) or None
    # In Docker, everyone may come through one port mapping: the
    # proxy machine can't be told from a phone, so all log in.
    lan = args.lan or args.docker
    if lan or args.login:
        if password and len(password) < 12:
            sys.exit("FRONTDESK_PASSWORD must be 12 characters or more" +
                     (". Fix it in seats.env, " + AGAIN if args.docker
                      else "."))
        shown = password is None
        password = password or make_password()
    else:
        password = None

    tally = Pg(settings.db_url) if args.docker else Psql(REPO)
    desk = Desk(settings, tally, lan)
    app = App(args, settings, desk, Gate(password))
    bind = "0.0.0.0" if lan else "127.0.0.1"
    try:
        server = Server((bind, args.port), Handler)
    except OSError as e:
        sys.exit(f"cannot use port {args.port}: {e.strerror}. "
                 f"Try --port {args.port + 1}.")
    server.app = app
    if lan:
        server.per_device = MAX_PER_DEVICE
    threading.Thread(target=desk.run, daemon=True).start()
    desk.kick.set()  # one check now: psql's ~150 ms start is paid here

    if args.docker:
        docker_banner(args, password, shown)
        return serve(server, desk)
    print("Front Desk is open.")
    print(f"  On this computer: http://127.0.0.1:{args.port}")
    if args.lan:
        print(f"  On your network:  http://{lan_ip() or '<proxy-ip>'}:"
              f"{args.port}")
        print("  Plain http: use it only on a network you trust.")
    if password and shown:
        print(f"  Password, shown once: {password}")
    elif password:
        print("  Password: the one in FRONTDESK_PASSWORD")
    else:
        print("  Anyone logged in to this computer can use it; on a "
              "shared one, add --login.")
    print("Stop it with Ctrl+C.", flush=True)
    serve(server, desk)


def serve(server, desk):
    # Docker stops it the same way: STOPSIGNAL SIGINT in gui/Dockerfile.
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print()
    finally:
        desk.psql.stop()


if __name__ == "__main__":
    main()

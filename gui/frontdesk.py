#!/usr/bin/env python3
"""Front Desk: one web page for the proxy's key cards and tally.

  python3 gui/frontdesk.py          # this computer only
  python3 gui/frontdesk.py --lan    # phones and laptops on your network

Runs on the proxy machine, next to seats.env. It reads the master key
from there, like mint-key.sh does, and never sends it to a browser.
Minting and revoking go through ./mint-key.sh. Stdlib only.
"""

import argparse
import gzip
import hashlib
import hmac
import http.client
import ipaddress
import json
import os
import queue
import re
import secrets
import select
import socket
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
READ_TIMEOUT = 10   # seconds a browser gets to send its request
SESSION_HOURS = 12
LOCK_AFTER = 5      # wrong passwords from one device in 10 minutes
LOCK_ALL_AFTER = 20     # from all devices together

SEAT_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,39}")
BUDGET_RE = re.compile(r"[0-9]{1,6}([.][0-9]{1,2})?")
DURATION_RE = re.compile(r"[1-9][0-9]{0,3}[hdw]")
KEY_RE = re.compile(r"sk-[A-Za-z0-9_-]{8,200}")
HOST_RE = re.compile(r"[A-Za-z0-9.-]{1,253}|[0-9A-Fa-f:]{2,39}")
# Key-like text in an error message: keys, key ends, hashes.
SECRET_RE = re.compile(r"sk-\S*|[0-9A-Fa-f]{32,}")

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
    """seats.env, reread when it changes, like mint-key.sh does."""

    def __init__(self, path):
        self.path = Path(path).resolve()
        self.mtime = None
        self.load()

    def load(self):
        mtime = self.path.stat().st_mtime
        env = read_env(self.path)
        self.mtime = mtime
        self.master = env.get("LITELLM_MASTER_KEY", "")
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
            self.warnings.append(
                "seats.env still has change-me values, so mint-key.sh "
                "will refuse to run. Do README Step 3a.")

    def reload_if_changed(self):
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
            raise ProxyError(MASTER_REFUSED)
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


def seat_state(k, now):
    if k.get("blocked"):
        return "blocked"
    exp = parse_time(k.get("expires"))
    if exp and exp <= now:
        return "expired"
    budget = k.get("max_budget")
    if budget is not None and (k.get("spend") or 0) >= budget:
        return "spent"
    return "ok"


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
        all-time spend of live cards only."""
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
                "spent": round(k.get("spend") or 0, 6),
                "expires": k.get("expires"),
                "created": k.get("created_at"),
                "used": k.get("last_active"),
                "state": seat_state(k, now),
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
                        r[f] = round(r[f], 6)
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


class Gate:
    """Who may use the desk. Without a password, everyone who can
    reach 127.0.0.1 (this computer) may."""

    def __init__(self, password):
        self.password = password
        self.sessions = {}  # cookie -> (expires, csrf, address)
        self.fails = {}     # address -> recent failure times
        self.all_fails = deque()
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
        """(cookie, csrf), or raises ValueError with a plain reason."""
        now = time.time()
        with self.lock:
            for a in [a for a, q in self.fails.items()
                      if not q or q[-1] < now - 600]:
                del self.fails[a]
            mine = self.fails.setdefault(addr, deque())
            for q in (mine, self.all_fails):
                while q and q[0] < now - 600:
                    q.popleft()
            if len(mine) >= LOCK_AFTER or \
                    len(self.all_fails) >= LOCK_ALL_AFTER:
                raise PermissionError(
                    "Too many wrong passwords. Wait 10 minutes.")
            given = hashlib.sha256(password.encode()).digest()
            want = hashlib.sha256(self.password.encode()).digest()
            if not hmac.compare_digest(given, want):
                mine.append(now)
                self.all_fails.append(now)
                left = min(LOCK_AFTER - len(mine),
                           LOCK_ALL_AFTER - len(self.all_fails))
                if left <= 0:
                    raise ValueError("Wrong password. That was the last "
                                     "try: wait 10 minutes.")
                tries = "1 try" if left == 1 else f"{left} tries"
                raise ValueError(f"Wrong password. {tries} left before "
                                 "a 10-minute lock.")
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
        self.mint = REPO / "mint-key.sh"
        self.work = threading.Lock()  # one mint or revoke at a time
        # One name per port, so two desks on one machine don't mix.
        self.cookie_name = f"fd_{args.port}"
        self.local_hosts = {f"{h}:{args.port}" for h in
                            ("127.0.0.1", "localhost", "[::1]")}
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
        name this machine goes by (or one given with --host)."""
        if not self.lan:
            return host in self.local_hosts
        try:
            u = urlsplit("//" + host)
            name, port = u.hostname, u.port or 80
        except ValueError:
            return False
        if not name or port != self.args.port:
            return False
        if name in self.names:
            return True
        try:
            ipaddress.ip_address(name)
        except ValueError:
            return False
        return True

    def run_mint(self, args):
        env = dict(os.environ, SEATS_ENV=str(self.settings.path))
        return subprocess.run([str(self.mint), *args], cwd=REPO, env=env,
                              capture_output=True, text=True, timeout=30)

    def plain_error(self, text):
        """mint-key.sh's complaint in plain words, minus anything that
        looks like a key or a hash."""
        t = SECRET_RE.sub("[hidden]", self.settings.scrub(text or ""))
        last = [x for x in t.strip().splitlines() if x.strip()][-1:]
        log("mint-key.sh said: " + (last[0][:300] if last else
                                    "nothing"))
        if "already exists" in t:
            return ("That seat name is taken. Cancel its card first, or "
                    "pick a new name, like laptop-2.")
        if "Authentication Error" in t or "error: 401" in t:
            return MASTER_REFUSED
        if "No keys found" in t or "error: 404" in t:
            return "No such key card. It may be cancelled already."
        if "Failed to connect" in t or "(7)" in t or "(52)" in t:
            return ("The proxy is not answering. Is it running? Check "
                    "with: docker compose ps")
        if "change-me" in t:
            return "seats.env still has change-me values. Do Step 3a."
        return "mint-key.sh said: " + (last[0][:300] if last else
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

    def __init__(self, *args):
        super().__init__(*args)
        self.slots = threading.BoundedSemaphore(MAX_CONNECTIONS)

    def process_request(self, request, client_address):
        # One thread per connection, so cap them: past the cap, a new
        # connection is closed at once.
        if not self.slots.acquire(blocking=False):
            self.shutdown_request(request)
            return
        try:
            super().process_request(request, client_address)
        except Exception:
            self.slots.release()
            raise

    def process_request_thread(self, request, client_address):
        try:
            super().process_request_thread(request, client_address)
        finally:
            self.slots.release()


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
            self.fail(500, "Something went wrong at the desk. "
                      "See the terminal where Front Desk runs.")

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
                "lan": app.lan,
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
        data = json.loads(self.rfile.read(n) or b"{}")
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
        except PermissionError as e:
            log(f"login refused for {self.addr()}: too many")
            return self.fail(429, str(e), headers=(("Retry-After",
                                                     "600"),))
        except ValueError as e:
            log(f"wrong password from {self.addr()}")
            return self.fail(401, str(e))
        log(f"logged in: {self.addr()}")
        line = (f"{app.cookie_name}={cookie}; Max-Age="
                f"{SESSION_HOURS * 3600}; Path=/; HttpOnly; "
                "SameSite=Strict")
        self.send(200, {"csrf": csrf, "seat_url": self.seat_url()},
                  headers=(("Set-Cookie", line),))

    def seat_url(self):
        """The base URL a seat types in: this machine, proxy port."""
        app = self.app
        if app.args.seat_url:
            return app.args.seat_url
        host = None
        if app.lan:
            # The address this viewer used already works on the LAN.
            name = urlsplit("//" + self.headers.get("Host", "")).hostname
            if name and HOST_RE.fullmatch(name) and \
                    name not in ("localhost", "127.0.0.1", "::1"):
                host = name
        host = host or lan_ip() or "<proxy-ip>"
        if ":" in host:
            host = f"[{host}]"
        return f"http://{host}:{app.settings.port}"

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
        with app.work:
            r = app.run_mint([seat, "--models", ",".join(models),
                              "--budget", budget, "--duration",
                              duration])
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
                        "base_url": self.seat_url()})

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
    args = ap.parse_args()

    try:
        settings = Settings(args.env)
    except OSError:
        sys.exit(f"missing {args.env}: do README Step 3 first")
    if not settings.master:
        sys.exit(f"LITELLM_MASTER_KEY: not set in {settings.path}")

    # Read once, then gone from the environment: no child gets it.
    password = os.environ.pop("FRONTDESK_PASSWORD", None) or None
    if args.lan or args.login:
        if password and len(password) < 12:
            sys.exit("FRONTDESK_PASSWORD must be 12 characters or more.")
        shown = password is None
        password = password or make_password()
    else:
        password = None

    desk = Desk(settings, Psql(REPO), args.lan)
    app = App(args, settings, desk, Gate(password))
    bind = "0.0.0.0" if args.lan else "127.0.0.1"
    try:
        server = Server((bind, args.port), Handler)
    except OSError as e:
        sys.exit(f"cannot use port {args.port}: {e.strerror}. "
                 f"Try --port {args.port + 1}.")
    server.app = app
    threading.Thread(target=desk.run, daemon=True).start()
    desk.kick.set()  # one check now: psql's ~150 ms start is paid here

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
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print()
    finally:
        desk.psql.stop()


if __name__ == "__main__":
    main()

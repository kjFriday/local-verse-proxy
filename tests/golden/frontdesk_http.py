"""Goldens for internal/frontdesk/web: host_ok over a Host corpus in
the three modes, host_name, over_network and seat_url, plain_error on
both mint-keys' complaints, and a real frontdesk.py server (its real
Desk, over a small fixture proxy) driven by raw requests: routes and
methods, headers, bodies, the mint checks (inputs.py, refill_vb,
misc_vb, game_vb), revoke, login and the lock. Minting itself is a
stand-in with scripted answers. Tokens, cookies and ETags are masked;
every key is made up.
python3 -B tests/golden/frontdesk_http.py"""
import argparse
import gzip
import json
import os
import socket
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlsplit

import common

HOSTNAME = "DeskHost.lan"
LAN_IP = "192.168.1.20"
PW = "right-password-123"

FIXTURE = {
    "readiness": {"status": "healthy", "db": "connected",
                  "litellm_version": "1.106.6"},
    "models": {"data": [{"id": "local"}, {"id": "big"}, {"id": ""},
                        {"object": "model"}]},
    "keys": [
        {"token": "tok-sam", "key_alias": "Sam", "key_name": "sk-...a1b2",
         "models": ["local"], "max_budget": 5.0, "spend": 1.25,
         "budget_duration": None, "budget_reset_at": None,
         "expires": "2099-01-01T00:00:00Z",
         "created_at": "2026-10-01T08:00:00Z", "last_active": None,
         "blocked": None},
        {"token": "tok-alice", "key_alias": "alice",
         "key_name": "sk-...c3d4", "models": ["big", "local"],
         "max_budget": None, "spend": 0.0, "budget_duration": "1d",
         "budget_reset_at": "2099-01-02T00:00:00",
         "expires": None, "created_at": "2026-10-02T08:00:00Z",
         "last_active": "2026-10-30T10:00:00Z", "blocked": False},
        {"token": "tok-fern", "key_alias": "game-fern",
         "key_name": "sk-...e5f6", "models": ["local"],
         "max_budget": 1, "spend": 1, "budget_duration": "1d",
         "budget_reset_at": None, "expires": None,
         "created_at": "2026-10-03T08:00:00Z", "last_active": None,
         "blocked": None},
        {"token": "tok-noname", "key_alias": None,
         "key_name": "sk-...0000", "models": [], "max_budget": 2,
         "spend": 0, "budget_duration": None, "budget_reset_at": None,
         "expires": "2020-01-01T00:00:00", "created_at": None,
         "last_active": None, "blocked": None},
    ],
    "activity": {"results": []},
    "rules": '[office]\nseat_prefix = "game-"\n[roster]\n'
             'seats = [{ name = "fern" }, { name = "Moss" }]\n',
}

# What the stand-in mint-key answers, by seat (or "revoke").
MINTS = {
    "made-ok": [0, "sk-fake-minted-key-0001\n", ""],
    "made-refill": [0, "  sk-fake-minted-key-0002  \n", ""],
    "bad-key": [0, "not a key\n", ""],
    "exists": [1, "", "mint-key: the proxy returned error: 400 (Bad "
               "Request): Key with alias 'exists' already exists.\n"],
    "auth": [1, "", "mint-key: the proxy returned error: 401 "
             "(Unauthorized): Authentication Error, sk-fake-leaky"
             "-key-0003 is not valid\n"],
    "gone404": [1, "", "the proxy returned error: 404 (Not Found)\n"],
    "down": [1, "", "Failed to connect to the proxy at "
             "http://localhost:4000 (connection refused)\nIs it "
             "running?\n"],
    "late": [1, "", "the proxy took too long. It got the request but "
             "did not answer in time, so it may have made the key "
             "anyway. To be sure, run this program again.\n"],
    "changeme": [1, "", "LITELLM_MASTER_KEY in seats.env still says "
                 "change-me\n"],
    "other": [1, "", "\n  first line\nsomething odd 9876543210" + "9" * 30 +
              " happened\n\n"],
    "silent": [1, "", ""],
    "revoke": [0, "revoked seat Sam\n", ""],
}

PLAIN = [
    "", "\n\n", "mint-key: error: 401 (Unauthorized)",
    "curl: (7) Failed to connect", "curl: (52) Empty reply",
    "Authentication Error", "No keys found for alias x",
    "the proxy returned error: 404 (Not Found)",
    "x already exists\nmore", "seats.env still has change-me",
    "It got the request but did not answer in time, so it may have "
    "made the key anyway.",
    "so it may have revoked it anyway. Run the same line again.",
    "leak sk-fake-plain-key-qwerty and " + "31" * 18 + " end",
    "hidden sk-fake-settings-master here",
    "a\u2028b\u2029last one\x1cx",
    "x" * 400, "line one\n   \nline two  ",
    "tab\tsk-\u00e9t\u00e9 ok",
]

HOSTS = [
    "127.0.0.1:{p}", "localhost:{p}", "[::1]:{p}", "127.0.0.1",
    "localhost", "127.0.0.1:{q}", "LOCALHOST:{p}", "", "evil.test",
    "evil.test:{p}", "deskhost:{p}", "DeskHost:{p}", "deskhost.lan:{p}",
    "deskhost.local:{p}", "deskhost.lan.local:{p}", "named:{p}",
    "Named:{p}", "named.local:{p}", "10.0.0.5:{p}", "10.0.0.5",
    "10.0.0.5:80", "10.0.0.5:0", "010.0.0.5:{p}", "[fe80::1]:{p}",
    "[fe80::1%eth0]:{p}", "[::ffff:127.0.0.1]:{p}", "fe80::1",
    "user@10.0.0.5:{p}", "10.0.0.5.:{p}", "localhost.:{p}",
    "10.0.0.5:abc", "10.0.0.5:99999", "[::1", "::1]:{p}", "a b:{p}",
    "1.2.3.4:{p}", "256.1.1.1:{p}", "1.2.3:{p}", "[::1]:{q}",
    "x" * 300 + ":{p}", "\u00e9t\u00e9:{p}", "192.168.1.20:8040",
]


def fmt_host(h, port):
    return h.replace("{p}", str(port)).replace("{q}", str(port + 1))


class FixtureProxy(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    state = {"down": False}

    def log_message(self, *a):
        pass

    def do_GET(self):
        u = urlsplit(self.path)
        q = parse_qs(u.query)
        if u.path == "/health/liveliness":
            code = 503 if self.state["down"] else 200
            return self.answer(code, "I'm alive!")
        if u.path == "/health/readiness/details":
            return self.answer(200, FIXTURE["readiness"])
        if u.path == "/v1/models":
            return self.answer(200, FIXTURE["models"])
        if u.path == "/key/list":
            keys = FIXTURE["keys"]
            if "key_alias" in q:
                keys = [k for k in keys
                        if k["key_alias"] == q["key_alias"][0]]
            return self.answer(200, {"keys": keys, "total_pages": 1})
        if u.path == "/user/daily/activity":
            return self.answer(200, FIXTURE["activity"])
        self.answer(404, {"detail": "no"})

    def answer(self, code, body):
        b = (body if isinstance(body, str) else json.dumps(body)).encode()
        self.send_response(code)
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)


class StubPsql:
    def query(self, sql, timeout=8.0):
        raise TallyErrorBox.cls("no database in this test")

    def stop(self):
        pass


class TallyErrorBox:
    cls = None


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    p = s.getsockname()[1]
    s.close()
    return p


def host_cases(fd):
    out = []
    port = 8040
    for mode in ("local", "lan", "docker"):
        args = argparse.Namespace(
            lan=mode == "lan", docker=mode == "docker", port=port,
            public_port=port, host=["Named"], seat_url=None, login=False,
            env=None)
        app = fd.App(args, None, None, None)
        for h in HOSTS:
            host = fmt_host(h, port)
            out.append({"mode": mode, "host": host,
                        "ok": app.host_ok(host)})
    return out


class FakeHandler:
    host_name = fd_host_name = None

    def __init__(self, app, host):
        self.app = app
        self.headers = {"Host": host}


def name_cases(fd, settings):
    out = []
    port = 8040
    H = type("H", (), {"host_name": fd.Handler.host_name,
                       "over_network": fd.Handler.over_network,
                       "seat_url": fd.Handler.seat_url})
    for mode in ("local", "lan", "docker"):
        for seat_url in (None, "http://given.example:4000"):
            for ip in (LAN_IP, None):
                fd.lan_ip = lambda ip=ip: ip
                args = argparse.Namespace(
                    lan=mode == "lan", docker=mode == "docker",
                    port=port, public_port=port, host=[],
                    seat_url=seat_url, login=False, env=None)
                app = fd.App(args, settings, None, None)
                for h in HOSTS:
                    host = fmt_host(h, port)
                    x = H()
                    x.app, x.headers = app, {"Host": host}
                    out.append({"mode": mode, "seat_url": seat_url,
                                "lan_ip": ip, "host": host,
                                "name": x.host_name(),
                                "network": x.over_network(),
                                "url": x.seat_url()})
    return out


def plain_cases(fd, settings):
    out = []
    logs = []
    fd.log = logs.append
    for docker in (False, True):
        args = argparse.Namespace(lan=False, docker=docker, port=8040,
                                  public_port=8040, host=[],
                                  seat_url=None, login=False, env=None)
        app = fd.App(args, settings, None, None)
        for text in PLAIN + [m[2] for m in MINTS.values()]:
            del logs[:]
            # The rename table (spec 11.9): mint-key.sh said is
            # mint-key said (ledger F-3).
            msg = app.plain_error(text).replace("mint-key.sh",
                                                "mint-key")
            out.append({"docker": docker, "text": text, "msg": msg,
                        "log": [x.replace("mint-key.sh", "mint-key")
                                for x in logs]})
    return out


def steps_local():
    S = []

    def get(name, path, **kw):
        S.append(dict(kw, name=name, method=kw.get("method", "GET"),
                      path=path))

    def post(name, path, body, **kw):
        S.append(dict(kw, name=name, method="POST", path=path,
                      body=body if isinstance(body, str) or body is None
                      else json.dumps(body)))

    get("wait", "/api/state", sleep=0)
    get("session", "/api/session", save="csrf")
    for p in ("/", "/index.html", "/app.js", "/app.css", "/icon.svg"):
        get("static " + p, p, save="etag")
        get("static 304 " + p, p, headers=[["If-None-Match", "{etag}"]])
        get("static gz " + p, p, headers=[["Accept-Encoding", "gzip"]])
        get("head " + p, p, method="HEAD")
    for ae in ("gzip", "x-gzip", "GZIP", "deflate, gzip;q=1", ""):
        get("state ae " + ae, "/api/state",
            headers=[["Accept-Encoding", ae]])
    get("state etag", "/api/state", save="etag")
    get("state 304", "/api/state", headers=[["If-None-Match", "{etag}"]])
    get("state 304 wrong", "/api/state",
        headers=[["If-None-Match", '"nope"']])
    get("state empty inm", "/api/state", headers=[["If-None-Match", ""]])
    for p in ("/nothing", "/%61pp.js", "/api/state?x=1", "/app.js?v=2",
              "/api/sessionx", "//app.js", "/static/app.js",
              "/api/../app.js"):
        get("path " + p, p)
    get("head state", "/api/state", method="HEAD")
    get("head nothing", "/nothing", method="HEAD")
    for m in ("PUT", "DELETE", "PATCH", "OPTIONS", "get", "TRACE"):
        get("method " + m, "/", method=m)
    S.append({"name": "options star", "raw": "OPTIONS * HTTP/1.1\r\n"
              "Host: 127.0.0.1:{port}\r\n\r\n"})
    for h in ("localhost:{port}", "[::1]:{port}", "evil.test",
              "127.0.0.1:{port1}", "LOCALHOST:{port}", "127.0.0.1"):
        get("host " + h, "/api/session", host=h)
        post("post host " + h, "/api/logout", {}, host=h)
    # net/http reads a Host with spaces around it trimmed (ledger
    # H-8), and refuses one with bytes outside ASCII itself (H-1).
    for h in ("127.0.0.1:{port} \t", "127.0.0.1:{port}\u0131"):
        get("host " + h, "/api/session", host=h)
    S.append({"name": "http10 no host", "raw": "GET /api/session "
              "HTTP/1.0\r\n\r\n"})
    S.append({"name": "http10 host", "raw": "GET /api/session HTTP/1.0"
              "\r\nHost: 127.0.0.1:{port}\r\n\r\n"})
    S.append({"name": "http11 close", "raw": "GET /api/session HTTP/1.1"
              "\r\nHost: 127.0.0.1:{port}\r\nConnection: close\r\n\r\n"})
    S.append({"name": "long line", "raw": "GET /" + "z" * 70000 +
              " HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\n\r\n"})
    S.append({"name": "101 headers", "raw": "GET /api/session HTTP/1.1"
              "\r\nHost: 127.0.0.1:{port}\r\n" + "".join(
                  "X-H%d: v\r\n" % i for i in range(100)) + "\r\n"})
    S.append({"name": "100 headers", "raw": "GET /api/session HTTP/1.1"
              "\r\nHost: 127.0.0.1:{port}\r\n" + "".join(
                  "X-H%d: v\r\n" % i for i in range(99)) + "\r\n"})
    S.append({"name": "long header", "raw": "GET /api/session HTTP/1.1"
              "\r\nHost: 127.0.0.1:{port}\r\nX-Long: " + "z" * 66000 +
              "\r\n\r\n"})
    # Origin and CSRF.
    for o in ("http://127.0.0.1:{port}", "http://evil.test", "null",
              "https://127.0.0.1:{port}", ""):
        post("origin " + o, "/api/logout", {},
             headers=[["Origin", o]])
    post("no csrf", "/api/logout", {}, csrf=None)
    post("wrong csrf", "/api/logout", {}, csrf="x" * 32)
    post("csrf unicode", "/api/logout", {}, csrf="\u00e9" * 16)
    # Bodies.
    for ct in ("text/plain", "application/json; charset=utf-8",
               "Application/JSON", "application/json;x", None,
               " application/json"):
        post("ct %s" % ct, "/api/revoke", {"seat": "nobody"}, ctype=ct)
    for name, body in (
            ("empty", ""), ("bom", "\u00ef\u00bb\u00bf{}"),
            ("bad utf8", '{"seat": "\u00ff"}'),
            ("utf16", "\u00ff\u00fe{\u0000}\u0000"),
            ("nan", '{"seat": NaN}'), ("inf", '{"seat": Infinity}'),
            ("array", "[1, 2]"), ("string", '"x"'), ("number", "5"),
            ("null", "null"), ("bad", "{nope"), ("trailing", "{} {}"),
            ("space", "  {}  \n"), ("deep", "[" * 2000 + "]" * 2000),
            ("deep obj", '{"a":' * 100 + "1" + "}" * 100),
            ("dup keys", '{"seat": "a", "seat": "nobody"}'),
            ("big", '{"pad": "' + "x" * 8200 + '"}'),
            ("8192", '{"pad": "' + "x" * (8192 - 11) + '"}'),
            ("surrogate", '{"seat": "\\ud800"}')):
        post("body " + name, "/api/revoke", body)
    post("length short", "/api/revoke", "{}", length="2")
    post("length zero", "/api/revoke", '{"seat": "x"}', length="0")
    post("length none", "/api/revoke", None, length=None)
    post("unknown post", "/api/nothing", {})
    post("login open", "/api/login", {"password": "x"})
    # Mint checks (inputs.py, refill_vb, misc_vb, game_vb).
    ok = {"seat": "made-ok", "models": ["local"], "budget": "1",
          "duration": "1d"}
    patches = [
        ("seat 41", {"seat": "q" * 41}), ("seat 40", {"seat": "q" * 40}),
        ("seat dash", {"seat": "-x"}), ("seat space", {"seat": "a b"}),
        ("seat unicode", {"seat": "s\u00e9"}),
        ("seat newline", {"seat": "a\nb"}), ("seat list", {"seat": ["a"]}),
        ("seat null", {"seat": None}), ("seat missing", {"seat": 5}),
        ("seat padded", {"seat": "  made-ok \t"}),
        ("seat trailing nl", {"seat": "made-ok\n"}),
        ("game prefix", {"seat": "game-new"}),
        ("game prefix upper", {"seat": "GAME-x"}),
        ("game roster", {"seat": "game-moss"}),
        ("game player", {"seat": "Game-Fern"}),
        ("taken", {"seat": "sam"}), ("taken exact", {"seat": "Sam"}),
        ("taken alice", {"seat": "ALICE"}),
        ("models empty", {"models": []}),
        ("models unknown", {"models": ["nope"]}),
        ("models str", {"models": "local"}),
        ("models comma", {"models": ["local,big"]}),
        ("models num", {"models": [1]}),
        ("models dup", {"models": ["local", "big", "local"]}),
        ("models empty id", {"models": [""]}),
        ("budget 0", {"budget": "0"}), ("budget -1", {"budget": "-1"}),
        ("budget 1e3", {"budget": "1e3"}),
        ("budget 1.234", {"budget": "1.234"}),
        ("budget 1234567", {"budget": "1234567"}),
        ("budget 999999.99", {"budget": "999999.99"}),
        ("budget num 2.5", {"budget": 2.5}),
        ("budget num 3", {"budget": 3}),
        ("budget num 1e2", {"budget": 1e2}),
        ("budget bool", {"budget": True}),
        ("budget nan", {"budget": "nan"}),
        ("budget pad", {"budget": " 5 "}),
        ("budget 0.00", {"budget": "0.00"}),
        ("budget .5", {"budget": ".5"}),
        ("budget missing", {"budget": None}),
        ("refill 1d", {"refill": "1d", "seat": "made-refill"}),
        ("refill 1w", {"refill": "1w"}), ("refill 1mo", {"refill": "1mo"}),
        ("refill empty", {"refill": ""}), ("refill 2d", {"refill": "2d"}),
        ("refill null", {"refill": None}), ("refill num", {"refill": 1}),
        ("refill 1D", {"refill": "1D"}),
        ("duration 0d", {"duration": "0d"}),
        ("duration 30m", {"duration": "30m"}),
        ("duration 1y", {"duration": "1y"}),
        ("duration 99999d", {"duration": "99999d"}),
        ("duration 9999d", {"duration": "9999d"}),
        ("duration 12mo", {"duration": "12mo"}),
        ("duration flag", {"duration": "--budget"}),
        ("duration num", {"duration": 30}),
        ("duration pad", {"duration": " 7d "}),
        ("bad key", {"seat": "bad-key"}), ("exists", {"seat": "exists"}),
        ("auth", {"seat": "auth"}), ("404", {"seat": "gone404"}),
        ("down", {"seat": "down"}), ("late", {"seat": "late"}),
        ("changeme", {"seat": "changeme"}), ("other", {"seat": "other"}),
        ("silent", {"seat": "silent"}),
    ]
    for name, patch in patches:
        body = dict(ok, **patch)
        post("mint " + name, "/api/mint", body)
    post("mint missing all", "/api/mint", {})
    for name, body in (("seat", {"seat": "Sam"}),
                       ("seat lower", {"seat": "sam"}),
                       ("unknown", {"seat": "nobody"}),
                       ("id zero", {"id": "0" * 16}),
                       ("id noname", {"id": "{noname}"}),
                       ("id num", {"id": 5}),
                       ("seat list", {"seat": ["Sam"]}),
                       ("flag", {"seat": "--revoke"}), ("empty", {}),
                       ("blank seat", {"seat": ""})):
        post("revoke " + name, "/api/revoke", body)
    post("logout", "/api/logout", {})
    S.append({"name": "proxy down", "proxy": "down", "method": "GET",
              "path": "/api/session", "sleep": 3.2})
    post("mint proxy down", "/api/mint", ok)
    S.append({"name": "proxy up", "proxy": "up", "method": "GET",
              "path": "/api/session", "sleep": 3.2})
    get("state after", "/api/state")
    return S


def steps_lan():
    S = []

    def get(name, path, **kw):
        S.append(dict(kw, name=name, method=kw.get("method", "GET"),
                      path=path))

    def post(name, path, body, **kw):
        S.append(dict(kw, name=name, method="POST", path=path,
                      body=body if isinstance(body, str) or body is None
                      else json.dumps(body)))

    get("session out", "/api/session")
    get("state out", "/api/state")
    get("events out", "/api/events")
    for h in ("10.0.0.5:{port}", "deskhost:{port}", "deskhost.local:"
              "{port}", "named:{port}", "evil.test:{port}",
              "10.0.0.5:{port1}", "10.0.0.5", "[::1]:{port}"):
        get("lan host " + h, "/api/session", host=h)
    post("logout out", "/api/logout", {})
    post("login no pw", "/api/login", {})
    post("login num", "/api/login", {"password": 5})
    post("login 201", "/api/login", {"password": "x" * 201})
    post("login 200", "/api/login", {"password": "x" * 200})
    S.append({"name": "gap", "method": "GET", "path": "/api/session",
              "sleep": 1.1})
    post("login surrogate", "/api/login", '{"password": "\\ud800"}')
    for i in range(4):
        S.append({"name": "gap", "method": "GET", "path": "/api/session",
                  "sleep": 1.1})
        post("login wrong %d" % i, "/api/login", {"password": "nope"})
    post("login at once", "/api/login", {"password": PW})
    post("login other device", "/api/login", {"password": PW},
         src="127.0.0.2", save="cookie2")
    get("session other", "/api/session", src="127.0.0.2",
        cookie="{cookie2}", save="csrf2")
    S.append({"name": "gap", "method": "GET", "path": "/api/session",
              "sleep": 1.1})
    post("login locked", "/api/login", {"password": PW})
    get("cookie other address", "/api/state", src="127.0.0.3",
        cookie="{cookie2}")
    post("logout other", "/api/logout", {}, src="127.0.0.2",
         cookie="{cookie2}", csrf="{csrf2}")
    get("after logout", "/api/state", src="127.0.0.2",
        cookie="{cookie2}")
    return S


def apply(text, ctx):
    for k, v in ctx.items():
        text = text.replace("{" + k + "}", str(v))
    return text


def build_raw(step, ctx):
    if "raw" in step:
        return apply(step["raw"], ctx).encode("latin-1")
    host = apply(step.get("host", "127.0.0.1:{port}"), ctx)
    lines = ["%s %s HTTP/1.1" % (step["method"], apply(step["path"],
                                                      ctx))]
    if host:
        lines.append("Host: " + host)
    hdrs = [[k, apply(v, ctx)] for k, v in step.get("headers", [])]
    cookie = apply(step.get("cookie", "{cookie}"), ctx)
    if cookie and "{" not in cookie:
        hdrs.append(["Cookie", "fd_%d=%s" % (ctx["port"], cookie)])
    body = None
    if step["method"] == "POST":
        body = step.get("body")
        ct = step.get("ctype", "application/json")
        if ct is not None:
            hdrs.append(["Content-Type", ct])
        csrf = step.get("csrf", "{csrf}")
        if csrf is not None:
            csrf = apply(csrf, ctx)
            if "{" not in csrf:
                hdrs.append(["X-CSRF-Token", csrf])
        data = apply(body, ctx).encode("latin-1") if body else b""
        length = step.get("length", str(len(data)))
        if length is not None:
            hdrs.append(["Content-Length", length])
        body = data
    for k, v in hdrs:
        lines.append("%s: %s" % (k, v))
    raw = ("\r\n".join(lines) + "\r\n\r\n").encode("utf-8")
    return raw + (body or b"")


def exchange(src, port, raw, method):
    s = socket.socket()
    s.settimeout(15)
    s.bind((src, 0))
    s.connect(("127.0.0.1", port))
    try:
        s.sendall(raw)
    except OSError:
        pass
    data = b""
    while b"\r\n\r\n" not in data:
        try:
            chunk = s.recv(65536)
        except OSError:
            chunk = b""
        if not chunk:
            break
        data += chunk
    head, _, rest = data.partition(b"\r\n\r\n")
    lines = head.decode("latin-1").split("\r\n")
    if not lines[0]:
        s.close()
        return {"status": 0}
    status = int(lines[0].split()[1])
    headers = {}
    for ln in lines[1:]:
        k, _, v = ln.partition(":")
        headers.setdefault(k.strip().lower(), v.strip())
    n = int(headers.get("content-length", "0"))
    if method == "HEAD" or status == 304:
        n = 0
    while len(rest) < n:
        chunk = s.recv(65536)
        if not chunk:
            break
        rest += chunk
    s.close()
    return {"status": status, "headers": headers, "body": rest[:n]}


SHOWN = ["content-type", "content-length", "cache-control", "vary",
         "content-encoding", "connection", "retry-after", "server",
         "content-security-policy", "x-content-type-options",
         "x-frame-options", "referrer-policy", "transfer-encoding"]


def record(resp, ctx, step):
    if not resp["status"]:
        return {"status": 0}
    h = resp["headers"]
    body = resp["body"]
    out = {"status": resp["status"]}
    gz = h.get("content-encoding") == "gzip"
    if gz:
        body = gzip.decompress(body)
    shown = {k: h[k] for k in SHOWN if k in h}
    if gz:
        shown.pop("content-length", None)  # gzip bytes differ: F-4
    if "etag" in h:
        shown["etag"] = "<etag>"
    if "set-cookie" in h:
        name, _, rest = h["set-cookie"].partition("=")
        val, _, attrs = rest.partition(";")
        shown["set-cookie"] = name.replace(str(ctx["port"]), "<port>") + \
            "=" + ("<cookie>" if val else "") + ";" + attrs
    out["headers"] = shown
    text = body.decode("utf-8", "replace")
    try:
        j = json.loads(text)
    except ValueError:
        j = None
    save = step.get("save")
    if isinstance(j, dict):
        if save in ("csrf", "csrf2") and j.get("csrf"):
            ctx[save] = j["csrf"]
        if j.get("csrf"):
            if step.get("src", "127.0.0.1") == "127.0.0.1":
                ctx["csrf"] = j["csrf"]
            j["csrf"] = "<csrf>"
        if "set-cookie" in h and h["set-cookie"].split("=", 1)[1][:1] \
                not in (";", ""):
            c = h["set-cookie"].split("=", 1)[1].split(";")[0]
            ctx[save if save == "cookie2" else "cookie"] = c
        text = json.dumps(j, separators=(",", ":"))
    if save == "etag" and "etag" in h:
        ctx["etag"] = h["etag"]
    # The tally's day is the day it ran.
    text = text.replace(time.strftime("%Y-%m-%d", time.gmtime()),
                        "<day>")
    text = text.replace(":%d" % ctx["fixport"], ":<proxy-port>")
    text = text.replace(":%d" % ctx["port"], ":<port>")
    if j is not None and "mint-key.sh" in text:
        n = text.count("mint-key.sh")
        text = text.replace("mint-key.sh", "mint-key")
        if "content-length" in shown:
            shown["content-length"] = str(int(shown["content-length"]) -
                                          3 * n)
    out["body"] = text
    out["json"] = j is not None
    return out


def serve_python(fd, tmp, mode):
    fix = ThreadingHTTPServer(("127.0.0.1", 0), FixtureProxy)
    FixtureProxy.state["down"] = False
    threading.Thread(target=fix.serve_forever, daemon=True).start()
    fixport = fix.server_address[1]
    env = os.path.join(tmp, mode + ".env")
    with open(env, "w") as f:
        f.write("LITELLM_MASTER_KEY=sk-fake-settings-master\n"
                "PROXY_URL=http://127.0.0.1:%d\n"
                "DEFAULT_MODELS=local\n" % fixport)
    game = os.path.join(tmp, "game-" + mode)
    os.makedirs(game, exist_ok=True)
    with open(os.path.join(game, "rules.toml"), "w") as f:
        f.write(FIXTURE["rules"])
    fd.GAME = Path(game)
    settings = fd.Settings(env)
    lan = mode == "lan"
    desk = fd.Desk(settings, StubPsql(), lan)
    desk.secret = bytes(range(16))
    port = free_port()
    args = argparse.Namespace(lan=lan, docker=False, port=port,
                              public_port=port, host=["named"],
                              seat_url=None, login=False, env=env)
    app = fd.App(args, settings, desk,
                 fd.Gate(PW if lan else None))
    calls = []

    def run_mint(a):
        calls.append(list(a))
        seat = a[0] if not a[0].startswith("--") else "revoke"
        code, out, err = MINTS.get(seat, MINTS["made-ok"])
        return subprocess.CompletedProcess(a, code, out, err)
    app.run_mint = run_mint
    server = fd.Server(("0.0.0.0" if lan else "127.0.0.1", port),
                       fd.Handler)
    server.app = app
    if lan:
        server.per_device = fd.MAX_PER_DEVICE
    threading.Thread(target=desk.run, daemon=True).start()
    desk.kick.set()
    threading.Thread(target=server.serve_forever, daemon=True).start()
    time.sleep(0.3)
    return server, fix, port, fixport, calls, desk


def run_steps(fd, tmp, mode, steps):
    logs = []
    fd.log = lambda m: logs.append(m.replace("mint-key.sh", "mint-key"))
    server, fix, port, fixport, calls, desk = serve_python(fd, tmp, mode)
    noname = fd.hmac.new(desk.secret, b"tok-noname",
                         "sha256").hexdigest()[:16]
    ctx = {"port": port, "port1": port + 1, "fixport": fixport,
           "noname": noname}
    out = []
    for step in steps:
        if "proxy" in step:
            FixtureProxy.state["down"] = step["proxy"] == "down"
        if step.get("sleep"):
            time.sleep(step["sleep"])
        del logs[:]
        n = len(calls)
        raw = build_raw(step, ctx)
        resp = exchange(step.get("src", "127.0.0.1"), port, raw,
                        step.get("method", "GET"))
        time.sleep(0.05)
        rec = record(resp, ctx, step)
        rec["log"] = list(logs)
        rec["mint_args"] = calls[n:]
        out.append({"step": step, "want": rec})
    server.shutdown()
    fix.shutdown()
    return out


def main():
    fd = common.load("frontdesk", "gui/frontdesk.py")
    TallyErrorBox.cls = fd.TallyError
    fd.socket.gethostname = lambda: HOSTNAME
    with tempfile.TemporaryDirectory() as tmp:
        env = os.path.join(tmp, "seats.env")
        with open(env, "w") as f:
            f.write("LITELLM_MASTER_KEY=sk-fake-settings-master\n"
                    "PROXY_URL=http://localhost:4100\n")
        settings = fd.Settings(env)
        hosts = host_cases(fd)
        names = name_cases(fd, settings)
        plain = plain_cases(fd, settings)
        fd.lan_ip = lambda: LAN_IP
        local = run_steps(fd, tmp, "local", steps_local())
        lan = run_steps(fd, tmp, "lan", steps_lan())
    common.write("frontdesk/web", "http.json", {
        "python": [common.version()],
        "fixture": [FIXTURE],
        "mints": [MINTS],
        "hostname": [HOSTNAME],
        "hosts": hosts,
        "names": names,
        "plain": plain,
        "local": local,
        "lan": lan,
    })


if __name__ == "__main__":
    main()

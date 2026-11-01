"""Front Desk load: page load, state (seats + tally), mint+revoke, with
1 and 20 concurrent viewers. Keys are checked by regex, never printed.
usage: load.py port [secs]"""
import http.client, json, re, statistics as st, sys, threading, time
port = int(sys.argv[1]); SECS = float(sys.argv[2]) if len(sys.argv) > 2 else 10
H = "127.0.0.1"
def pct(xs):
    xs = sorted(xs); n = len(xs)
    if not n: return "n=0"
    return (f"p50 {st.median(xs):7.2f} p95 {xs[min(n-1,int(.95*n))]:7.2f} "
            f"max {xs[-1]:7.2f} ms n={n}")
def conn(): return http.client.HTTPConnection(H, port, timeout=30)
def get(c, path, hdr=None):
    c.request("GET", path, headers=hdr or {}); r = c.getresponse()
    b = r.read(); return r, b
GZ = {"Accept-Encoding": "gzip, deflate, br"}
c = conn(); _, s = get(c, "/api/session"); CSRF = json.loads(s)["csrf"]
def page_load():
    """Fresh connection, like a first visit: page, css, js, icon, session,
    state. Browsers open ~6 connections, but this is the serial worst."""
    t = time.perf_counter(); c = conn()
    for p in ("/", "/app.css", "/app.js", "/icon.svg", "/api/session",
              "/api/state"):
        r, b = get(c, p, GZ); assert r.status == 200, (p, r.status)
    c.close(); return (time.perf_counter() - t) * 1e3
def run(viewers, fn, secs=SECS):
    out = []; lock = threading.Lock(); stop = time.perf_counter() + secs
    def w():
        local = []; ctx = {}
        while time.perf_counter() < stop: local.append(fn(ctx))
        with lock: out.extend(local)
    ts = [threading.Thread(target=w) for _ in range(viewers)]
    [t.start() for t in ts]; [t.join() for t in ts]
    return out
def state_req(ctx):
    c = ctx.get("c") or ctx.setdefault("c", conn())
    t = time.perf_counter(); r, b = get(c, "/api/state", GZ)
    assert r.status == 200; return (time.perf_counter() - t) * 1e3
def state_304(ctx):
    c = ctx.get("c") or ctx.setdefault("c", conn())
    hdr = dict(GZ); 
    if "e" in ctx: hdr["If-None-Match"] = ctx["e"]
    t = time.perf_counter(); r, b = get(c, "/api/state", hdr)
    ctx["e"] = r.getheader("ETag"); return (time.perf_counter() - t) * 1e3
KEY = re.compile(r"^sk-[A-Za-z0-9_-]{8,200}$")
def post(path, body):
    c = conn(); t = time.perf_counter()
    c.request("POST", path, json.dumps(body), {
        "Content-Type": "application/json", "X-CSRF-Token": CSRF,
        "Origin": f"http://{H}:{port}", "Host": f"{H}:{port}"})
    r = c.getresponse(); b = r.read(); c.close()
    return r.status, json.loads(b), (time.perf_counter() - t) * 1e3
mint_n = [0]
def mint_cycle(n, tag):
    m, v = [], []
    for i in range(n):
        seat = f"perf-{tag}-{i}"
        code, j, dt = post("/api/mint", {"seat": seat, "models": ["local"],
                                         "budget": "1", "duration": "1h"})
        assert code == 200 and KEY.match(j.get("key", "")), (code, j.get("error"))
        j.pop("key", None); m.append(dt)
        code, j, dt = post("/api/revoke", {"seat": seat})
        assert code == 200, (code, j); v.append(dt)
    return m, v
if __name__ == "__main__":
    what = sys.argv[3] if len(sys.argv) > 3 else "all"
    for viewers in (1, 20):
        if what in ("all", "page"):
            print(f"[{viewers:2d} viewers] page load (6 GETs) ", pct(run(viewers, lambda c: page_load())))
        if what in ("all", "state"):
            print(f"[{viewers:2d} viewers] /api/state 200 gz ", pct(run(viewers, state_req)))
            print(f"[{viewers:2d} viewers] /api/state 304    ", pct(run(viewers, state_304)))
    if what in ("all", "mint"):
        m, v = mint_cycle(15, "a")
        print("[ 0 bg] mint  ", pct(m)); print("[ 0 bg] revoke", pct(v))
        res = {}
        bg = threading.Thread(target=lambda: res.setdefault("bg", run(20, state_req, 60)))
        bg.start(); time.sleep(1)
        m, v = mint_cycle(15, "b")
        print("[20 bg] mint  ", pct(m)); print("[20 bg] revoke", pct(v))

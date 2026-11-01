"""Refill field: server-side validation and what reaches mint-key.sh.
usage: refill_vb.py port pwfile. Prints no secrets (keys masked)."""
import json, sys, time
sys.path.insert(0, sys.argv[0].rsplit("/", 1)[0])
import lib
lib.PORT = int(sys.argv[1])
PW = open(sys.argv[2]).read().strip()
SRC = "127.0.0.20"
ck, csrf = lib.session(SRC, PW)
H = {"Cookie": ck, "X-CSRF-Token": csrf,
     "Origin": f"http://192.0.2.2:{lib.PORT}"}


def post(path, body):
    st, j, hd, _ = lib.req(SRC, "POST", path, body, H)
    return st, j


def state():
    st, j, hd, _ = lib.req(SRC, "GET", "/api/state", None,
                           {"Cookie": ck})
    return j


s = state()
print("models:", s["models"], "defaults:", s["defaults"])
model = s["models"][0]
base = {"models": [model], "budget": "1", "duration": "30d"}
NOT = object()
bad = ["2d", "1m", "1h", "30m", "1D", " 1d", "1d ", "1mo ", "1d\n",
       "1d;id", "--models", "1d --models all", "١d", "1 d",
       5, 1, 0, None, True, False, ["1d"], {"v": "1d"}, 1.0]
n = 0
for v in bad:
    n += 1
    st, j = post("/api/mint", dict(base, seat=f"vb-bad-{n}", refill=v))
    print(f"  refill={json.dumps(v)!s:22} -> {st} field="
          f"{(j or {}).get('field')} {(j or {}).get('error', '')[:40]!r}"
          if st != 200 else f"  refill={v!r} -> 200 (MINTED!)")
cards = [c["seat"] for c in state()["cards"]]
print("  any vb-bad-* card made?", any(c.startswith("vb-bad-")
                                       for c in cards))
good = [("vb-r-none", NOT), ("vb-r-empty", ""), ("vb-r-1d", "1d"),
        ("vb-r-1w", "1w"), ("vb-r-1mo", "1mo")]
for seat, v in good:
    body = dict(base, seat=seat)
    if v is not NOT:
        body["refill"] = v
    st, j = post("/api/mint", body)
    print(f"  {seat}: {st} refill echo={j.get('refill')!r} "
          f"key={lib.mask(j.get('key'))}")
# odd but valid-looking durations the widened DURATION_RE now allows
for seat, dur in (("vb-d-1s", "1s"), ("vb-d-5m", "5m"),
                  ("vb-d-1mo", "1mo"), ("vb-d-9999mo", "9999mo")):
    st, j = post("/api/mint", dict(base, seat=seat, duration=dur))
    print(f"  duration {dur}: {st} {(j or {}).get('error', '')[:70]!r}")
time.sleep(1.5)
for c in state()["cards"]:
    if c["seat"].startswith("vb-"):
        print(f"  card {c['seat']:12} state={c['state']:8} refills="
              f"{c['refills']!r:6} resets={c['resets']} expires="
              f"{c['expires']}")

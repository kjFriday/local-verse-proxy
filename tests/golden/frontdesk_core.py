"""Goldens for internal/frontdesk/desk: seat_state, dollars, utc_text,
merge_tally, http_tally and build() (200 seats with refills, blocked
and expired cards, game seats, blank and hostile names; a fixed
secret, so the same card ids), the SQL text, and Settings over a few
seats.env files. Every key and secret here is made up.
python3 -B tests/golden/frontdesk_core.py"""
import json
import os
import random
import tempfile
from datetime import datetime, timezone

import common

NOW = datetime(2026, 10, 31, 12, 0, 0, tzinfo=timezone.utc)
SECRET = bytes(range(16))


class FixedDatetime(datetime):
    @classmethod
    def now(cls, tz=None):
        return NOW if tz else NOW.replace(tzinfo=None)


def compact(v):
    return json.dumps(v, separators=(",", ":"))


def outcome(fn, *a):
    try:
        return {"ok": fn(*a)}
    except Exception as e:  # what Python did instead: Go ledgers it
        return {"error": type(e).__name__}


TIMES = [
    None, "", "2026-10-31T12:00:00", "2026-10-31T12:00:00Z",
    "2026-10-31T11:59:59.999999+00:00", "2026-10-31T12:00:00.000001",
    "2026-11-01T00:00:00+05:30", "2026-10-30 23:00:00", "2026-10-31",
    "2026-10-31T12:00", "2026-10-31T13:00:00+01:00",
    "2026-10-31T12:00:00.5-02:00", "20261031T120000", "garbage",
    "2026-02-30T00:00:00", "2026-10-31T12:00:00+24:00",
]

NAMES = ["sam", "Sam2", "alice", "bob.laptop", "<script>x</script>",
         "", "\u00dcn\u00efcode", "\u0130stanbul", "\u03a3\u0391\u03a3",
         "q" * 40, "emoji\U0001f600", "tab\there", 'q"uote',
         "back\\slash", "game-fern", "game-moss", "zed", "Zed-2",
         "\u00e9t\u00e9", "line\nbreak", "\x7fdel", "ok_seat"]


def make_keys(n, seed):
    r = random.Random(seed)
    keys = []
    for i in range(n):
        name = r.choice(NAMES) + ("" if i < len(NAMES) else "-%d" % i)
        budget = r.choice([None, 5, 2.5, 0, 0.01, 100.0, 7])
        spend = r.choice([0.0, 1e-18, -1e-18, 4.999999, 5, 5.0000004,
                          2.5, 0.123456789, 0, None, 3, 1e-07])
        k = {
            "token": "tok-%04d" % i,
            "key_alias": name if r.random() > 0.05 else None,
            "key_name": r.choice(["sk-...ab%02d" % (i % 100), None, "",
                                  "xy"]),
            "models": r.choice([["local"], [], None, ["big", "local"]]),
            "max_budget": budget,
            "spend": spend,
            "budget_duration": r.choice([None, "1d", "1w", "", "1mo"]),
            "budget_reset_at": r.choice(TIMES[:13]),
            "expires": r.choice([None, "", "2026-10-31T12:00:00",
                                 "2026-10-31T11:00:00+00:00",
                                 "2027-01-01T00:00:00Z",
                                 "2026-10-31T12:00:00.000001"]),
            "created_at": "2026-10-%02dT08:00:00Z" % (1 + i % 28),
            "last_active": r.choice([None, "2026-10-31T10:00:00Z"]),
            "blocked": r.choice([None, False, True, False, None]),
        }
        if r.random() < 0.1:
            del k["spend"]
        keys.append(k)
    return keys


TALLY_ROWS = [
    {"k": "seat", "name": "sam", "spend": 1.5, "tokens": 10, "reqs": 2,
     "fails": 0},
    {"k": "seat", "name": "", "spend": 0, "tokens": 0, "reqs": 1,
     "fails": 1},
    {"k": "seat", "name": "gone-seat", "spend": 2, "tokens": 5,
     "reqs": None, "fails": None},
    {"k": "seat", "name": "<b>x</b>", "spend": -1e-18, "tokens": 1,
     "reqs": 1, "fails": 0},
    {"k": "model", "name": "local", "spend": 3.5000004, "tokens": 15,
     "reqs": 4, "fails": 1},
    {"k": "model", "name": "", "spend": 0.0, "tokens": 0, "reqs": 0,
     "fails": 0},
]


def merges():
    hist = {"day": "2026-10-31", "rows": TALLY_ROWS}
    today = {"day": "2026-10-31", "rows": [
        {"k": "seat", "name": "sam", "spend": 0.25, "tokens": 3,
         "reqs": 1, "fails": 0},
        {"k": "seat", "name": "new", "spend": 1, "tokens": 2,
         "reqs": None, "fails": 0},
        {"k": "model", "name": "local", "spend": 0.25, "tokens": 3,
         "reqs": 1, "fails": 0}]}
    empty = {"day": "2026-10-31", "rows": []}
    return [(hist, today), (empty, today), (hist, empty),
            (empty, empty), (today, hist)]


ACTIVITY = [
    {"results": [{"breakdown": {"model_groups": {
        "local": {"metrics": {"spend": 0.5, "total_tokens": 30,
                              "api_requests": 3, "failed_requests": 1},
                  "api_key_breakdown": {
                      "h1": {"metadata": {"key_alias": "sam"},
                             "metrics": {"spend": 0.25,
                                         "total_tokens": 10,
                                         "api_requests": 1,
                                         "failed_requests": 0}},
                      "h2": {"metadata": {},
                             "metrics": {"spend": 0.25,
                                         "total_tokens": 20,
                                         "api_requests": 2,
                                         "failed_requests": 1}}}},
        "big": {"metrics": {}, "api_key_breakdown": {}}}}},
        {"breakdown": None}]},
    {"results": []},
    {},
    "proxy-error",
]


def settings_cases(fd, tmp):
    texts = [
        "LITELLM_MASTER_KEY=sk-fake-master-0001\n"
        "LITELLM_SALT_KEY=sk-fake-salt-000001\n"
        "DATABASE_URL=postgresql://litellm:fakepw@db:5432/litellm\n"
        "PROXY_URL=http://localhost:4000\n",
        "export LITELLM_MASTER_KEY='sk-fake-quoted'\n"
        "LITELLM_SALT_KEY=\"change-me-fake\"\n"
        "PROXY_URL=https://Proxy.Example.test/\n"
        "DEFAULT_MODELS=\" local , big ,,\"\n"
        "DEFAULT_BUDGET=2.50\nDEFAULT_DURATION=7d\n"
        "SOME_TOKEN=short\nAPI_SECRET=fake-secret-value\n"
        "MY_PASSWORD=fake-password-1\nOTHER=fake-not-hidden\n",
        "LITELLM_MASTER_KEY=change-me\n"
        "PROXY_URL=http://10.0.0.5:4100\n"
        "# a comment\n\nDEFAULT_MODELS=one\n",
        "LITELLM_MASTER_KEY=sk-fake-ipv6-key\n"
        "PROXY_URL=http://[::1]:4000\n",
        "LITELLM_MASTER_KEY=sk-fake-no-port\n"
        "PROXY_URL=https://proxy.example.test\n",
    ]
    out = []
    for i, text in enumerate(texts):
        p = os.path.join(tmp, "seats%d.env" % i)
        with open(p, "w") as f:
            f.write(text)
        s = fd.Settings(p)
        out.append({"text": text, "master": s.master, "db_url": s.db_url,
                    "https": s.https, "host": s.host, "port": s.port,
                    "defaults": s.defaults, "hidden": s.hidden,
                    "warnings": [w.replace("mint-key.sh", "mint-key")
                                 for w in s.warnings],
                    "scrub": s.scrub("x " + " ".join(s.hidden) + " y")})
    return out


def main():
    fd = common.load("frontdesk", "gui/frontdesk.py")
    fd.datetime = FixedDatetime
    with tempfile.TemporaryDirectory() as tmp:
        settings = settings_cases(fd, tmp)
        p = os.path.join(tmp, "seats.env")
        with open(p, "w") as f:
            f.write("LITELLM_MASTER_KEY=sk-fake-build-master\n"
                    "DEFAULT_MODELS=local,big\n")
        s = fd.Settings(p)

        states = []
        for k in make_keys(300, "states"):
            states.append({"key": k, "state": outcome(fd.seat_state, k,
                                                      NOW)})
        for t in TIMES:
            for b in (1, 2):
                k = {"expires": t, "max_budget": b, "spend": 1}
                gone = {"max_budget": b, "spend": 1}
                states.append({"key": k,
                               "state": outcome(fd.seat_state, k, NOW),
                               "if_absent": fd.seat_state(gone, NOW)})

        dollars = [{"in": x, "out": fd.dollars(x)} for x in [
            None, 0, 0.0, 1e-18, -1e-18, 1.23456789, 2, 2.0000005,
            2.0000015, 0.0000005, 0.0000015, -3, 123456.1234565, 1e-07,
            5.0000004, 0.1 + 0.2]]
        utc = [{"in": t, "out": fd.utc_text(t)} for t in TIMES + [
            5, None, "2026-10-31T12:00:00.123+00:00",
            "2026-10-31T12:00:00-00:30", "2026-10-31T12:00:00+00:00:30"]]

        merged = [{"hist": h, "today": t, "out": fd.merge_tally(h, t)}
                  for h, t in merges()]

        desk = fd.Desk(s, None, False)
        desk.secret = SECRET
        http = []
        keys = make_keys(12, "http")
        for act in ACTIVITY:
            def fake_json(path, act=act):
                if act == "proxy-error":
                    raise fd.ProxyError("no")
                return json.loads(json.dumps(act))
            desk.proxy.json = fake_json
            desk.raw_keys = json.loads(json.dumps(keys))
            http.append({"activity": act, "keys": keys,
                         "out": compact(desk.http_tally("why it fell"))})

        builds = []
        for seed, n, lan in (("build-a", 200, False), ("build-b", 3, True),
                             ("build-c", 0, False)):
            d = fd.Desk(s, None, lan)
            d.secret = SECRET
            d.raw_keys = make_keys(n, seed)
            d.models = ["big", "local"]
            d.litellm = "1.106.6"
            d.db = "connected"
            d.game = frozenset({"game-fern", "game-moss-3"})
            if seed == "build-a":
                d.tally = dict(fd.merge_tally(*merges()[0]),
                               source="database")
            elif seed == "build-b":
                d.tally = {"day": "2026-10-31", "rows": [],
                           "source": "proxy", "why": "psql stopped"}
            notes = ["Details: nothing answers at localhost:4000 "
                     "(ConnectionRefusedError).",
                     "leak sk-fake-build-master here"]
            state = d.build(seed != "build-c", notes)
            builds.append({"seed": seed, "n": n, "lan": lan,
                           "keys": d.raw_keys,
                           "tally": json.loads(json.dumps(
                               d.tally)) if d.tally else None,
                           "game": sorted(d.game), "notes": notes,
                           "up": seed != "build-c",
                           "body": compact(state),
                           "tokens": d.tokens})

    common.write("frontdesk/desk", "core.json", {
        "python": [common.version()],
        "now": [NOW.isoformat()],
        "secret": [list(SECRET)],
        "sql": [fd.TODAY_SQL, fd.HISTORY_SQL],
        "settings": settings,
        "states": states,
        "dollars": dollars,
        "utc": utc,
        "merge": merged,
        "http_tally": http,
        "builds": builds,
    })


if __name__ == "__main__":
    main()

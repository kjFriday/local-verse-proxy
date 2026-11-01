"""Server-side input checks on /api/mint and /api/revoke (local mode).
Keys are never printed. usage: inputs.py port"""
import http.client, json, sys
port = int(sys.argv[1])
def req(method, path, body=None, headers=None, raw=None):
    c = http.client.HTTPConnection("127.0.0.1", port, timeout=30)
    h = {"Host": f"127.0.0.1:{port}"}
    h.update(headers or {})
    data = raw if raw is not None else (json.dumps(body) if body is not None else None)
    c.request(method, path, data, h)
    r = c.getresponse(); b = r.read(); c.close()
    return r.status, b
_, s = req("GET", "/api/session"); csrf = json.loads(s)["csrf"]
H = {"Content-Type": "application/json", "X-CSRF-Token": csrf}
ok = {"seat": "probe", "models": ["local"], "budget": "1", "duration": "1d"}
cases = [
 ("seat 41 chars", {"seat": "a" * 41}), ("seat leading dash", {"seat": "-x"}),
 ("seat space", {"seat": "a b"}), ("seat unicode", {"seat": "sé"}),
 ("seat newline", {"seat": "a\nb"}), ("seat list", {"seat": ["a"]}),
 ("seat null", {"seat": None}),
 ("models empty", {"models": []}), ("models unknown", {"models": ["gpt-x"]}),
 ("models str", {"models": "local"}), ("models comma", {"models": ["local,claude"]}),
 ("budget 0", {"budget": "0"}), ("budget -1", {"budget": "-1"}),
 ("budget 1e3", {"budget": "1e3"}), ("budget 1.234", {"budget": "1.234"}),
 ("budget 1234567", {"budget": "1234567"}), ("budget num 2.5", {"budget": 2.5}),
 ("budget nan", {"budget": "nan"}), ("budget ' 5 '", {"budget": " 5 "}),
 ("duration 0d", {"duration": "0d"}), ("duration 30m", {"duration": "30m"}),
 ("duration 1y", {"duration": "1y"}), ("duration 99999d", {"duration": "99999d"}),
 ("duration --budget", {"duration": "--budget"}),
]
for name, patch in cases:
    st, b = req("POST", "/api/mint", dict(ok, **patch), H)
    j = json.loads(b)
    leaked = "key" in j
    print(f"mint {name:20} -> {st} field={j.get('field')} {'KEY RETURNED' if leaked else ''} {j.get('error','')[:60]}")
    if leaked:
        req("POST", "/api/revoke", {"seat": j["seat"]}, H)
# bad transport
for name, hdr, raw in [
  ("no csrf", {"Content-Type": "application/json"}, json.dumps(ok)),
  ("wrong csrf", dict(H, **{"X-CSRF-Token": "x" * 32}), json.dumps(ok)),
  ("text/plain", dict(H, **{"Content-Type": "text/plain"}), json.dumps(ok)),
  ("origin evil", dict(H, Origin="http://evil.test"), json.dumps(ok)),
  ("origin null", dict(H, Origin="null"), json.dumps(ok)),
  ("big body", H, json.dumps(dict(ok, pad="x" * 9000))),
  ("bad json", H, "{nope"), ("json list", H, "[1,2]"),
  ("bad length", dict(H, **{"Content-Length": "abc"}), ""),
  ("neg length", dict(H, **{"Content-Length": "-5"}), ""),
]:
    st, b = req("POST", "/api/mint", None, hdr, raw)
    print(f"post {name:12} -> {st} {b[:70]!r}")
# revoke by unknown, by injected seat
for body in ({"seat": "nobody"}, {"id": "0" * 16}, {"seat": ["seat000"]},
             {"seat": "--revoke"}, {}):
    st, b = req("POST", "/api/revoke", body, H)
    print(f"revoke {json.dumps(body):24} -> {st} {b[:70]!r}")

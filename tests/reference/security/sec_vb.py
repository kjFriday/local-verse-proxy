"""Regression checks: Host, Origin, CSRF, cookies, body limits,
error scrubbing. usage: sec_vb.py port pwfile. No secrets printed."""
import json, socket, sys, time
sys.path.insert(0, sys.argv[0].rsplit("/", 1)[0])
import lib
lib.PORT = P = int(sys.argv[1])
PW = open(sys.argv[2]).read().strip()
SRC = "127.0.0.30"
ME = socket.gethostname().lower()


def raw(src, data, timeout=12):
    s = socket.create_connection(("127.0.0.1", P),
                                 source_address=(src, 0), timeout=timeout)
    s.sendall(data)
    out = b""
    try:
        while True:
            b = s.recv(65536)
            if not b:
                break
            out += b
    except socket.timeout:
        out += b"<timeout>"
    s.close()
    return out.split(b"\r\n", 1)[0].decode(), out


print("== Host check (GET /)")
for host in ["192.0.2.2:%d" % P, "127.0.0.1:%d" % P, "localhost:%d" % P,
             f"{ME}:{P}", f"{ME}.local:{P}", "[::1]:%d" % P,
             "evil.example:%d" % P, "evil.example", "192.0.2.2:9999",
             "127.0.0.1.nip.io:%d" % P, "", "192.0.2.2"]:
    line, _ = raw(SRC, (f"GET / HTTP/1.1\r\nHost: {host}\r\n"
                        "Connection: close\r\n\r\n").encode())
    print(f"  Host {host!r:28} -> {line}")
line, _ = raw(SRC, b"GET / HTTP/1.1\r\nConnection: close\r\n\r\n")
print(f"  (no Host header)             -> {line}")
line, out = raw(SRC, (f"POST /api/login HTTP/1.1\r\nHost: evil.example:{P}"
                      "\r\nContent-Type: application/json\r\n"
                      "Content-Length: 2\r\n\r\n{}").encode())
print("  POST /api/login Host evil     ->", line)

print("== login cookie")
st, j, hd = lib.login(SRC, PW)
print("  Set-Cookie flags:", ";".join(
    p.strip() if not p.strip().startswith("fd_") else
    p.split("=")[0].strip() + "=<value>"
    for p in hd["Set-Cookie"].split(";")))
ck, csrf = hd["Set-Cookie"].split(";")[0], j["csrf"]
O = {"Origin": f"http://192.0.2.2:{P}"}
mint_body = {"seat": "vb-csrf", "models": ["local"], "budget": "1",
             "duration": "1d"}

print("== CSRF and Origin on every POST")
for path, body in (("/api/mint", mint_body),
                   ("/api/revoke", {"seat": "vb-r-none"}),
                   ("/api/logout", {})):
    cases = {
        "no cookie": {**O, "X-CSRF-Token": csrf},
        "cookie, no token": {**O, "Cookie": ck},
        "cookie, wrong token": {**O, "Cookie": ck, "X-CSRF-Token": "x" * 32},
        "cookie+token, Origin evil": {"Origin": "http://evil.example",
                                      "Cookie": ck, "X-CSRF-Token": csrf},
        "cookie+token, Origin null": {"Origin": "null", "Cookie": ck,
                                      "X-CSRF-Token": csrf},
        "cookie+token, other port": {"Origin": "http://192.0.2.2:4133",
                                     "Cookie": ck, "X-CSRF-Token": csrf},
    }
    for name, h in cases.items():
        st, jj, _, _ = lib.req(SRC, "POST", path, body, h)
        print(f"  {path:12} {name:28} -> {st} {(jj or {}).get('error')!r}")
    # right cookie and token, but from another address
    st, jj, _, _ = lib.req("127.0.0.31", "POST", path, body,
                           {**O, "Cookie": ck, "X-CSRF-Token": csrf})
    print(f"  {path:12} {'cookie+token, other device':28} -> {st} "
          f"{(jj or {}).get('error')!r}")
    for ctype in ("text/plain", "application/x-www-form-urlencoded",
                  "multipart/form-data; boundary=x"):
        st, jj, _, _ = lib.req(SRC, "POST", path, None,
                               {**O, "Cookie": ck, "X-CSRF-Token": csrf,
                                "Content-Type": ctype},
                               raw=json.dumps(body))
        print(f"  {path:12} {'Content-Type ' + ctype[:18]:28} -> {st} "
              f"{(jj or {}).get('error')!r}")
st, jj, _, _ = lib.req(SRC, "GET", "/api/state", None, {})
print("  GET /api/state no cookie ->", st)
st, jj, _, _ = lib.req("127.0.0.31", "GET", "/api/state", None,
                       {"Cookie": ck})
print("  GET /api/state cookie from other device ->", st)

print("== body size and shape")
G = {**O, "Cookie": ck, "X-CSRF-Token": csrf,
     "Content-Type": "application/json"}
for name, hdrs, body in (
        ("8192 bytes", {}, b'{"seat":"' + b"a" * 8182 + b'"}'),
        ("8193 bytes", {}, b'{"seat":"' + b"a" * 8183 + b'"}'),
        ("CL -1", {"Content-Length": "-1"}, b"{}"),
        ("CL abc", {"Content-Length": "abc"}, b"{}"),
        ("CL 10**9", {"Content-Length": str(10**9)}, b"{}"),
        ("JSON array", {}, b"[1,2]"),
        ("bad JSON", {}, b"{nope"),
        ("deep nest", {}, b"[" * 4000 + b"]" * 4000)):
    hh = dict(G, **hdrs)
    hl = "".join(f"{k}: {v}\r\n" for k, v in hh.items())
    if "Content-Length" not in hdrs:
        hl += f"Content-Length: {len(body)}\r\n"
    t = time.time()
    line, out = raw(SRC, (f"POST /api/mint HTTP/1.1\r\nHost: 192.0.2.2:{P}"
                          f"\r\n{hl}\r\n").encode() + body)
    err = out.split(b"\r\n\r\n", 1)[-1][:60]
    print(f"  {name:12} -> {line} {err!r} ({time.time() - t:.2f} s)")

print("== error scrubbing (wrapper fakes a leaky mint-key.sh failure)")
st, jj, _, _ = lib.req(SRC, "POST", "/api/mint",
                       dict(mint_body, seat="scrubtest"),
                       {**O, "Cookie": ck, "X-CSRF-Token": csrf})
e = (jj or {}).get("error", "")
print("  status", st, "| error:", e.replace("[hidden]", "[H]")[:160])
print("  has sk-?", "sk-" in e, "| has 32+ hex?",
      any(len(w) >= 32 and all(c in "0123456789abcdef" for c in w)
          for w in e.split()))

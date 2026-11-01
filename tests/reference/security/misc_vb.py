"""Budget comma, headers, SSE slots after resets.
usage: misc_vb.py port pwfile. Prints no secrets."""
import socket, struct, sys, time
sys.path.insert(0, sys.argv[0].rsplit("/", 1)[0])
import lib
lib.PORT = P = int(sys.argv[1])
PW = open(sys.argv[2]).read().strip()
SRC = "127.0.15.1"
ck, csrf = lib.session(SRC, PW)
H = {"Cookie": ck, "X-CSRF-Token": csrf, "Origin": f"http://192.0.2.2:{P}"}
base = {"models": ["local"], "duration": "1d"}
n = 0
for b in ("2,50", "1,000", "2.505", "1e3", "Infinity", "NaN", "-1", "0",
          "0.00", "٢", " 2", "1000000", 2.5, True, None, "2.50"):
    n += 1
    st, j, _, _ = lib.req(SRC, "POST", "/api/mint",
                          dict(base, seat=f"vb-b{n}-{P}", budget=b), H)
    print(f"  budget {b!r:12} -> {st} {(j or {}).get('field')} "
          f"{'key ' + lib.mask(j.get('key')) if st == 200 else ''}")
st, j, hd, _ = lib.req(SRC, "GET", "/", None, {})
print("  GET / headers:", {k: v[:40] for k, v in hd.items()
                          if k in ("Content-Security-Policy",
                                   "X-Content-Type-Options",
                                   "X-Frame-Options", "Referrer-Policy",
                                   "Cache-Control")})
st, j, hd, _ = lib.req(SRC, "GET", "/api/state", None, {"Cookie": ck})
print("  /api/state:", st, hd.get("Cache-Control"),
      hd.get("X-Frame-Options"))
# SSE: open many streams, reset them, then can we still open one?
L0 = struct.pack("ii", 1, 0)
def stream(src):
    s = socket.create_connection(("127.0.0.1", P), source_address=(src, 0),
                                 timeout=5)
    s.sendall((f"GET /api/events HTTP/1.1\r\nHost: 192.0.2.2:{P}\r\n"
               f"Cookie: {ck}\r\n\r\n").encode())
    first = s.recv(200).split(b"\r\n", 1)[0].decode()
    return s, first
socks, codes = [], {}
for i in range(30):
    s, first = stream(SRC)
    codes[first] = codes.get(first, 0) + 1
    socks.append(s)
print("  30 streams:", codes)
for s in socks:
    s.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, L0)
    s.close()
time.sleep(2.5)
codes = {}
socks = []
for i in range(30):
    s, first = stream(SRC)
    codes[first] = codes.get(first, 0) + 1
    socks.append(s)
print("  after resets, 30 streams again:", codes)
for s in socks:
    s.close()

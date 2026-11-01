"""Reset variants beyond GET /: does any still write to the log?
usage: rst2_vb.py port errfile pid. Prints no secrets."""
import os, socket, struct, sys, time
P, ERR, PID = int(sys.argv[1]), sys.argv[2], int(sys.argv[3])
H = f"192.0.2.2:{P}"
LINGER0 = struct.pack("ii", 1, 0)
SRC = "127.0.14.1"


def lines():
    return sum(1 for _ in open(ERR, errors="replace"))


def threads():
    return len(os.listdir(f"/proc/{PID}/task"))


def rst(data, wait=0.0, src=SRC, read_first=False):
    s = socket.create_connection(("127.0.0.1", P),
                                 source_address=(src, 0), timeout=5)
    s.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, LINGER0)
    if data:
        s.sendall(data)
    if read_first:
        try:
            s.settimeout(2)
            s.recv(65536)
        except OSError:
            pass
    if wait:
        time.sleep(wait)
    s.close()


def post(path, body, extra=""):
    body = body.encode() if isinstance(body, str) else body
    return (f"POST {path} HTTP/1.1\r\nHost: {H}\r\nContent-Type: "
            f"application/json\r\nContent-Length: {len(body)}\r\n"
            f"{extra}\r\n").encode() + body


big_uri = b"GET /" + b"a" * 70000 + b" HTTP/1.1\r\nHost: " + \
    H.encode() + b"\r\n\r\n"
cases = [
    ("POST /api/login {} (400)", post("/api/login", "{}")),
    ("POST /api/mint unauth (401)", post("/api/mint", "{}")),
    ("POST bad JSON (400)", post("/api/login", "{nope")),
    ("POST nested 4000", post("/api/login", "[" * 4000 + "]" * 4000)),
    ("bad request line (400)", b"GARBAGE\r\n\r\n"),
    ("bad version (400)", b"GET / HTTP/9.x\r\n\r\n"),
    ("HTTP/2.0 (505)", b"GET / HTTP/2.0\r\nHost: " + H.encode() +
     b"\r\n\r\n"),
    ("URI 70k (414)", big_uri),
    ("200 headers (431)", b"GET / HTTP/1.1\r\nHost: " + H.encode() +
     b"\r\n" + b"".join(b"X%d: y\r\n" % i for i in range(200)) +
     b"\r\n"),
    ("unknown method (501)", b"BREW / HTTP/1.1\r\nHost: " + H.encode() +
     b"\r\n\r\n"),
    ("wrong Host (421)", b"GET / HTTP/1.1\r\nHost: evil.example\r\n\r\n"),
    ("HEAD /api/state (405)", b"HEAD /api/state HTTP/1.1\r\nHost: " +
     H.encode() + b"\r\n\r\n"),
    ("GET /nope (404)", b"GET /nope HTTP/1.1\r\nHost: " + H.encode() +
     b"\r\n\r\n"),
    ("GET /app.js gzip", b"GET /app.js HTTP/1.1\r\nHost: " + H.encode() +
     b"\r\nAccept-Encoding: gzip\r\n\r\n"),
    ("GET /app.js plain", b"GET /app.js HTTP/1.1\r\nHost: " + H.encode() +
     b"\r\n\r\n"),
    ("Expect 100-continue, no body",
     (f"POST /api/login HTTP/1.1\r\nHost: {H}\r\nContent-Type: "
      "application/json\r\nContent-Length: 20\r\nExpect: 100-continue"
      "\r\n\r\n").encode()),
    ("POST body cut short", post("/api/login", "{}")[:-1] + b"",),
    ("pipelined x5", (b"GET / HTTP/1.1\r\nHost: " + H.encode() +
                      b"\r\n\r\n") * 5),
]
total = 0
for name, data in cases:
    n0 = lines()
    for _ in range(10):
        rst(data)
    time.sleep(0.3)
    d = lines() - n0
    total += d
    print(f"  {name:32} x10 reset at once: {d} new log lines")
# keep-alive: answer read, then reset while the server waits
n0 = lines()
for _ in range(10):
    rst(b"GET / HTTP/1.1\r\nHost: " + H.encode() + b"\r\n\r\n",
        read_first=True, wait=0.2)
time.sleep(0.3)
print(f"  {'keep-alive idle, then reset':32} x10: {lines() - n0} lines")
# half a request line, then reset (readline raises)
n0 = lines()
for _ in range(10):
    rst(b"GET / HT", wait=0.1)
time.sleep(0.3)
print(f"  {'half request line, then reset':32} x10: {lines() - n0} lines")
# request headers half sent, then reset
n0 = lines()
for _ in range(10):
    rst(b"GET / HTTP/1.1\r\nHost: " + H.encode() + b"\r\nX-A: b", wait=0.1)
time.sleep(0.3)
print(f"  {'half headers, then reset':32} x10: {lines() - n0} lines")
# POST body half sent, then reset (read inside do_POST)
n0 = lines()
for _ in range(10):
    rst((f"POST /api/login HTTP/1.1\r\nHost: {H}\r\nContent-Type: "
         "application/json\r\nContent-Length: 200\r\n\r\n{\"pa").encode(),
        wait=0.1)
time.sleep(0.3)
print(f"  {'POST body half, then reset':32} x10: {lines() - n0} lines")

# slot and per-device counts: all given back after the resets?
time.sleep(0.5)
print(f"  threads now: {threads()}")
held = []
for i in range(40):
    x = socket.create_connection(("127.0.0.1", P),
                                 source_address=(SRC, 0))
    x.sendall(b"GET / HTTP/1.1\r\n")
    held.append(x)
time.sleep(0.5)
alive = 0
for x in held:
    x.settimeout(0.05)
    try:
        if x.recv(1) != b"":
            alive += 1
    except (socket.timeout, BlockingIOError):
        alive += 1
    except OSError:
        pass
print(f"  same address after resets: {alive} of 40 held (cap is 32)")
for x in held:
    x.close()

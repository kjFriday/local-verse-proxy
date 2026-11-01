"""Odd unauthenticated requests: which ones write to the log?
usage: fuzz_vb.py port errfile"""
import socket, sys, time
P, ERR = int(sys.argv[1]), sys.argv[2]
H = f"192.0.2.2:{P}"


def raw(data):
    s = socket.create_connection(("127.0.0.1", P),
                                 source_address=("127.0.11.1", 0),
                                 timeout=12)
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
    return out.split(b"\r\n", 1)[0].decode(errors="replace")


def post(path, body, extra=""):
    if isinstance(body, str):
        body = body.encode()
    return raw((f"POST {path} HTTP/1.1\r\nHost: {H}\r\nContent-Type: "
                f"application/json\r\nContent-Length: {len(body)}\r\n"
                f"{extra}Connection: close\r\n\r\n").encode() + body)


def get(path, extra=""):
    return raw((f"GET {path} HTTP/1.1\r\nHost: {H}\r\n{extra}"
                "Connection: close\r\n\r\n").encode())


cases = [
    ("login surrogate", lambda: post("/api/login",
                                     '{"password":"\\udc80"}')),
    ("login nested 990", lambda: post("/api/login",
                                      "[" * 990 + "]" * 990)),
    ("login nested obj", lambda: post("/api/login",
                                      '{"a":' * 1500 + "1" + "}" * 1500)),
    ("login 5000-digit int", lambda: post("/api/login",
                                          '{"password":' + "9" * 5000 + "}")),
    ("login bad utf-8", lambda: post("/api/login",
                                     b'{"password":"\xff\xfe"}')),
    ("login NaN", lambda: post("/api/login", '{"password":NaN}')),
    ("login dup keys", lambda: post("/api/login",
                                    '{"password":"a","password":"b"}')),
    ("mint surrogate seat", lambda: post("/api/mint",
                                         '{"seat":"\\ud800"}')),
    ("bad cookie", lambda: get("/api/state",
                               'Cookie: fd_%d="\\x00;;=\r\n' % P)),
    ("cookie latin1", lambda: raw((f"GET /api/state HTTP/1.1\r\nHost: {H}"
                                   "\r\nCookie: fd_x=\xe9\xff\r\n"
                                   "Connection: close\r\n\r\n")
                                  .encode("latin-1"))),
    ("host latin1", lambda: raw(("GET / HTTP/1.1\r\nHost: \xe9:%d\r\n"
                                 "Connection: close\r\n\r\n" % P)
                                .encode("latin-1"))),
    ("host [v1.x]", lambda: raw((f"GET / HTTP/1.1\r\nHost: [v1.x]:{P}\r\n"
                                 "Connection: close\r\n\r\n").encode())),
    ("host [::1", lambda: raw((f"GET / HTTP/1.1\r\nHost: [::1:{P}\r\n"
                               "Connection: close\r\n\r\n").encode())),
    ("host port 99999", lambda: raw(("GET / HTTP/1.1\r\nHost: 192.0.2.2:"
                                     "99999\r\nConnection: close\r\n\r\n")
                                    .encode())),
    ("CL short body", lambda: raw((f"POST /api/login HTTP/1.1\r\nHost: {H}"
                                   "\r\nContent-Type: application/json\r\n"
                                   "Content-Length: 50\r\n\r\n{}"))
     if False else "skipped (waits 10 s)"),
    ("CL unicode digit", lambda: raw(
        (f"POST /api/login HTTP/1.1\r\nHost: {H}\r\nContent-Type: "
         "application/json\r\nContent-Length: \u0662\r\n"
         "Connection: close\r\n\r\n{}").encode())),
    ("If-None-Match odd", lambda: get("/", "If-None-Match: \x7f\r\n")),
    ("path %00", lambda: get("/%00")),
    ("path long", lambda: get("/" + "a" * 60000)),
    ("HEAD api", lambda: raw((f"HEAD /api/state HTTP/1.1\r\nHost: {H}\r\n"
                              "Connection: close\r\n\r\n").encode())),
    ("events unauth", lambda: get("/api/events")),
    ("huge header", lambda: get("/", "X: " + "a" * 70000 + "\r\n")),
    ("200 headers", lambda: get("/", "".join(f"X{i}: a\r\n"
                                             for i in range(200)))),
]
for name, fn in cases:
    n0 = sum(1 for _ in open(ERR))
    try:
        line = fn()
    except OSError as e:
        line = "OSError " + e.__class__.__name__
    time.sleep(0.15)
    lines = open(ERR).read().splitlines()[n0:]
    print(f"  {name:22} -> {line[:40]:40} log: "
          f"{[l[9:90] for l in lines] or '-'}")

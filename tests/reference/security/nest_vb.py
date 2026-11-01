"""Unauthenticated deep-nested JSON bodies vs Front Desk's log.
usage: nest_vb.py port errfile seconds"""
import os, socket, sys, threading, time
P, ERR, SECS = int(sys.argv[1]), sys.argv[2], float(sys.argv[3])
BODY = b"[" * 4000 + b"]" * 4000
REQ = (f"POST /api/login HTTP/1.1\r\nHost: 192.0.2.2:{P}\r\n"
       "Content-Type: application/json\r\n"
       f"Content-Length: {len(BODY)}\r\n\r\n").encode() + BODY


def one(src):
    s = socket.create_connection(("127.0.0.1", P),
                                 source_address=(src, 0), timeout=5)
    s.sendall(REQ)
    out = b""
    while b"\r\n\r\n" not in out:
        b = s.recv(4096)
        if not b:
            break
        out += b
    s.close()
    return out.split(b"\r\n", 1)[0].decode()


print("one try:", one("127.0.9.1"))
time.sleep(0.2)
print("last log line:", open(ERR).read().splitlines()[-1][:120])
b0, n0 = os.path.getsize(ERR), sum(1 for _ in open(ERR))
stop, cnt = threading.Event(), [0]
def w():
    while not stop.is_set():
        one("127.0.9.1"); cnt[0] += 1
ts = [threading.Thread(target=w) for _ in range(8)]
t0 = time.time(); [t.start() for t in ts]
time.sleep(SECS); stop.set(); [t.join() for t in ts]
dt = time.time() - t0
time.sleep(0.3)
b1, n1 = os.path.getsize(ERR), sum(1 for _ in open(ERR))
print(f"one unauthenticated address, 8 conns, {dt:.1f} s: {cnt[0]} "
      f"requests, {n1 - n0} log lines ({(n1 - n0) / dt:.0f}/s), "
      f"{(b1 - b0) / dt / 1024:.0f} KiB/s of log")

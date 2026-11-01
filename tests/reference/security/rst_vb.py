"""Unauthenticated: send a request, then reset the connection at once.
Does Front Desk's terminal grow? usage: rst_vb.py port errfile secs"""
import os, socket, struct, sys, threading, time
P, ERR, SECS = int(sys.argv[1]), sys.argv[2], float(sys.argv[3])
SRC = sys.argv[4] if len(sys.argv) > 4 else "127.0.12.1"
PATH = sys.argv[5] if len(sys.argv) > 5 else "/"
REQ = (f"GET {PATH} HTTP/1.1\r\nHost: 192.0.2.2:{P}\r\n"
       "Accept-Encoding: gzip\r\n\r\n").encode()
LINGER0 = struct.pack("ii", 1, 0)


def one():
    s = socket.create_connection(("127.0.0.1", P),
                                 source_address=(SRC, 0), timeout=5)
    s.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, LINGER0)
    s.sendall(REQ)
    s.close()  # RST, not FIN


def lines():
    return sum(1 for _ in open(ERR, errors="replace"))


n0, b0 = lines(), os.path.getsize(ERR)
for _ in range(20):
    one()
time.sleep(0.5)
n1 = lines()
print(f"20 reset requests for {PATH}: {n1 - n0} new log lines")
tail = open(ERR, errors="replace").read().splitlines()[n0:n0 + 14]
print("  first new lines:")
for l in tail:
    print("   |", l[:110])
stop, cnt = threading.Event(), [0]
def w():
    while not stop.is_set():
        try:
            one(); cnt[0] += 1
        except OSError:
            pass
ts = [threading.Thread(target=w) for _ in range(4)]
n2, b2 = lines(), os.path.getsize(ERR)
t0 = time.time(); [t.start() for t in ts]
time.sleep(SECS); stop.set(); [t.join() for t in ts]
dt = time.time() - t0
time.sleep(0.5)
n3, b3 = lines(), os.path.getsize(ERR)
print(f"one address, 4 threads, {dt:.1f} s: {cnt[0]} requests, "
      f"{n3 - n2} log lines ({(n3 - n2) / dt:.0f}/s), "
      f"{(b3 - b2) / dt / 1024:.0f} KiB/s")

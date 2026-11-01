"""How fast one locked device grows Front Desk's log. usage: port errfile"""
import os, sys, threading, time
sys.path.insert(0, sys.argv[0].rsplit("/", 1)[0])
import lib
lib.PORT = int(sys.argv[1]); ERR = sys.argv[2]
SRC = "127.0.0.7"   # locked in lock_vb.py
b0, n0 = os.path.getsize(ERR), sum(1 for _ in open(ERR))
stop, cnt = threading.Event(), [0]
def w():
    while not stop.is_set():
        lib.login(SRC, "wrong-password-xyz"); cnt[0] += 1
ts = [threading.Thread(target=w) for _ in range(8)]
t0 = time.time(); [t.start() for t in ts]
time.sleep(5); stop.set(); [t.join() for t in ts]
dt = time.time() - t0
b1, n1 = os.path.getsize(ERR), sum(1 for _ in open(ERR))
print(f"one locked device, 8 conns, {dt:.1f} s: {cnt[0]} tries, "
      f"{n1 - n0} log lines ({(n1 - n0) / dt:.0f}/s), "
      f"{(b1 - b0) / dt / 1024:.0f} KiB/s of log")

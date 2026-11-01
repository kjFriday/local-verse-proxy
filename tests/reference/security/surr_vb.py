"""A password with a lone surrogate (JSON "\\ud800") from one device.
usage: surr_vb.py port pwfile errfile seconds"""
import os, sys, threading, time
sys.path.insert(0, sys.argv[0].rsplit("/", 1)[0])
import lib
lib.PORT = int(sys.argv[1])
PW = open(sys.argv[2]).read().strip()
ERR, SECS = sys.argv[3], float(sys.argv[4])
SRC = "127.0.10.1"
RAW = '{"password": "x\\ud800"}'


def surr():
    st, j, hd, _ = lib.req(SRC, "POST", "/api/login", None,
                           {"Content-Type": "application/json"}, raw=RAW)
    return st, (j or {}).get("error")


for i in range(7):
    print("  try", i + 1, surr()); time.sleep(1.05)
print("  last log line:", open(ERR).read().splitlines()[-1])
b0, n0 = os.path.getsize(ERR), sum(1 for _ in open(ERR))
stop, cnt = threading.Event(), [0]
def w():
    while not stop.is_set():
        surr(); cnt[0] += 1
ts = [threading.Thread(target=w) for _ in range(8)]
t0 = time.time(); [t.start() for t in ts]
time.sleep(SECS); stop.set(); [t.join() for t in ts]
dt = time.time() - t0
time.sleep(0.3)
b1, n1 = os.path.getsize(ERR), sum(1 for _ in open(ERR))
print(f"  {dt:.1f} s: {cnt[0]} tries, {n1 - n0} log lines "
      f"({(n1 - n0) / dt:.0f}/s), {(b1 - b0) / dt / 1024:.0f} KiB/s")
print("  same device, RIGHT password now:", lib.login(SRC, PW)[0])
print("  then a real wrong one:", lib.login(SRC, "nope")[1].get("error"))

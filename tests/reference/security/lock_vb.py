"""--lan login lock and slow-down. usage: lock_vb.py port pwfile"""
import sys, time, threading
sys.path.insert(0, sys.argv[0].rsplit("/", 1)[0])
import lib
lib.PORT = int(sys.argv[1])
PW = open(sys.argv[2]).read().strip()
WRONG = "wrong-password-xyz"


def L(src, pw):
    st, j, hd = lib.login(src, pw)
    return st, (j or {}).get("error", "ok"), hd.get("Retry-After")


print("== 1. one device, 6 wrong tries 1.1 s apart, then right")
for i in range(6):
    print("  .7 wrong", i + 1, L("127.0.0.7", WRONG))
    time.sleep(1.1)
print("  .7 RIGHT (locked):", L("127.0.0.7", PW))
time.sleep(1.1)
print("  .8 RIGHT:", L("127.0.0.8", PW)[:1])

print("== 2. locked .7 floods 300 tries; fresh device logs in meanwhile")
res = []
def flood():
    for _ in range(300):
        res.append(L("127.0.0.7", WRONG)[0])
t = threading.Thread(target=flood); t.start()
time.sleep(0.2)
r = L("127.0.0.9", PW)
t.join()
print("  locked flood codes:", {c: res.count(c) for c in set(res)},
      "| fresh .9 RIGHT during it:", r[:1])

print("== 3. 5 wrong each from 3 devices, then a fresh device")
for a in ("127.0.1.1", "127.0.1.2", "127.0.1.3"):
    for i in range(5):
        st = L(a, WRONG); time.sleep(1.05)
    print(" ", a, "5th answer:", st[0], st[2])
time.sleep(1.1)
print("  fresh 127.0.2.50 RIGHT:", L("127.0.2.50", PW)[:1])

print("== 4. 200 wrong from 40 devices, 8 threads at once")
codes = {}
lock = threading.Lock()
def worker(k):
    for i in range(25):
        st = L(f"127.0.3.{(k * 25 + i) % 40 + 1}", WRONG)
        with lock:
            codes[(st[0], st[2])] = codes.get((st[0], st[2]), 0) + 1
t0 = time.time()
ts = [threading.Thread(target=worker, args=(k,)) for k in range(8)]
[x.start() for x in ts]; [x.join() for x in ts]
dt = time.time() - t0
print(f"  {dt:.2f} s, (status, Retry-After): {codes}")
r1 = L("127.0.3.200", PW)
print("  RIGHT password at once:", r1)
time.sleep(1.1)
print("  RIGHT password 1.1 s later:", L("127.0.3.201", PW)[:1])

print("== 5. sustained: 10 attacker addresses hammer, legit obeys "
      "Retry-After")
stop = threading.Event()
checked = [0]
def attacker():
    i = 0
    while not stop.is_set():
        st = L(f"127.0.5.{i % 10 + 1}", WRONG)[0]
        if st == 401:
            checked[0] += 1
        i += 1
ats = [threading.Thread(target=attacker) for _ in range(2)]
[x.start() for x in ats]
time.sleep(0.3)
t0 = time.time(); tries = 0; got = None
while time.time() - t0 < 90:
    tries += 1
    st, msg, ra = L("127.0.5.200", PW)
    if st == 200:
        got = time.time() - t0
        break
    time.sleep(float(ra or 1))
stop.set(); [x.join() for x in ats]
print(f"  attacker checked tries: {checked[0]}; legit got in after "
      f"{got if got is None else round(got, 1)} s and {tries} tries")

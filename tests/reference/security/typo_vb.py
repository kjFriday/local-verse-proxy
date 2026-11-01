"""A device that mistyped once, while 10 other addresses guess.
usage: typo_vb.py port pwfile"""
import sys, time, threading
sys.path.insert(0, sys.argv[0].rsplit("/", 1)[0])
import lib
lib.PORT = int(sys.argv[1])
PW = open(sys.argv[2]).read().strip()
stop = threading.Event()
checked = [0]


def attacker(k):
    i = 0
    while not stop.is_set():
        st = lib.login(f"127.0.8.{(i * 2 + k) % 10 + 1}", "nope-nope")[0]
        checked[0] += st == 401
        i += 1


ats = [threading.Thread(target=attacker, args=(k,)) for k in range(2)]
[x.start() for x in ats]
time.sleep(0.5)
print("legit typo:", lib.login("127.0.8.200", "typo")[0])
t0 = time.time(); tries = 0; got = None
while time.time() - t0 < 120:
    time.sleep(1.0)
    tries += 1
    st, j, hd = lib.login("127.0.8.200", PW)
    if st == 200:
        got = time.time() - t0
        break
stop.set(); [x.join() for x in ats]
print(f"attacker checked {checked[0]}; typo device got in after "
      f"{None if got is None else round(got, 1)} s, {tries} tries")

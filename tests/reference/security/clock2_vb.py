"""Gate.login with the wall clock and the monotonic clock stepped.
usage: python3 -B clock2_vb.py path/to/gui"""
import sys, time
sys.path.insert(0, sys.argv[1])
import frontdesk as fd
PW = "right-password-123"


def tryit(g, who, pw):
    try:
        g.login(who, pw)
        return "ok"
    except fd.Refused as e:
        return f"refused({e.wait})"
    except ValueError as e:
        return "wrong: " + str(e)[:30]


real, realm = time.time, time.monotonic
g = fd.Gate(PW)
print("wrong .5:", tryit(g, "10.0.0.5", "x"))
for back in (5, 3600, -3600):
    t0 = real()
    fd.time.time = lambda: t0 - back
    print(f"wall clock {-back:+} s: .6 RIGHT {tryit(g, '10.0.0.6', PW)},"
          f" .5 RIGHT {tryit(g, '10.0.0.5', PW)}")
    fd.time.time = real
# same device after a wrong try, real monotonic gap
g = fd.Gate(PW)
tryit(g, "10.0.0.5", "x")
print("same device at once:", tryit(g, "10.0.0.5", PW))
time.sleep(1.05)
print("same device 1.05 s later:", tryit(g, "10.0.0.5", PW))
# session made with wall clock back: does it still expire right?
g = fd.Gate(PW)
t0 = real()
fd.time.time = lambda: t0 - 3600 * 13
ck, cs = g.login("10.0.0.9", PW)
fd.time.time = real
print("session made with clock 13 h back; valid now?",
      g.csrf_for(ck, "10.0.0.9") is not None)
# lock window ages out on monotonic time
g = fd.Gate(PW)
base = realm()
for i in range(5):
    fd.time.monotonic = lambda i=i: base + i * 2
    tryit(g, "10.0.0.7", "x")
fd.time.monotonic = lambda: base + 10
print("locked at +10 s:", tryit(g, "10.0.0.7", PW))
fd.time.monotonic = lambda: base + 8 + 601
print("at +609 s (last fail +8):", tryit(g, "10.0.0.7", PW))
fd.time.monotonic = realm

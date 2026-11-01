"""Gate.login after the wall clock steps back (NTP, a laptop's RTC).
usage: python3 -B clock_vb.py path/to/gui"""
import sys, time
sys.path.insert(0, sys.argv[1])
import frontdesk as fd
real = time.time
g = fd.Gate("right-password-123")
try:
    g.login("10.0.0.5", "wrong")
except ValueError as e:
    print("wrong try:", e)
for back in (5, 3600):
    t0 = real()
    fd.time.time = lambda: t0 - back     # clock stepped back
    for who in ("10.0.0.6", "10.0.0.7"):
        try:
            g.login(who, "right-password-123")
            print(f"stepped back {back} s, {who} RIGHT: ok")
        except fd.Refused as e:
            print(f"stepped back {back} s, {who} RIGHT: refused "
                  f"({e}, Retry-After {e.wait}); refused until the "
                  f"clock passes the old time again, ~{back} s")
    fd.time.time = real

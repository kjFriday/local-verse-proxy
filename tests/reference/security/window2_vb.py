"""Lock length, Retry-After, wait text, and what happens after.
usage: python3 -B window2_vb.py gui"""
import sys
sys.path.insert(0, sys.argv[1])
import frontdesk as fd
PW = "right-password-123"
T = 1000.0
now = [T]
fd.time.monotonic = lambda: now[0]


def at(g, t, pw, addr="10.0.0.7"):
    now[0] = T + t
    try:
        g.login(addr, pw)
        return "200"
    except fd.Refused as e:
        return f"429 RA={e.wait} '{e}'"
    except fd.Locked as e:
        return f"401 LOCKED '{e}'"
    except fd.WrongPassword as e:
        return f"401 '{e}'"


g = fd.Gate(PW)
print("-- spread tries, lock at 560")
for t in (0, 500, 520, 540, 560):
    print(f"t={t:>7}", at(g, t, "x"))
for t in (561, 570, 600, 601, 1100, 1099.5, 1159, 1159.9, 1160,
          1160.01):
    print(f"t={t:>7} right:", at(g, t, PW) if t >= 1160 else
          at(g, t, "x"))
print("-- after lock ends: fresh tries?")
g = fd.Gate(PW)
for t in (0, 2, 4, 6, 8):
    at(g, t, "x")
for t in (608.5, 610, 612, 614, 616, 618, 620):
    print(f"t={t:>7} wrong:", at(g, t, "x"))
print("-- sliding window still: 4 wrong, then wait 590 s, 1 more")
g = fd.Gate(PW)
for t in (0, 2, 4, 6):
    at(g, t, "x")
for t in (596, 598, 604, 606, 608):
    print(f"t={t:>7} wrong:", at(g, t, "x"))
print("fails dict:", {a: list(q) for a, q in g.fails.items()})

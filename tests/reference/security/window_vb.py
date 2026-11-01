"""How long the 'locked for 10 minutes' lock really lasts when the
five wrong tries are spread out. usage: python3 -B window_vb.py gui"""
import sys, time
sys.path.insert(0, sys.argv[1])
import frontdesk as fd
PW = "right-password-123"
g = fd.Gate(PW)
T = 1000.0
now = [T]
fd.time.monotonic = lambda: now[0]


def at(t, pw):
    now[0] = T + t
    try:
        g.login("10.0.0.7", pw)
        return "200"
    except fd.Refused as e:
        return f"429 Retry-After {e.wait}"
    except fd.Locked as e:
        return f"401 Locked: {e}"
    except ValueError as e:
        return f"401 {e}"


for t in (0, 500, 520, 540, 560):
    print(f"t={t:>4} s wrong:", at(t, "x"))
print("t= 570 s right:", at(570, PW))
print("t= 601 s right:", at(601, PW), "(the lock of \"wait 10 minutes\" "
      "ended 41 s after it began)")

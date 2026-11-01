"""Seed N seats via mint-key.sh (keys only to a 0600 file), timing each."""
import json, os, statistics as st, subprocess, sys, time
from concurrent.futures import ThreadPoolExecutor
repo, n = sys.argv[1], int(sys.argv[2])
path = os.path.join(repo, "perf", "keys.json")
keys = json.load(open(path)) if os.path.exists(path) else {}
def mint(i):
    seat = f"seat{i:03d}"
    if seat in keys: return seat, None, 0
    t = time.perf_counter()
    r = subprocess.run(["./mint-key.sh", seat, "--models", "local",
                        "--budget", "50", "--duration", "30d"],
                       cwd=repo, capture_output=True, text=True)
    dt = (time.perf_counter() - t) * 1000
    k = r.stdout.strip()
    if r.returncode or not k.startswith("sk-"):
        print("fail", seat, r.returncode, r.stderr[-200:].replace(k, "?"))
        return seat, None, dt
    return seat, k, dt
times = []
with ThreadPoolExecutor(4) as ex:
    for seat, k, dt in ex.map(mint, range(n)):
        if k: keys[seat] = k; times.append(dt)
fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
os.write(fd, json.dumps(keys).encode()); os.close(fd)
if times:
    times.sort()
    print(f"minted {len(times)} via mint-key.sh (4 parallel): p50 "
          f"{st.median(times):.0f} ms p95 {times[int(.95*len(times))-1]:.0f} ms")
print("total keys on file:", len(keys))

"""Tally and check timing for one frontdesk.py, on the persistent psql.
usage: tally_bench.py repo guidir"""
import importlib.util, statistics as st, subprocess, sys, time
repo, gui = sys.argv[1], sys.argv[2]
spec = importlib.util.spec_from_file_location("fd", f"{gui}/frontdesk.py")
fd = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fd)


def pct(xs):
    xs = sorted(xs)
    n = len(xs)
    return (f"p50 {st.median(xs):7.2f} p95 {xs[min(n-1, int(.95*n))]:7.2f}"
            f" ms n={n}")


def cg(svc):
    cid = subprocess.run(["docker", "compose", "ps", "-q", svc], cwd=repo,
                         capture_output=True, text=True).stdout.strip()
    path = f"/sys/fs/cgroup/cpuacct/docker/{cid}/cpuacct.usage"
    return lambda: int(open(path).read()) / 1e6


pg, lt = cg("db"), cg("proxy")
settings = fd.Settings(f"{repo}/seats.env")
psql = fd.Psql(repo)
t = time.perf_counter()
psql.query("select json_build_object('a', 1)")
print(f"psql spawn + first query {(time.perf_counter()-t)*1e3:.1f} ms")
desk = fd.Desk(settings, psql, False)
# The tally path as the poller runs it, 40 times.
fn = getattr(desk, "tally_query", None) or \
    (lambda: psql.query(fd.TALLY_SQL))
xs, c0 = [], pg()
for i in range(40):
    t = time.perf_counter()
    j = fn()
    xs.append((time.perf_counter() - t) * 1e3)
print(f"tally as polled  {pct(xs)}  Postgres CPU "
      f"{(pg()-c0)/40:.1f} ms/run, rows {len(j['rows'])}")
if hasattr(fd, "HISTORY_SQL"):
    xs, c0 = [], pg()
    for i in range(10):
        t = time.perf_counter()
        psql.query(fd.HISTORY_SQL)
        xs.append((time.perf_counter() - t) * 1e3)
    print(f"history query    {pct(xs)}  Postgres CPU "
          f"{(pg()-c0)/10:.1f} ms/run (once per "
          f"{fd.HISTORY_EVERY:.0f} s)")
# Whole checks at the real cadence for 60 s, as with one open page.
xs = []
c0, l0, t0 = pg(), lt(), time.time()
end = time.monotonic() + 60
while time.monotonic() < end:
    t = time.perf_counter()
    desk.refresh()
    xs.append((time.perf_counter() - t) * 1e3)
    time.sleep(max(0, fd.FAST - (time.perf_counter() - t)))
dt = time.time() - t0
print(f"check (refresh)  {pct(xs)}; per second of watching: Postgres "
      f"{(pg()-c0)/dt/10:.2f}% core, LiteLLM {(lt()-l0)/dt/10:.2f}% core")
print("cards", len(desk.state["cards"]), "tally rows",
      len(desk.state["tally"]["seats"]) + len(desk.state["tally"]["models"]))
psql.stop()

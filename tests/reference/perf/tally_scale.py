"""Tally SQL at 146k daily rows: shipped query vs today-only query
(history cached), persistent psql; Postgres CPU per run."""
import statistics as st, subprocess, sys, time
repo = sys.argv[1]; sys.path.insert(0, repo + "/gui"); import frontdesk as fd
def pct(xs):
    xs = sorted(xs); n = len(xs)
    return f"p50 {st.median(xs):7.2f} p95 {xs[min(n-1,int(.95*n))]:7.2f} ms n={n}"
cid = subprocess.run(["docker", "compose", "ps", "-q", "db"], cwd=repo,
                     capture_output=True, text=True).stdout.strip()
CG = f"/sys/fs/cgroup/cpuacct/docker/{cid}/cpuacct.usage"
p = fd.Psql(repo); p.query("select json_build_object('a',1)")
TODAY = fd.TALLY_SQL.replace(
    'from "LiteLLM_DailyUserSpend" d left join n on n.token = d.api_key)',
    'from "LiteLLM_DailyUserSpend" d left join n on n.token = d.api_key\n'
    "  where d.date = to_char(now() at time zone 'UTC', 'YYYY-MM-DD'))")
HIST = TODAY.replace("where d.date =", "where d.date <")
assert TODAY != fd.TALLY_SQL and HIST != TODAY
for name, q in (("shipped all-in-one", fd.TALLY_SQL), ("today only", TODAY),
                ("history < today", HIST)):
    xs = []; c0 = int(open(CG).read())
    for _ in range(40):
        t = time.perf_counter(); j = p.query(q); xs.append((time.perf_counter()-t)*1e3)
    cpu = (int(open(CG).read()) - c0) / 40 / 1e6
    print(f"{name:20s} {pct(xs)}  Postgres CPU {cpu:5.1f} ms/run  rows {len(j['rows'])}")
r = p.query("select json_build_object('n', (select count(*) from \"LiteLLM_DailyUserSpend\"))")
print("daily rows:", r["n"])
p.stop()

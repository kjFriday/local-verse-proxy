"""Mint+revoke latency A/B, and check the card shows/vanishes at once."""
import json, sys, threading, time, http.client
sys.argv = [sys.argv[0], sys.argv[1]] + sys.argv[2:]
import importlib.util
spec = importlib.util.spec_from_file_location("load", sys.argv[0].replace("mint_ab.py", "load.py"))
port = int(sys.argv[1]); tag = sys.argv[2]
L = importlib.util.module_from_spec(spec); sys.argv = ["load.py", str(port)]; spec.loader.exec_module(L)
def seats():
    c = L.conn(); r, b = L.get(c, "/api/state"); c.close()
    return {x["seat"] for x in json.loads(b)["cards"]}
m, v, bad = [], [], 0
for i in range(20):
    seat = f"ab-{tag}-{i}"
    code, j, dt = L.post("/api/mint", {"seat": seat, "models": ["local"], "budget": "1", "duration": "1h"})
    assert code == 200 and L.KEY.match(j.pop("key", "")), (code, j)
    m.append(dt); bad += seat not in seats()
    code, j, dt = L.post("/api/revoke", {"seat": seat}); assert code == 200, j
    v.append(dt); bad += seat in seats()
    time.sleep(0.37)  # land at different points of the poller's cycle
print(f"port {port} mint  ", L.pct(m)); print(f"port {port} revoke", L.pct(v))
print(f"port {port} state wrong right after mint/revoke: {bad} of 40")

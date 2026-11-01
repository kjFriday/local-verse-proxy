"""Chat calls through the proxy with many keys, concurrently.
usage: chat.py repo total threads [seconds_rate_limit]"""
import http.client, json, random, statistics as st, sys, threading, time
repo, total, threads = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
pace = float(sys.argv[4]) if len(sys.argv) > 4 else 0
keys = list(json.load(open(f"{repo}/perf/keys.json")).values())
lock = threading.Lock(); left = [total]; codes = {}; lat = []
def worker(w):
    c = http.client.HTTPConnection("127.0.0.1", 4116, timeout=30)
    rnd = random.Random(w)
    while True:
        with lock:
            if left[0] <= 0: return
            left[0] -= 1
        k = rnd.choice(keys)
        body = json.dumps({"model": "local", "messages": [
            {"role": "user", "content": "hi " + str(rnd.random())}]})
        t = time.perf_counter()
        try:
            c.request("POST", "/v1/chat/completions", body,
                      {"Authorization": "Bearer " + k,
                       "Content-Type": "application/json"})
            r = c.getresponse(); r.read(); code = r.status
        except Exception as e:
            code = type(e).__name__
            c.close(); c = http.client.HTTPConnection("127.0.0.1", 4116, timeout=30)
        with lock:
            codes[code] = codes.get(code, 0) + 1
            lat.append((time.perf_counter() - t) * 1000)
        if pace: time.sleep(pace)
t0 = time.perf_counter()
ts = [threading.Thread(target=worker, args=(i,)) for i in range(threads)]
[t.start() for t in ts]; [t.join() for t in ts]
dt = time.perf_counter() - t0
lat.sort()
print(f"{total} calls, {threads} threads, {dt:.1f} s, {total/dt:.0f} rps, "
      f"codes {codes}, p50 {st.median(lat):.0f} ms p95 {lat[int(.95*len(lat))]:.0f} ms p99 {lat[int(.99*len(lat))]:.0f} ms")

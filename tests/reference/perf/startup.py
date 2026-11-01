"""Startup: exec -> first page, first /api/state, idle RSS.
usage: startup.py repo guidir port"""
import http.client, statistics as st, subprocess, sys, time
repo, gui, port = sys.argv[1], sys.argv[2], int(sys.argv[3])
page, first, rss = [], [], []
for i in range(5):
    t = time.perf_counter()
    p = subprocess.Popen([sys.executable, f"{gui}/frontdesk.py", "--port",
                          str(port)], cwd=repo, stdout=subprocess.DEVNULL,
                         stderr=subprocess.DEVNULL)
    while True:
        try:
            c = http.client.HTTPConnection("127.0.0.1", port, timeout=5)
            c.request("GET", "/")
            r = c.getresponse()
            r.read()
            if r.status == 200:
                break
        except OSError:
            time.sleep(0.005)
    page.append((time.perf_counter() - t) * 1e3)
    c.request("GET", "/api/state")
    r = c.getresponse()
    r.read()
    first.append((time.perf_counter() - t) * 1e3)
    time.sleep(5)
    for line in open(f"/proc/{p.pid}/status"):
        if line.startswith("VmRSS"):
            rss.append(int(line.split()[1]) / 1024)
    p.terminate()
    p.wait()
    time.sleep(0.5)
print(f"exec -> first page p50 {st.median(page):.0f} ms "
      f"({min(page):.0f}-{max(page):.0f}); exec -> first state p50 "
      f"{st.median(first):.0f} ms ({min(first):.0f}-{max(first):.0f}); "
      f"RSS 5 s later p50 {st.median(rss):.1f} MB")

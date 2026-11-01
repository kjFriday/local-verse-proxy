"""Live-stream cost per viewer, like a browser: read /api/events; on a
'changed' event fetch /api/state (gzip + If-None-Match, keep-alive).
Counts wire bytes both ways. Works for the old stream (full 'state'
events) and the new one ('changed' + fetch).
usage: sse_bench2.py repo port pid secs [traffic]   (N viewers env)"""
import os, socket, subprocess, sys, threading, time
repo, port, pid, secs = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), \
    float(sys.argv[4])
traffic = len(sys.argv) > 5 and sys.argv[5] == "traffic"
N = int(os.environ.get("N", "20"))
CLK = os.sysconf("SC_CLK_TCK")


def fcpu():
    f = open(f"/proc/{pid}/stat").read().rsplit(")", 1)[1].split()
    return (int(f[11]) + int(f[12])) / CLK


def rss():
    for line in open(f"/proc/{pid}/status"):
        if line.startswith("VmRSS"):
            return int(line.split()[1]) / 1024


def cg(svc):
    cid = subprocess.run(["docker", "compose", "ps", "-q", svc], cwd=repo,
                         capture_output=True, text=True).stdout.strip()
    path = f"/sys/fs/cgroup/cpuacct/docker/{cid}/cpuacct.usage"
    return lambda: int(open(path).read()) / 1e9


lcpu, pcpu = cg("proxy"), cg("db")
stop = threading.Event()
live = {}


def fetch_state(st):
    """GET /api/state on the viewer's keep-alive connection."""
    if st.get("c") is None:
        st["c"] = socket.create_connection(("127.0.0.1", port))
        st["f"] = st["c"].makefile("rb")
    req = (f"GET /api/state HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\n"
           "Accept: */*\r\nAccept-Encoding: gzip, deflate, br\r\n"
           "Cache-Control: no-cache\r\n" +
           (f"If-None-Match: {st['etag']}\r\n" if st.get("etag") else "")
           + "\r\n").encode()
    st["c"].sendall(req)
    st["up"] += len(req)
    f = st["f"]
    line = f.readline()
    if not line:
        raise ConnectionResetError("closed")
    st["down"] += len(line)
    clen = 0
    while True:
        h = f.readline()
        st["down"] += len(h)
        if h in (b"\r\n", b""):
            break
        k, _, v = h.decode().partition(":")
        if k.lower() == "content-length":
            clen = int(v)
        if k.lower() == "etag":
            st["etag"] = v.strip()
    st["down"] += len(f.read(clen))
    st["fetches"] += 1
    if b" 200 " in line:
        st["full"] += 1


def viewer():
    st = {"down": 0, "up": 0, "fetches": 0, "full": 0, "events": 0}
    s = socket.create_connection(("127.0.0.1", port))
    req = (f"GET /api/events HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\n"
           "Accept: text/event-stream\r\nAccept-Encoding: gzip\r\n\r\n"
           ).encode()
    s.sendall(req)
    st["up"] += len(req)
    s.settimeout(0.5)
    buf = b""
    while not stop.is_set():
        try:
            b = s.recv(65536)
        except socket.timeout:
            continue
        if not b:
            break
        st["down"] += len(b)
        buf += b
        while b"\n\n" in buf:
            ev, buf = buf.split(b"\n\n", 1)
            if b"event: state" in ev:
                st["events"] += 1
                st["full"] += 1
            elif b"event: changed" in ev:
                st["events"] += 1
                try:
                    fetch_state(st)
                except OSError:
                    # The server closed the idle keep-alive connection:
                    # a browser just opens a new one, so do the same.
                    st["c"] = None
                    st["reconnects"] = st.get("reconnects", 0) + 1
                    fetch_state(st)
        live[threading.get_ident()] = dict(st)
    s.close()


bg = None
if traffic:
    bg = subprocess.Popen([sys.executable, "-I",
                           os.path.join(os.path.dirname(__file__),
                                        "chat.py"), repo,
                           str(int(secs * 12 + 60)), "4", "0.3"],
                          stdout=subprocess.DEVNULL,
                          stderr=subprocess.DEVNULL)
ts = [threading.Thread(target=viewer, daemon=True) for _ in range(N)]
[t.start() for t in ts]
time.sleep(3)
c0, l0, g0, t0 = fcpu(), lcpu(), pcpu(), time.time()
s0 = {k: dict(v) for k, v in live.items()}
time.sleep(secs)
c1, l1, g1, t1 = fcpu(), lcpu(), pcpu(), time.time()
s1 = {k: dict(v) for k, v in live.items()}
r = rss()
stop.set()
[t.join(2) for t in ts]
if bg:
    bg.terminate()
dt = t1 - t0
z = {"down": 0, "up": 0, "fetches": 0, "full": 0, "events": 0}


def d(f):
    xs = [s1[k][f] - s0.get(k, z)[f] for k in s1]
    return sum(xs) / max(1, len(xs))


mbh = 3600 / dt / 1024 / 1024
rc = sum(v.get("reconnects", 0) for v in s1.values()) / max(1, len(s1))
print(f"x{N} traffic={traffic} {dt:.0f}s: FD CPU {(c1-c0)/dt*100:.2f}% "
      f"core, RSS {r:.1f} MB, LiteLLM CPU {(l1-l0)/dt*100:.1f}%, Postgres "
      f"CPU {(g1-g0)/dt*100:.2f}% | per viewer: down {d('down')*mbh:.2f} "
      f"MB/h, up {d('up')*mbh:.2f} MB/h, events {d('events'):.1f}, full "
      f"states {d('full'):.1f}, fetches {d('fetches'):.1f} in window, "
      f"new connections {rc:.1f}")

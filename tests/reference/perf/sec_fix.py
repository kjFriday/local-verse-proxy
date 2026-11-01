"""Security checks for the fixes. Prints no secrets.
usage: sec_fix.py repo lan_port local_port pwfile lan_pid"""
import http.client, importlib.util, json, os, re, socket, subprocess, sys
import tempfile, threading, time
repo, lan, loc, pwfile, lan_pid = sys.argv[1], int(sys.argv[2]), \
    int(sys.argv[3]), sys.argv[4], int(sys.argv[5])
PW = open(pwfile).read().strip()
IP = "192.0.2.2"


def req(port, method, path, host=None, body=None, headers=None,
        src="127.0.0.1"):
    s = socket.create_connection(("127.0.0.1", port),
                                 source_address=(src, 0))
    c = http.client.HTTPConnection("127.0.0.1", port)
    c.sock = s
    h = {"Host": host or f"127.0.0.1:{port}"}
    h.update(headers or {})
    if body is not None:
        body = json.dumps(body)
        h["Content-Type"] = "application/json"
    c.request(method, path, body, h)
    r = c.getresponse()
    data = r.read()
    c.close()
    return r, data


print("== --lan Host check (DNS rebinding)")
me = socket.gethostname()
for host in [f"{IP}:{lan}", f"127.0.0.1:{lan}", f"localhost:{lan}",
             f"{me}:{lan}", f"{me}.local:{lan}", f"rebind.test:{lan}",
             f"evil.example:{lan}", f"{IP}:9999", IP, f"{IP}.nip.io:{lan}",
             f"[::1]:{lan}", ""]:
    r, _ = req(lan, "GET", "/api/session", host=host)
    print(f"  GET /api/session Host {host!r:28} -> {r.status}")
r, b = req(lan, "POST", "/api/login", host=f"rebind.test:{lan}",
           body={"password": "wrong-wrong-wrong"},
           headers={"Origin": f"http://rebind.test:{lan}"})
print(f"  POST /api/login via rebind.test -> {r.status} (never reaches "
      "the password check, so it cannot lock anyone out)")

print("== login: tries left, then the lock")
for i in range(2):
    r, b = req(lan, "POST", "/api/login", body={"password": "nope" * 4},
               src="127.0.0.5")
    print(f"  wrong try {i + 1}: {r.status} {json.loads(b)['error']}")

print("== session bound to the address that logged in")
r, b = req(lan, "POST", "/api/login", body={"password": PW})
ck = r.getheader("Set-Cookie")
name = ck.split("=", 1)[0]
cookie = ck.split(";", 1)[0]
flags = "; ".join(p.strip() for p in ck.split(";")[1:])
print(f"  login {r.status}, cookie name {name}, flags: {flags}")
csrf = json.loads(b)["csrf"]
for src in ("127.0.0.1", "127.0.0.3"):
    r, b = req(lan, "GET", "/api/state", headers={"Cookie": cookie},
               src=src)
    r2, b2 = req(lan, "GET", "/api/session", headers={"Cookie": cookie},
                 src=src)
    print(f"  from {src}: /api/state {r.status}, /api/session csrf "
          f"{'given' if json.loads(b2)['csrf'] else 'null'}")
r, b = req(lan, "GET", "/api/state", headers={"Cookie": "fd=" +
           cookie.split("=", 1)[1]})
print(f"  old cookie name 'fd' -> {r.status}")

print("== http.server's own errors, HEAD")
r, b = req(lan, "HEAD", "/", host=f"{IP}:{lan}")
print(f"  HEAD / -> {r.status}, body {len(b)} B, CSP "
      f"{'yes' if r.getheader('Content-Security-Policy') else 'no'}, "
      f"length header {r.getheader('Content-Length')}")
r, b = req(lan, "HEAD", "/api/state", host=f"{IP}:{lan}")
print(f"  HEAD /api/state -> {r.status}")
r, b = req(lan, "PUT", "/", host=f"{IP}:{lan}")
print(f"  PUT / -> {r.status} {r.getheader('Content-Type')} {b!r} XFO "
      f"{r.getheader('X-Frame-Options')} nosniff "
      f"{r.getheader('X-Content-Type-Options')} CSP "
      f"{'yes' if r.getheader('Content-Security-Policy') else 'no'}")
s = socket.create_connection(("127.0.0.1", lan))
s.sendall(b"GET / HTTP/<script>alert(1)</script>\r\n\r\n")
raw = s.recv(4096)
s.close()
head, _, body = raw.partition(b"\r\n\r\n")
print("  bad request line ->", head.split(b"\r\n")[0], body[:60],
      "CSP" if b"Content-Security-Policy" in head else "no CSP")

print("== FRONTDESK_PASSWORD not in any child's environment")
kids = subprocess.run(["pgrep", "-P", str(lan_pid)], capture_output=True,
                      text=True).stdout.split()
for k in kids:
    env = open(f"/proc/{k}/environ", "rb").read().split(b"\0")
    hits = sum(e.startswith(b"FRONTDESK_PASSWORD=") for e in env)
    cmd = open(f"/proc/{k}/cmdline", "rb").read().split(b"\0")[:3]
    print(f"  child {b' '.join(cmd).decode()[:40]}: {hits} entries")
env = open(f"/proc/{lan_pid}/environ", "rb").read()
print("  Front Desk's own /proc environ (as started):",
      b"FRONTDESK_PASSWORD=" in env, "(the exec-time copy; os.environ "
      "no longer has it)")

print("== thread cap and read timeout")


def threads():
    return len(os.listdir(f"/proc/{lan_pid}/task"))


def rss():
    for line in open(f"/proc/{lan_pid}/status"):
        if line.startswith("VmRSS"):
            return line.split()[1] + " kB"


print(f"  baseline: {threads()} threads, RSS {rss()}")
socks = []
for i in range(400):
    try:
        x = socket.create_connection(("127.0.0.1", lan))
        if i % 2:
            x.sendall(b"POST /api/login HTTP/1.1\r\nHost: " +
                      f"{IP}:{lan}".encode() + b"\r\nContent-Type: "
                      b"application/json\r\nContent-Length: 8000\r\n\r\n{")
        else:
            x.sendall(b"GET / HTTP/1.1\r\n")
        socks.append(x)
    except OSError as e:
        print("  connect failed:", e)
        break
time.sleep(1)
closed = 0
for x in socks:
    x.settimeout(0.01)
    try:
        if x.recv(1) == b"":
            closed += 1
    except (socket.timeout, OSError):
        pass
print(f"  400 stalled connections: {threads()} threads, RSS {rss()}, "
      f"{closed} closed at once by the cap")
t = time.perf_counter()
try:
    r, _ = req(lan, "GET", "/", host=f"{IP}:{lan}")
    print(f"  a page load now: {r.status} in "
          f"{(time.perf_counter() - t) * 1e3:.1f} ms")
except OSError as e:
    print(f"  a page load now: refused ({e.__class__.__name__}) - the cap"
          " is full")
time.sleep(11)
print(f"  11 s later (read timeout 10 s): {threads()} threads")
t = time.perf_counter()
r, _ = req(lan, "GET", "/", host=f"{IP}:{lan}")
print(f"  a page load: {r.status} in {(time.perf_counter() - t) * 1e3:.1f}"
      " ms")
for x in socks:
    x.close()

print("== mint-key.sh's error text never reaches the page raw")
spec = importlib.util.spec_from_file_location("fd", f"{repo}/gui/"
                                              "frontdesk.py")
fd = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fd)
real = fd.read_env(f"{repo}/seats.env")
fake = "sk-" + os.urandom(24).hex()
with tempfile.NamedTemporaryFile("w", suffix=".env", delete=False) as f:
    for k, v in real.items():
        f.write(f"{k}={fake if k == 'LITELLM_MASTER_KEY' else v}\n")
os.chmod(f.name, 0o600)
r = subprocess.run([f"{repo}/mint-key.sh", "errprobe", "--models",
                    "local"], cwd=repo, env=dict(os.environ,
                    SEATS_ENV=f.name), capture_output=True, text=True)
settings = fd.Settings(f.name)
fd.log = lambda m: print("  terminal log line:", m[:90] + "…")


class Stub:
    pass


st = Stub()
st.settings = settings
msg = fd.App.plain_error(st, r.stderr)
raw_leaks = fake[-4:] in r.stderr, bool(re.search(r"[0-9a-f]{64}",
                                                  r.stderr))
print(f"  raw stderr has key end: {raw_leaks[0]}, has a 64-hex hash: "
      f"{raw_leaks[1]}")
print(f"  page gets: {msg!r}")
print("  page text has sk-/hex:", bool(re.search(
    r"sk-|[0-9a-fA-F]{32,}", msg)))
os.unlink(f.name)

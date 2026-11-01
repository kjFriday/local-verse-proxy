"""Per-address connection cap with --lan. usage: cap_vb.py port pid"""
import http.client, os, socket, sys, time, threading
port, pid = int(sys.argv[1]), int(sys.argv[2])
HOST = f"192.0.2.2:{port}"


def threads():
    return len(os.listdir(f"/proc/{pid}/task"))


def page(src):
    t = time.perf_counter()
    try:
        s = socket.create_connection(("127.0.0.1", port),
                                     source_address=(src, 0), timeout=5)
        c = http.client.HTTPConnection("127.0.0.1", port, timeout=5)
        c.sock = s
        c.request("GET", "/", headers={"Host": HOST})
        r = c.getresponse()
        n = len(r.read())
        c.close()
        return f"{r.status} {n}B in {(time.perf_counter() - t) * 1e3:.1f} ms"
    except OSError as e:
        return f"refused ({e.__class__.__name__})"


def stall(src, n):
    socks = []
    for i in range(n):
        x = socket.create_connection(("127.0.0.1", port),
                                     source_address=(src, 0))
        x.sendall(b"GET / HTTP/1.1\r\n")
        socks.append(x)
    time.sleep(0.5)
    open_ = []
    for x in socks:
        x.settimeout(0.01)
        try:
            if x.recv(1) == b"":
                x.close()
                continue
        except (socket.timeout, BlockingIOError):
            pass
        except OSError:
            x.close()
            continue
        open_.append(x)
    return open_


print(f"baseline: {threads()} threads")
held = stall("127.0.0.5", 400)
print(f"A: 127.0.0.5 opens 400 stalled: {len(held)} still open, "
      f"{threads()} threads")
res = {}
def go(i):
    res[i] = page(f"127.0.6.{i}")
ts = [threading.Thread(target=go, args=(i,)) for i in range(1, 21)]
[t.start() for t in ts]; [t.join() for t in ts]
ok = sum(v.startswith("200") for v in res.values())
print(f"   20 other devices load the page at once: {ok}/20 ok;",
      "e.g.", res[1], "|", res[20])
print("   same device 127.0.0.5:", page("127.0.0.5"))
# slowloris: drip one header byte every 4 s on the held sockets
stop = threading.Event()
def drip():
    while not stop.wait(4):
        for x in held:
            try:
                x.sendall(b"X")
            except OSError:
                pass
threading.Thread(target=drip, daemon=True).start()
time.sleep(13)
alive = 0
for x in held:
    x.settimeout(0.01)
    try:
        if x.recv(1) != b"":
            alive += 1
    except (socket.timeout, BlockingIOError):
        alive += 1
    except OSError:
        pass
print(f"B: after 13 s of drip, {alive} of 127.0.0.5's conns alive; "
      f"other device: {page('127.0.6.99')}; threads {threads()}")
stop.set()
for x in held:
    x.close()
time.sleep(0.5)
print("   127.0.0.5 after letting go:", page("127.0.0.5"),
      f"threads {threads()}")
# C: two addresses of one attacker: still only 64 slots
h2 = stall("127.0.0.11", 100) + stall("127.0.0.12", 100)
print(f"C: 2 addresses x 100 stalled: {len(h2)} held; other device:",
      page("127.0.6.50"))
for x in h2:
    x.close()
# D: 8 addresses x 40 = global 256 full
h3 = []
for i in range(8):
    h3 += stall(f"127.0.4.{i + 1}", 40)
print(f"D: 8 addresses x 40 stalled: {len(h3)} held, threads {threads()};"
      f" 9th device: {page('127.0.4.99')}")
for x in h3:
    x.close()
time.sleep(0.5)
print("   after they let go, 9th device:", page("127.0.4.99"),
      f"threads {threads()}")

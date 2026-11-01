"""Goldens for Front Desk's gate and start (internal/frontdesk/gate,
internal/frontdesk): Gate traces on pinned clocks and tokens (the lock
windows and clock steps of window_vb, window2_vb, clock_vb, clock2_vb
in tests/reference/security, sessions, the 100-session cap), and
frontdesk.py run for real for its banners, start errors and flag
errors. Passwords and ports are masked; every key is made up.
python3 -B tests/golden/frontdesk_gate.py"""
import itertools
import os
import re
import signal
import socket
import stat
import subprocess
import sys
import tempfile
import time

import common

PW = "right-password-123"


def trace(fd, password, ops):
    """Run ops on one Gate: the clocks and tokens are the trace's."""
    clock = {"wall": 1e9, "mono": 1000.0}
    fd.time.time = lambda: clock["wall"]
    fd.time.monotonic = lambda: clock["mono"]
    n = itertools.count()
    fd.secrets.token_urlsafe = lambda size: "T%d" % next(n)
    g = fd.Gate(password)
    cookies = {}
    out = []
    for op in ops:
        clock["wall"] = op.get("wall", clock["wall"])
        clock["mono"] = op.get("mono", clock["mono"])
        kind = op["op"]
        if kind == "login":
            try:
                c, s = g.login(op["addr"], op["pw"])
                cookies[int(c[1:])] = c
                r = {"r": "ok", "cookie": int(c[1:]), "csrf": int(s[1:])}
            except fd.Refused as e:
                r = {"r": "refused", "msg": str(e), "wait": e.wait}
            except fd.WrongPassword as e:
                r = {"r": "wrong", "msg": str(e),
                     "locked": isinstance(e, fd.Locked)}
        elif kind == "csrf":
            c = cookies.get(op["cookie"], "T999999")
            if op["cookie"] is None:
                c = None
            s = g.csrf_for(c, op["addr"])
            r = {"csrf": None if s is None else int(s[1:])}
        elif kind == "logout":
            g.logout(cookies.get(op["cookie"], ""))
            r = {}
        elif kind == "count":
            r = {"sessions": len(g.sessions)}
        out.append(dict(op, result=r))
    return {"password": password, "open_csrf": 0, "ops": out}


def lock_ops():
    a = "10.0.0.7"
    ops = []
    for t in (0, 500, 520, 540, 560):
        ops.append({"op": "login", "mono": 1000 + t, "addr": a,
                    "pw": "x"})
    for t in (561, 570, 600, 601, 1100, 1099.5, 1159, 1159.9):
        ops.append({"op": "login", "mono": 1000 + t, "addr": a,
                    "pw": "x"})
    for t in (1160, 1160.01):
        ops.append({"op": "login", "mono": 1000 + t, "addr": a,
                    "pw": PW})
    return ops


def fresh_ops():
    a = "10.0.0.7"
    ops = [{"op": "login", "mono": 1000 + t, "addr": a, "pw": "x"}
           for t in (0, 2, 4, 6, 8)]
    ops += [{"op": "login", "mono": 1000 + t, "addr": a, "pw": "x"}
            for t in (608.5, 610, 612, 614, 616, 618, 620)]
    return ops


def sliding_ops():
    a = "10.0.0.7"
    ops = [{"op": "login", "mono": 1000 + t, "addr": a, "pw": "x"}
           for t in (0, 2, 4, 6)]
    ops += [{"op": "login", "mono": 1000 + t, "addr": a, "pw": "x"}
            for t in (596, 598, 604, 606, 608)]
    return ops


def window_ops():
    a = "10.0.0.7"
    ops = [{"op": "login", "mono": 1000 + t, "addr": a, "pw": "x"}
           for t in (0, 500, 520, 540, 560)]
    ops += [{"op": "login", "mono": 1570, "addr": a, "pw": PW},
            {"op": "login", "mono": 1601, "addr": a, "pw": PW}]
    return ops


def clock_ops():
    ops = [{"op": "login", "mono": 1000, "addr": "10.0.0.5",
            "pw": "wrong"}]
    for back in (5, 3600, -3600):
        for who in ("10.0.0.6", "10.0.0.5"):
            ops.append({"op": "login", "wall": 1e9 - back,
                        "mono": 1000.5, "addr": who, "pw": PW})
    ops += [{"op": "login", "wall": 1e9, "mono": 1001.6,
             "addr": "10.0.0.5", "pw": PW},
            {"op": "login", "wall": 1e9, "mono": 1002, "addr": "10.0.0.8",
             "pw": "x"},
            {"op": "login", "mono": 1002.3, "addr": "10.0.0.8", "pw": PW},
            {"op": "login", "mono": 1003.05, "addr": "10.0.0.8",
             "pw": PW}]
    return ops


def lock_age_ops():
    a = "10.0.0.7"
    ops = [{"op": "login", "mono": 1000 + i * 2, "addr": a, "pw": "x"}
           for i in range(5)]
    ops += [{"op": "login", "mono": 1010, "addr": a, "pw": PW},
            {"op": "login", "mono": 1000 + 8 + 601, "addr": a, "pw": PW}]
    return ops


def gap_ops():
    # Devices that got it wrong share one try a second; a fresh one
    # is always let in.
    ops = []
    for i, a in enumerate(("10.0.0.1", "10.0.0.2", "10.0.0.1",
                           "10.0.0.3", "10.0.0.2")):
        ops.append({"op": "login", "mono": 1000 + i * 0.4, "addr": a,
                    "pw": "x"})
    ops += [{"op": "login", "mono": 1001.7, "addr": "10.0.0.4",
             "pw": PW},
            {"op": "login", "mono": 1001.75, "addr": "10.0.0.1",
             "pw": PW},
            {"op": "login", "mono": 1003, "addr": "10.0.0.1", "pw": PW}]
    return ops


def session_ops():
    ops = [{"op": "login", "wall": 1e9, "addr": "10.0.0.9", "pw": PW},
           {"op": "csrf", "cookie": 1, "addr": "10.0.0.9"},
           {"op": "csrf", "cookie": 1, "addr": "10.0.0.10"},
           {"op": "csrf", "cookie": None, "addr": "10.0.0.9"},
           {"op": "csrf", "cookie": 7777, "addr": "10.0.0.9"},
           {"op": "csrf", "wall": 1e9 + 43199.5, "cookie": 1,
            "addr": "10.0.0.9"},
           {"op": "csrf", "wall": 1e9 + 43200, "cookie": 1,
            "addr": "10.0.0.9"},
           {"op": "count"},
           {"op": "login", "wall": 1e9 + 43300, "addr": "10.0.0.9",
            "pw": PW},
           {"op": "logout", "cookie": 3},
           {"op": "csrf", "cookie": 3, "addr": "10.0.0.9"},
           {"op": "count"},
           {"op": "login", "wall": 1e9 - 3600 * 13, "addr": "10.0.0.9",
            "pw": PW},
           {"op": "csrf", "wall": 1e9, "cookie": 5, "addr": "10.0.0.9"},
           {"op": "count"}]
    # 101 logins: the oldest goes; then expired ones go at a login.
    for i in range(101):
        ops.append({"op": "login", "wall": 2e9 + i, "addr": "10.1.0.1",
                    "pw": PW})
    ops += [{"op": "count"},
            {"op": "csrf", "wall": 2e9 + 101, "cookie": 7,
             "addr": "10.1.0.1"},
            {"op": "csrf", "cookie": 9, "addr": "10.1.0.1"},
            {"op": "login", "wall": 2e9 + 43200 + 50.5,
             "addr": "10.1.0.1", "pw": PW},
            {"op": "count"},
            {"op": "login", "addr": "10.1.0.1",
             "pw": "p\u00e4ssw\u00f6rd-long-1"}]
    return ops


def open_ops():
    return [{"op": "csrf", "cookie": None, "addr": "1.2.3.4"},
            {"op": "csrf", "cookie": 7777, "addr": "1.2.3.4"}]


FAKE_DOCKER = "#!/bin/sh\ncat >/dev/null\nexit 1\n"


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    p = s.getsockname()[1]
    s.close()
    return p


def lan_ip(fd):
    return fd.lan_ip() or "<proxy-ip>"


def run_desk(argv, env, timeout=10):
    """frontdesk.py with argv; stopped by Ctrl+C once its banner is
    out. Gives (stdout, stderr, exit code)."""
    p = subprocess.Popen([sys.executable, "-B", "gui/frontdesk.py",
                          *argv], cwd=common.REPO, env=env,
                         stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                         text=True)
    out = []
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        line = p.stdout.readline()
        if not line:
            break
        out.append(line)
        if line.startswith("Stop it with"):
            p.send_signal(signal.SIGINT)
    try:
        rest, err = p.communicate(timeout=timeout)
    except subprocess.TimeoutExpired:
        p.kill()
        rest, err = p.communicate()
    # The server thread may print a traceback for the Ctrl+C on some
    # versions; only the first line of stderr matters to us.
    return "".join(out) + rest, err, p.returncode


def mask(text, port, ip):
    text = re.sub(r"Password, (shown once|new at each start): \S+",
                  r"Password, \1: <password>", text)
    text = text.replace(":%d" % port, ":<port>")
    text = text.replace("--port %d" % (port + 1), "--port <port+1>")
    text = text.replace("port %d:" % port, "port <port>:")
    if ip:
        text = text.replace("//" + ip + ":", "//<lan-ip>:")
    return text


def starts(fd, tmp):
    bindir = os.path.join(tmp, "bin")
    os.mkdir(bindir)
    with open(os.path.join(bindir, "docker"), "w") as f:
        f.write(FAKE_DOCKER)
    os.chmod(os.path.join(bindir, "docker"), 0o755)
    good = os.path.join(tmp, "seats.env")
    with open(good, "w") as f:
        f.write("LITELLM_MASTER_KEY=sk-fake-start-master\n"
                "LITELLM_SALT_KEY=sk-fake-start-salt\n"
                "PROXY_URL=http://127.0.0.1:9\n")
    nomaster = os.path.join(tmp, "nomaster.env")
    with open(nomaster, "w") as f:
        f.write("PROXY_URL=http://127.0.0.1:9\n")
    base = {"PATH": bindir + os.pathsep + os.environ["PATH"],
            "LANG": "C.UTF-8", "HOME": tmp}
    ip = fd.lan_ip()
    long_pw = "a-given-password-1"
    cases = [
        ("local", ["--env", good], {}),
        ("login", ["--env", good, "--login"], {}),
        ("login-given", ["--env", good, "--login"],
         {"FRONTDESK_PASSWORD": long_pw}),
        ("lan", ["--env", good, "--lan"], {}),
        ("lan-given", ["--env", good, "--lan"],
         {"FRONTDESK_PASSWORD": long_pw}),
        ("local-ignores-short", ["--env", good],
         {"FRONTDESK_PASSWORD": "short"}),
        ("lan-short", ["--env", good, "--lan"],
         {"FRONTDESK_PASSWORD": "short-11chr"}),
        ("missing", ["--env", os.path.join(tmp, "nope", "seats.env")],
         {}),
        ("no-master", ["--env", nomaster], {}),
        ("docker-no-master", ["--docker"], {}),
        ("docker-short", ["--docker"],
         {"LITELLM_MASTER_KEY": "sk-fake-d", "FRONTDESK_PASSWORD": "x"}),
        ("docker", ["--docker"], {"LITELLM_MASTER_KEY": "sk-fake-d"}),
        ("docker-given", ["--docker"],
         {"LITELLM_MASTER_KEY": "sk-fake-d",
          "FRONTDESK_PASSWORD": long_pw}),
        ("docker-bind-any", ["--docker"],
         {"LITELLM_MASTER_KEY": "sk-fake-d",
          "FRONTDESK_BIND": "0.0.0.0"}),
        ("docker-bind-localhost", ["--docker"],
         {"LITELLM_MASTER_KEY": "sk-fake-d",
          "FRONTDESK_BIND": "localhost"}),
        ("docker-bind-empty", ["--docker"],
         {"LITELLM_MASTER_KEY": "sk-fake-d", "FRONTDESK_BIND": ""}),
        ("docker-bind-v6", ["--docker"],
         {"LITELLM_MASTER_KEY": "sk-fake-d", "FRONTDESK_BIND": "::1"}),
        ("docker-bind-lan", ["--docker"],
         {"LITELLM_MASTER_KEY": "sk-fake-d",
          "FRONTDESK_BIND": "192.168.1.20"}),
        ("docker-port", ["--docker"],
         {"LITELLM_MASTER_KEY": "sk-fake-d", "FRONTDESK_PORT": "18040"}),
        ("bad-port", ["--env", good, "--port", "abc"], {}),
        ("unknown-flag", ["--env", good, "--bogus"], {}),
        ("extra-arg", ["--env", good, "extra"], {}),
        ("no-value", ["--env"], {}),
    ]
    out = []
    for name, argv, extra in cases:
        port = free_port()
        if "--port" not in argv:
            argv = argv + ["--port", str(port)]
        env = dict(base, **extra)
        o, e, code = run_desk(argv, env)
        first = e.strip().splitlines()[-1] if e.strip() else ""
        if code == 2:
            first = e  # argparse: usage and the error line
        out.append({"name": name, "argv": argv, "env": extra,
                    "stdout": mask(o, port, ip),
                    "stderr": mask(first.replace(tmp, "<tmp>"), port,
                                   ip),
                    "code": code})
    # A port in use.
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    s.listen(1)
    port = s.getsockname()[1]
    o, e, code = run_desk(["--env", good, "--port", str(port)], base)
    s.close()
    out.append({"name": "port-in-use", "argv": [], "env": {},
                "stdout": mask(o, port, ip),
                "stderr": mask(e.strip(), port, ip), "code": code})
    return out


def main():
    fd = common.load("frontdesk", "gui/frontdesk.py")
    real = fd.time.time, fd.time.monotonic, fd.secrets.token_urlsafe
    traces = [
        ("lock", trace(fd, PW, lock_ops())),
        ("fresh-after-lock", trace(fd, PW, fresh_ops())),
        ("sliding", trace(fd, PW, sliding_ops())),
        ("window", trace(fd, PW, window_ops())),
        ("clock", trace(fd, PW, clock_ops())),
        ("lock-ages", trace(fd, PW, lock_age_ops())),
        ("gap", trace(fd, PW, gap_ops())),
        ("sessions", trace(fd, PW, session_ops())),
        ("open", trace(fd, None, open_ops())),
    ]
    fd.time.time, fd.time.monotonic, fd.secrets.token_urlsafe = real
    common.write("frontdesk/gate", "gate.json", {
        "python": [common.version()],
        "traces": [dict(t, name=n) for n, t in traces],
    })
    with tempfile.TemporaryDirectory() as tmp:
        common.write("frontdesk", "start.json", {
            "python": [common.version()],
            "starts": starts(fd, tmp),
        })


if __name__ == "__main__":
    main()

"""Helpers for --lan tests: requests from a chosen loopback alias.
Prints no secrets."""
import http.client, json, socket

PORT = 4162
HOSTHDR = f"192.0.2.2:{PORT}"


def req(src, method, path, body=None, headers=None, port=None,
        raw=None, timeout=10):
    port = port or PORT
    s = socket.create_connection(("127.0.0.1", port),
                                 source_address=(src, 0),
                                 timeout=timeout)
    c = http.client.HTTPConnection("127.0.0.1", port, timeout=timeout)
    c.sock = s
    h = {"Host": f"192.0.2.2:{port}"}
    if body is not None and raw is None:
        raw = json.dumps(body)
        h["Content-Type"] = "application/json"
    h.update(headers or {})
    c.request(method, path, raw, h)
    r = c.getresponse()
    data = r.read()
    c.close()
    try:
        j = json.loads(data)
    except ValueError:
        j = None
    return r.status, j, dict(r.getheaders()), data


def login(src, pw, port=None):
    st, j, hd, _ = req(src, "POST", "/api/login", {"password": pw},
                       port=port)
    return st, j, hd


def session(src, pw, port=None):
    """Log in; return (cookie header value, csrf)."""
    st, j, hd = login(src, pw, port)
    assert st == 200, (st, j)
    ck = hd["Set-Cookie"].split(";")[0]
    return ck, j["csrf"]


def mask(t):
    t = str(t)
    return t[:3] + "..." + t[-4:] if len(t) > 12 else t

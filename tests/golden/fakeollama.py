"""Goldens for internal/fakeollama: tokens() and prompt_text() over a
corpus, and the whole HTTP answer (status, type, body) to each of a
list of requests, with the dice pinned (choice walks the words in
turn, randint gives its top, uniform its bottom) and the clock fixed.
Requests that crashed Python's handler are marked so.
python3 -B tests/golden/fakeollama.py"""
import base64
import http.client
import json
import threading
import time as real_time
from http.server import ThreadingHTTPServer

import common

fake = common.load("fake_ollama", "game/fake_ollama.py")


class Dice:
    def __init__(self):
        self.n = 0

    def choice(self, seq):
        x = seq[self.n % len(seq)]
        self.n += 1
        return x

    def randint(self, a, b):
        return b

    def uniform(self, a, b):
        return a


class Time:
    def sleep(self, s):
        pass

    def gmtime(self, *a):
        return real_time.gmtime(1700000000)

    def strftime(self, *a):
        return real_time.strftime(*a)


TEXTS = ["", "a", "ab", "abc", "abcd", "abcde", "abcdef", "x" * 10,
         "y" * 14, "z" * 18, "q" * 2, "w" * 6, "hello there",
         "héllo ✓ \U0001F600", "\n\n", "word " * 300]
MESSAGES = [None, [], [{"content": "hi"}], [{"content": None}],
            [{"role": "user"}], [{"content": ""}, {"content": "two"}],
            [{"content": 42}], [{"content": 1.5}], [{"content": True}],
            [{"content": [{"type": "text", "text": "part one"},
                          {"type": "image"}, "skip me",
                          {"text": "part two"}]}],
            [{"content": {"a": 1, "b": [2, None]}}],
            [{"content": "x" * 401}, {"content": "y"}]]

CHAT = [
    {"model": "office-pro", "messages": [{"role": "user",
                                         "content": "hello there"}]},
    {"messages": [{"content": "no model"}]},
    {"model": 5, "messages": []},
    {"model": None},
    {"model": "office-local", "stream": True,
     "messages": [{"content": "stream me " * 20}]},
    {"model": "office-local", "stream": 1, "options": {"num_predict": 3}},
    {"model": "m", "stream": "", "options": {"num_predict": 10}},
    {"model": "m", "options": {"num_predict": 1}},
    {"model": "m", "options": {"num_predict": 0}},
    {"model": "m", "options": {"num_predict": -5}},
    {"model": "m", "options": {"num_predict": 5000}},
    {"model": "m", "options": {"num_predict": 7.9}},
    {"model": "m", "options": {"num_predict": "12"}},
    {"model": "m", "options": {"num_predict": " 9 "}},
    {"model": "m", "options": {"num_predict": True}},
    {"model": "m", "options": {"num_predict": False}},
    {"model": "m", "options": {"num_predict": None}},
    {"model": "m", "options": None},
    {"model": "m", "options": {}},
    {"model": "m", "stream": True, "options": {"num_predict": 4096}},
    {"model": "m", "messages": [{"content": [{"text": "a"},
                                             {"text": "b"}]}]},
    {"model": "é✓", "messages": [{"content": "é"}]},
    {"model": {"name": "x", "tag": 1}},
    # These crashed Python's handler (no answer at all).
    {"model": "m", "options": {"num_predict": "abc"}},
    {"model": "m", "options": {"num_predict": [1]}},
    {"model": "m", "options": [1]},
    {"model": "m", "messages": "text"},
    {"model": "m", "messages": [1]},
    {"model": "m", "messages": [{"content": [{"text": 5}]}]},
    [1, 2],
]

REQUESTS = [("GET", "/", None), ("GET", "/api/version", None),
            ("GET", "/api/tags", None), ("GET", "/api/tags?x=1", None),
            ("GET", "/nope", None), ("GET", "/api/chat", None),
            ("POST", "/api/show", b"{}"), ("POST", "/api/show", b""),
            ("POST", "/api/show", b"{bad"), ("POST", "/api/show",
                                             b"\xff\xfe"),
            ("POST", "/api/show", b"[1]"), ("POST", "/nope", b"{}"),
            ("POST", "/", b"null"), ("POST", "/api/chat", b""),
            ("POST", "/api/chat", b"{\"model\": \"a\", \"model\": \"b\"}"),
            ("PUT", "/", b"{}"), ("HEAD", "/", None),
            ("DELETE", "/api/tags", None)]
REQUESTS += [("POST", "/api/chat", json.dumps(c).encode()) for c in CHAT]
# Python 3.12+ cuts a leading // to one /; json.loads of bytes skips
# a UTF-8 byte order mark; a lone surrogate comes back as its escape
# (Go answers U+FFFD: ledger P-3).
HELLO = json.dumps(CHAT[0]).encode()
REQUESTS += [("GET", "//", None), ("GET", "///api/tags", None),
             ("POST", "//api/chat", HELLO),
             ("POST", "/api/chat", b"\xef\xbb\xbf" + HELLO),
             ("POST", "/api/show", b"\xef\xbb\xbf"),
             ("POST", "/api/chat", b'{"model": "\\ud800"}')]


def ask(port, method, path, body):
    c = http.client.HTTPConnection("127.0.0.1", port, timeout=10)
    headers = {}
    if body is not None:
        headers["Content-Length"] = str(len(body))
    c.request(method, path, body=body, headers=headers)
    try:
        r = c.getresponse()
    except (http.client.RemoteDisconnected, ConnectionError):
        return {"crash": True}
    data = r.read()
    return {"status": r.status, "type": r.getheader("Content-Type"),
            "length": r.getheader("Content-Length"),
            "body": base64.b64encode(data).decode()}


def main():
    fake.random = Dice()
    fake.time = Time()
    srv = ThreadingHTTPServer(("127.0.0.1", 0), fake.Handler)
    srv.handle_error = lambda *a: None  # quiet: crashes are recorded
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    port = srv.server_address[1]
    answers = []
    for method, path, body in REQUESTS:
        a = ask(port, method, path, body)
        a.update({"method": method, "path": path,
                  "request": None if body is None
                  else base64.b64encode(body).decode()})
        answers.append(a)
    srv.shutdown()
    common.write("fakeollama", "fakeollama.json", {
        "python": common.version(),
        "tokens": [[t, fake.tokens(t)] for t in TEXTS],
        "prompt_text": [[json.dumps(m), fake.prompt_text(m)]
                        for m in MESSAGES],
        "answers": answers})


if __name__ == "__main__":
    main()

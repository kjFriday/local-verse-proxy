"""Login lockouts on --lan. usage: lock.py port pwfile"""
import http.client, json, socket, sys
port, PW = int(sys.argv[1]), open(sys.argv[2]).read().strip()
def login(src, pw):
    s = socket.create_connection(("127.0.0.1", port), source_address=(src, 0))
    c = http.client.HTTPConnection("127.0.0.1", port); c.sock = s
    c.request("POST", "/api/login", json.dumps({"password": pw}),
              {"Host": f"192.0.2.2:{port}", "Content-Type": "application/json",
               "Origin": f"http://192.0.2.2:{port}"})
    r = c.getresponse(); b = json.loads(r.read()); c.close()
    return r.status, b.get("error", "ok"), r.getheader("Retry-After")
for i in range(6):
    print("127.0.0.7 wrong", i + 1, login("127.0.0.7", "wrong-" * 3))
print("127.0.0.7 RIGHT ", login("127.0.0.7", PW))
print("127.0.0.8 RIGHT ", login("127.0.0.8", PW))
for a in ("127.0.1.1", "127.0.1.2", "127.0.1.3"):
    for i in range(5):
        st = login(a, "wrong-" * 3)
    print(a, "after 5 wrong:", st)
print("fresh 127.0.2.50 RIGHT:", login("127.0.2.50", PW))

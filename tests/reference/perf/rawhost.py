"""Raw Host header probes. usage: rawhost.py port"""
import socket, sys
port = int(sys.argv[1])
cases = {
 "empty Host 1.1": b"GET /api/session HTTP/1.1\r\nHost: \r\n\r\n",
 "no Host 1.0": b"GET /api/session HTTP/1.0\r\n\r\n",
 "no Host 1.1": b"GET /api/session HTTP/1.1\r\n\r\n",
 "two Hosts (ip, evil)": b"GET /api/session HTTP/1.1\r\nHost: 192.0.2.2:%d\r\nHost: evil.test:%d\r\n\r\n" % (port, port),
 "two Hosts (evil, ip)": b"GET /api/session HTTP/1.1\r\nHost: evil.test:%d\r\nHost: 192.0.2.2:%d\r\n\r\n" % (port, port),
 "trailing dot vm.": b"GET /api/session HTTP/1.1\r\nHost: vm.:%d\r\n\r\n" % port,
 "upper VM": b"GET /api/session HTTP/1.1\r\nHost: VM:%d\r\n\r\n" % port,
 "ip with userinfo": b"GET /api/session HTTP/1.1\r\nHost: evil.test@192.0.2.2:%d\r\n\r\n" % port,
 "absolute URI evil": b"GET http://evil.test:%d/api/session HTTP/1.1\r\nHost: 192.0.2.2:%d\r\n\r\n" % (port, port),
 "ipv4 decimal 3221225986": b"GET /api/session HTTP/1.1\r\nHost: 3221225986:%d\r\n\r\n" % port,
 "0.0.0.0": b"GET /api/session HTTP/1.1\r\nHost: 0.0.0.0:%d\r\n\r\n" % port,
 "port 0%d": b"GET /api/session HTTP/1.1\r\nHost: 192.0.2.2:0%d\r\n\r\n" % port,
 "space in host": b"GET /api/session HTTP/1.1\r\nHost: 192.0.2.2 :%d\r\n\r\n" % port,
}
for name, req in cases.items():
    s = socket.create_connection(("127.0.0.1", port)); s.settimeout(3)
    s.sendall(req)
    try:
        data = s.recv(4096)
    except socket.timeout:
        data = b"(timeout)"
    s.close()
    line = data.split(b"\r\n", 1)[0].decode(errors="replace")
    body = data.split(b"\r\n\r\n", 1)[-1][:70]
    print(f"{name:26} -> {line} {body!r}")

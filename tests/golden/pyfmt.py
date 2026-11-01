"""Goldens for internal/pyfmt: what Python prints and reads, case by
case. python3 -B tests/golden/pyfmt.py"""
import datetime
import ipaddress
import json
import os
import random
import struct
import urllib.parse

import common

SPECS = [">7.1f", ">9,", ">6,.0f", ">4.2f", "+.2f", ">+5.0%", "<36",
         "<6", "<8", ">2", ">4", ",", ".0f", "02.0f", ".1f", ".2f",
         "<14", "<9", ",.1f", ",.0f", ">3.0f", ">6.1f", ">5.0f", "<2",
         ".8f", "", "d", ">10", "^9", "*^9", "=+8", "08.3f", "010,",
         " .2f", "+d", "e", ".3e", "g", ".3g", ".2", ".6", "%", ".1%",
         "x", "#x", "_", "s", ">+9,.2f", "-", "+"]
INTS = [0, 1, -1, 7, 42, 999, 1000, 1500, 12345, -12345, 1234567,
        10**12, -(10**15), 2**53 + 1]
FLOATS = [0.0, -0.0, 0.5, 1.5, 2.5, 0.125, 0.375, 2.675, 1e-05, 0.0001,
          59.6, 59.5, 60.0, 1234.5678, -1234.5678, 1e16, 1.5e300,
          123456789.125, 0.1 + 0.2, 1 / 3, 2 / 3, -0.004, 0.005, 0.015,
          999.95, 9.995, 1500.0, 0.00001 * 1500, 12.0, 100.0, 1e22,
          float("inf"), float("-inf"), float("nan")]
STRS = ["", "a", "fern", "héllo", "日本", "a much longer piece of text",
        "game-fern"]
# NaNs as bits: the sign bit set (inf * 0 on amd64 makes that one),
# and payloads. Python never prints a NaN's sign.
NANS = ["fff8000000000000", "fff42f5dd84e6669", "7ff4000000000001"]


def from_bits(h):
    return struct.unpack("<d", struct.pack("<Q", int(h, 16)))[0]


def fmt_cases():
    out = []
    for v in INTS + FLOATS + [from_bits(h) for h in NANS] + STRS:
        for s in SPECS:
            try:
                r = format(v, s)
            except ValueError:
                continue
            kind = type(v).__name__
            enc = v if kind != "float" else float_bits(v)
            out.append([kind, enc, s, r])
    return out


def fuzz_spec(rng):
    """A random spec from the whole mini-language, often one Python
    refuses for the value it meets."""
    s = ""
    if rng.random() < 0.3:
        s += rng.choice(" *0x")
        s += rng.choice("<>=^")
    elif rng.random() < 0.4:
        s += rng.choice("<>=^")
    for flag, p in (("+- ", 0.3), ("z", 0.1), ("#", 0.15), ("0", 0.15)):
        if rng.random() < p:
            s += rng.choice(flag)
    if rng.random() < 0.6:
        s += str(rng.randint(0, 14))
    if rng.random() < 0.25:
        s += rng.choice(",_")
    if rng.random() < 0.4:
        s += "." + str(rng.randint(0, 10))
    if rng.random() < 0.8:
        s += rng.choice("bcdeEfFgGnosxX%" + "dfge%" * 2)
    return s


def fuzz_cases():
    """Random specs over every kind of value: Python's answer, or None
    where it refuses. Go prints the same, or "!(...)" for a refusal."""
    rng = random.Random("pyfmt-fuzz")
    vals = (INTS + FLOATS + [from_bits(h) for h in NANS] + STRS +
            [True, False, -0.0001, -0.004, 4679985, 685792, 65, 0x10ffff,
             -5e-7, 1e-300, 255, 0.0001, 9.5, -1.0])
    out = []
    for _ in range(4000):
        v = rng.choice(vals)
        spec = fuzz_spec(rng)
        try:
            r = format(v, spec)
        except (ValueError, OverflowError):
            r = None
        kind = type(v).__name__
        enc = float_bits(v) if kind == "float" else v
        out.append([kind, enc, spec, r])
    return out


def float_bits(f):
    return "%016x" % struct.unpack("<Q", struct.pack("<d", f))[0]


def round_cases():
    xs = FLOATS[:-3] + [0.000123456789, 1e-18, -1e-18, 12.3456785,
                        0.0000005, 0.0000015, 2.5e-7, 1234.000000499,
                        -2.5, 3.5, 1e300, 5e-324, 0.1 * 3]
    out = []
    for x in xs:
        for n in [0, 1, 2, 3, 6, 8, 15, -1, -2]:
            out.append([float_bits(x), n, float_bits(round(x, n))])
        out.append([float_bits(x), None, round(x)])
    return out


def repr_cases():
    rng = random.Random("pyfmt-repr")
    vals = list(FLOATS) + [1e15, 1e17, 123456789012345678.0,
                           1234567890123456.0, 0.001, 0.0012, 1e-4,
                           9.999999999999999e-05, 5e-324,
                           1.7976931348623157e308,
                           2.0**-1074, 1e100, 1.5e-7, 100.0]
    for _ in range(2000):
        vals.append(struct.unpack("<d", struct.pack(
            "<Q", rng.getrandbits(64)))[0])
    for _ in range(500):
        vals.append(rng.uniform(-1e6, 1e6))
        vals.append(round(rng.uniform(0, 100), rng.randint(0, 6)))
    out = []
    for v in vals:
        if v != v:
            continue
        out.append([float_bits(v), repr(v)])
    return out


JSONS = ['5', '-0', '5.0', '1e3', '1E400', '-1e400',
         '123456789012345678901234567890', '0.1', 'true', 'false',
         'null', '"x"', '"it\'s"', '[1, "a", null, true, 2.5]',
         '{"a": 1}', '[]', '{}', '-12', '1.0e-7', '0.00001', '1e16',
         '[["x"], {"k": [1.5]}]', '"tab\\there"', '"\\u00e9"']


def str_cases():
    return [[t, str(json.loads(t))] for t in JSONS]


SPACE = "\t\n\v\f\r\x1c\x1d\x1e\x1f \x85\xa0\u1680" + \
    "".join(chr(c) for c in range(0x2000, 0x200b)) + \
    "\u2028\u2029\u202f\u205f\u3000"
TEXTS = ["  a b  ", "\xa0x\xa0", "a\u2028b", "x\n", "\n", "", " ",
         "a\r\nb\rc\nd\x0be\x0cf\x1cg\x1dh\x1ei\x85j\u2029k",
         "line one\nline two\n\nline four", "\r\n\r\n", "a\x1fb",
         "İstanbul", "ΟΔΟΣ", "ΣΑΣ Σ", "Σ", "aΣ", "aΣ.", "aΣb", "ǅ", "ß",
         "ﬁ", "ÀÉÎ", "Ꭰ", "ΑΣ'", "ΑΣ'Σ", "ÁΣ", "\u200bX\u200b",
         "MiXeD CaSe 123", "tab\tsep\tvalues", "x\u3000y", "Ǆ", "Ω",
         "Ω", "K", "K", "ÅNGSTRÖM", "ΣΣ", "a.Σ", "ϴ"]


def text_cases(rng):
    pool = SPACE + "abcXYZ ΣσİiÀ.'-́\u200b日本\u2028\x85"
    texts = list(TEXTS)
    for _ in range(300):
        texts.append("".join(rng.choice(pool) for _ in range(
            rng.randint(0, 12))))
    out = []
    for t in texts:
        out.append({"s": t, "strip": t.strip(), "split": t.split(),
                    "lines": t.splitlines(), "lower": t.lower(),
                    "cut3": t[:3], "repr": repr(t)})
    return out


REPRS = ["", "plain", "it's", 'say "hi"', "both ' and \"", "back\\slash",
         "\x00\x01\x1f\x7f", "\x80\x9f\xa0\xad", "é日本", "\u200b\u200e",
         "\ue000", "͸", "\U0010ffff", "😀", "\u2028", "\t\n\r",
         "a\u3000b", "\xa0", "\U0001f600x", "ÿĀ"]


def iso_cases(rng):
    base = ["2026-11-01T00:00:00Z", "2026-10-31T21:26:43.063000Z",
            "2026-10-31T22:26:43.061192Z", "2026-10-31T22:26:43.061+00:00",
            "2026-10-31 21:28:09.797000+00:00", "2026-10-31T21:26:43",
            "2026-10-31", "20261031", "20261031T212643", "2026-W44-6",
            "2026W446", "2026-W44", "2026-10-31T21:26:43.1234567Z",
            "2026-10-31T21:26:43,5", "2026-10-31T21:26:43+05:30",
            "2026-10-31T21:26:43-08:00", "2026-10-31T21:26:43+0530",
            "2026-10-31T21:26:43+05", "2026-10-31T21:26:43.5-00:00",
            "2026-10-31T21:26", "2026-10-31T21", "2026-10-31T2126",
            "2026-10-31T212643.25", "2026-02-29T00:00:00", "2024-02-29",
            "2026-13-01", "2026-00-10", "0000-01-01", "9999-12-31T23:59:59",
            "2026-10-31T24:00:00", "2026-10-31T23:60:00", "2026-10-31T",
            "2026-10-31 ", "2026-10-31x12:00", "2026-10-31é12:00",
            "2026-10-31T12:30x+00:00", "2026-10-31T12:30:00Zx",
            "2026-10-31T12:30:00+24:00", "2026-10-31T12:30:00+23:59",
            "2020-W53-1", "2021-W53-1", "2026-W00-1", "2026-W01-0",
            "2026-W01-8", "2026-1031", "202610-31", "2026-10-31T12::30",
            "2026-10-31T12:30:", "2026-10-31T12:30:45.", "abcdefg",
            "2026-10", "", "2026-10-31T12:30:45.123456789+01:00",
            "2026-10-31T12-05", "2026-10-31T12:30+05:30:15.5"]
    alphabet = "0123456789-:T Z+.,W"
    cases = list(base)
    for _ in range(3000):
        s = list(rng.choice(base))
        for _ in range(rng.randint(1, 3)):
            op = rng.randint(0, 2)
            i = rng.randint(0, len(s))
            if op == 0 and s:
                s[min(i, len(s) - 1)] = rng.choice(alphabet)
            elif op == 1:
                s.insert(i, rng.choice(alphabet))
            elif s:
                del s[min(i, len(s) - 1)]
        cases.append("".join(s))
    cases += structured(rng)
    out = []
    seen = set()
    for s in cases:
        if s in seen:
            continue
        seen.add(s)
        try:
            t = datetime.datetime.fromisoformat(s)
        except ValueError:
            out.append([s, None, None])
            continue
        off = t.utcoffset()
        frac = off is not None and off.microseconds != 0
        out.append([s, t.isoformat(), t.tzinfo is None, frac])
    return out


def structured(rng):
    """Random strings built from the parts ISO times are made of, so
    many of them read."""
    def d2(lo, hi):
        return "%02d" % rng.randint(lo, hi)
    out = []
    for _ in range(3000):
        y = "%04d" % rng.choice([1, 999, 2024, 2026, 9999, rng.randint(
            1, 9999)])
        form = rng.randint(0, 3)
        if form == 0:
            date = y + "-" + d2(0, 13) + "-" + d2(0, 32)
        elif form == 1:
            date = y + d2(1, 12) + d2(1, 31)
        elif form == 2:
            date = y + "-W" + d2(0, 54) + rng.choice(
                ["", "-" + str(rng.randint(0, 8))])
        else:
            date = y + "W" + d2(0, 54) + rng.choice(
                ["", str(rng.randint(0, 8))])
        if rng.random() < 0.15:
            out.append(date)
            continue
        sep = rng.choice("TT t_x\u00e9")
        n = rng.randint(1, 3)
        colon = rng.random() < 0.7
        parts = [d2(0, 25), d2(0, 61), d2(0, 61)][:n]
        t = (":" if colon else "").join(parts)
        if n == 3 and rng.random() < 0.5 or rng.random() < 0.1:
            t += rng.choice(".,") + "".join(
                rng.choice("0123456789") for _ in range(rng.randint(
                    0, 9)))
        z = rng.choice(["", "", "Z", "+", "-"])
        if z in "+-" and z:
            k = rng.randint(1, 3)
            zp = [d2(0, 25), d2(0, 99), d2(0, 99)][:k]
            z += (":" if rng.random() < 0.7 else "").join(zp)
            if rng.random() < 0.1:
                z += "." + str(rng.randint(0, 9999999))
        out.append(date + sep + t + z)
    return out


HOSTS = ["127.0.0.1:8040", "localhost:8040", "[::1]:8040", "LOCALHOST",
         "Example.COM:80", "a@b:1", "user:pw@host:99", "[fe80::1%eth0]:8040",
         "[fe80::1%ETH0]", "[::1", "::1]", "[1.2.3.4]", "[vfe.x]", "[v1.x]",
         "[V1.x]:3", "[vg.x]", "host:", "host:abc", "host:+80", "host:65536",
         "host:65535", "host:0080", "host: 80", "host:٨٠", "h\tost",
         "x/y:1", "x?y", "x#y", "[::1]x:80", "a[::1]", "", ":80", "日本.jp",
         "a℀b", "a：b", "192.168.1.20:8040", "1.2.3.4.5", "[::1]:",
         "[::1]:x", "@", "a@", "@b", "[::ffff:127.0.0.1]:1", "HOST.Local:8040",
         "İX", "host:00000000000000000080", "host:99999999999999999999",
         "[::1]%25:1", "a\nb", "a\rb:1", "ÉCOLE.fr", "x:1:2", "[1::2::3]",
         "[12345::]", "[::1%]", "pc.local.", "0x7f.1:80", "127.1:80",
         "[a:b:c:d:e:f:1.2.3.4]:8", "\x00x", " x", "x "]


def host_cases():
    out = []
    for h in HOSTS:
        rec = {"h": h}
        try:
            u = urllib.parse.urlsplit("//" + h)
        except ValueError as e:
            rec["split_err"] = str(e)
            out.append(rec)
            continue
        rec["name"] = u.hostname
        try:
            rec["port"] = u.port
        except ValueError as e:
            rec["port_err"] = str(e)
        out.append(rec)
    return out


IPS = ["127.0.0.1", "127.255.255.255", "126.0.0.1", "0.0.0.0",
       "255.255.255.255", "256.0.0.1", "01.2.3.4", "1.2.3", "1.2.3.4 ",
       " 1.2.3.4", "::1", "::", "0::1", "::ffff:127.0.0.1", "::ffff:7f00:1",
       "::ffff:126.0.0.1", "1::", "1:2:3:4:5:6:7:8", "1:2:3:4:5:6:7:8:9",
       "1::2::3", "fe80::1%eth0", "fe80::1%", "::1%1", "12345::", "g::1",
       "::1.2.3.4", "1:2:3:4:5:6:1.2.3.4", "1:2:3:4:5:6:7:1.2.3.4",
       "localhost", "", "١.٢.٣.٤", "1.2.3.4/32",
       "::1/128", ":1", "1:", ":::", "1:::2", "0:0:0:0:0:0:0:1",
       "0000:0000:0000:0000:0000:0000:0000:0001", "00000::1", "::00001",
       "a:b:c:d:e:f:a:b", "A::B", "::127.0.0.1", "127.0.0.0", "127.000.0.1",
       "1.2.3.04", "0.0.0.00", "::1%eth0%1", "%eth0", "1.2.3.4%x"]


def ip_cases():
    out = []
    for s in IPS:
        try:
            a = ipaddress.ip_address(s)
            out.append([s, True, a.is_loopback])
        except ValueError:
            out.append([s, False, False])
    return out


def errno_cases():
    nums = list(range(1, 41)) + [98, 99, 100, 101, 103, 104, 110, 111,
                                 113]
    return [[n, os.strerror(n)] for n in nums]


def oserror_cases():
    out = []
    for path in ["game/rules.toml", "it's.toml", "/abs/path/x",
                 "dir with space/r.toml", 'q"uote']:
        try:
            open(os.path.join("/nonexistent-golden", path))
        except OSError as e:
            out.append([os.path.join("/nonexistent-golden", path), str(e)])
    return out


def main():
    rng = random.Random("pyfmt-golden")
    common.write("pyfmt", "pyfmt.json", {
        "python": [common.version()],
        "format": fmt_cases(),
        "format_fuzz": fuzz_cases(),
        "round": round_cases(),
        "repr": repr_cases(),
        "str": str_cases(),
        "text": text_cases(rng),
        "reprstr": [[s, repr(s)] for s in REPRS],
        "iso": iso_cases(rng),
        "hosts": host_cases(),
        "ips": ip_cases(),
        "floordiv": [[float_bits(a), float_bits(b), float_bits(a // b),
                      float_bits(a % b)]
                     for a in [125.0, -125.0, 59.6, 0.0, -0.0, 7.5, 3600.4,
                               1e-9, 5.0]
                     for b in [60.0, -60.0, 1.0, 0.5]],
        "errno_linux": errno_cases(),
        "oserror_linux": oserror_cases(),
    })


if __name__ == "__main__":
    main()

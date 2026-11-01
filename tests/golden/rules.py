"""Goldens for internal/rules: office.py's load_rules on the repo's
rules.toml and on 80-odd edited copies, with its exact messages, and
Front Desk's GameSeats reading of the seat names.
python3 -B tests/golden/rules.py"""
import json
import os
import pathlib
import re
import tempfile

import common

office = common.load("office", "game/office.py")
frontdesk = common.load("frontdesk", "gui/frontdesk.py")
BASE = open(os.path.join(common.REPO, "game", "rules.toml"),
            encoding="utf-8").read()


class Died(Exception):
    pass


def die(msg):
    raise Died(msg)


office.die = die


def sub(old, new, count=1):
    def edit(text):
        assert old in text, old
        return text.replace(old, new, count)
    return edit


def drop_line(start):
    def edit(text):
        lines = text.split("\n")
        keep = [x for x in lines if not x.startswith(start)]
        assert len(keep) < len(lines), start
        return "\n".join(keep)
    return edit


SEATS_TABLES = BASE[:BASE.index("[roster]")] + "\n".join(
    '[[roster.seats]]\nname = "%s"\npersona = "%s"\nguild = "%s"\n' % m
    for m in re.findall(r'name = "(\w+)",\s+persona = "(\w+)",\s+'
                        r'guild = "(\w+)"', BASE))

CASES = [
    ("good", lambda t: t),
    ("good-seats-as-tables", lambda t: SEATS_TABLES),
    ("good-1h", sub('length = "2m"', 'length = "1h"')),
    ("good-1d", sub('length = "2m"', 'length = "1d"')),
    ("good-5m", sub('length = "2m"', 'length = "5m"')),
    ("good-close-half", sub('close = "35s"', 'close = "60s"')),
    ("good-empty-prefix", sub('seat_prefix = "game-"', 'seat_prefix = ""')),
    ("good-seed-text", sub("seed = 42 ", 'seed = "abc"')),
    ("good-per-week-float", sub("per_week = 5", "per_week = 5.0")),
    ("good-daily-float", sub("daily = 1500", "daily = 1500.0")),
    ("good-usd-exp", sub("usd = 0.00001", "usd = 1e-5")),
    ("good-unknown-keys", sub('key_life = "8d"',
                              'key_life = "8d"\ncolour = "blue"')),
    ("good-unknown-table", lambda t: t + "\n[extra]\nx = 1\n"),
    ("good-unknown-persona-key", sub("stream = false",
                                     "stream = false\nmood = 3")),
    ("good-unknown-seat-key", sub('guild = "owls"},',
                                  'guild = "owls", hat = 1},')),
    ("length-90s", sub('length = "2m"', 'length = "90s"')),
    ("length-7m", sub('length = "2m"', 'length = "7m"')),
    ("length-5h", sub('length = "2m"', 'length = "5h"')),
    ("length-2d", sub('length = "2m"', 'length = "2d"')),
    ("length-2x", sub('length = "2m"', 'length = "2x"')),
    ("length-02m", sub('length = "2m"', 'length = "02m"')),
    ("length-0m", sub('length = "2m"', 'length = "0m"')),
    ("length-2M", sub('length = "2m"', 'length = "2M"')),
    ("length-huge", sub('length = "2m"',
                        'length = "999999999999999999999m"')),
    ("length-int", sub('length = "2m"', 'length = 120')),
    ("length-empty", sub('length = "2m"', 'length = ""')),
    ("close-61s", sub('close = "35s"', 'close = "61s"')),
    ("close-abc", sub('close = "35s"', 'close = "abc"')),
    ("close-int", sub('close = "35s"', 'close = 35')),
    ("close-huge", sub('close = "35s"', 'close = "99999999999999999999s"')),
    ("close-and-bad-seat", lambda t: sub('close = "35s"', 'close = "x"')(
        sub('name = "fern"', 'name = "Fern"')(t))),
    ("seat-upper", sub('name = "fern"', 'name = "Fern"')),
    ("seat-long", sub('name = "fern"', 'name = "fernando"')),
    ("seat-digit-first", sub('name = "fern"', 'name = "9lives"')),
    ("seat-empty", sub('name = "fern"', 'name = ""')),
    ("seat-accent", sub('name = "fern"', 'name = "fërn"')),
    ("seat-int", sub('name = "fern"', "name = 5")),
    ("seat-no-persona", sub('persona = "frugal",   guild = "owls"',
                            'persona = "nobody",   guild = "owls"')),
    ("seat-persona-int", sub('persona = "frugal",   guild = "owls"',
                             'persona = 5,   guild = "owls"')),
    ("seat-twice", sub('name = "bea"', 'name = "fern"')),
    ("seat-missing-guild", sub(',   guild = "owls"},', '},')),
    ("seat-and-prefix", lambda t: sub('seat_prefix = "game-"',
                                      'seat_prefix = "G"')(
        sub('name = "bea"', 'name = "Bea"')(t))),
    ("prefix-upper", sub('seat_prefix = "game-"', 'seat_prefix = "Game-"')),
    ("prefix-underscore", sub('seat_prefix = "game-"',
                              'seat_prefix = "game_"')),
    ("prefix-int", sub('seat_prefix = "game-"', "seat_prefix = 7")),
    ("no-day", lambda t: t.replace("[day]", "[days]")),
    ("no-close", drop_line("close = ")),
    ("no-roster", lambda t: t[:t.index("[roster]")]),
    ("no-seats", sub("seats = [", "chairs = [")),
    ("no-persona-stream", drop_line("stream = ")),
    ("daily-text", sub("daily = 1500", 'daily = "1500"')),
    ("models-text", sub('models = ["local", "pro"]', 'models = "local"')),
    ("chars-three", sub("chars = [300, 700]", "chars = [300, 700, 9]")),
    ("pause-text", sub("pause = [1.0, 2.0]", 'pause = ["1", "2"]')),
    ("individuals-text", sub("individuals = true", 'individuals = "yes"')),
    ("per-seat-float", sub("per_seat = 10", "per_seat = 10.0")),
    ("syntax-error", sub("[office]", "[office")),
    ("syntax-no-value", sub('key_life = "8d"', "key_life =")),
    ("duplicate-key", sub('key_life = "8d"',
                          'key_life = "8d"\nkey_life = "9d"')),
    # Windows Notepad's "UTF-8 with BOM": tomllib refuses it.
    ("bom", lambda t: "\ufeff" + t),
    # Counts too big to hold must still fail the close check.
    ("close-overflow-a", sub('close = "35s"', 'close = "60000000000000d"')),
    ("close-overflow-b", sub('close = "35s"', 'close = "200000000000000d"')),
    ("close-overflow-c", sub('close = "35s"', 'close = "53375995583651d"')),
    ("close-overflow-d", sub('close = "35s"',
                             'close = "999999999999999999d"')),
    ("length-overflow", sub('length = "2m"', 'length = "60000000000000d"')),
    # TOML 1.1 forms: tomllib (TOML 1.0) refuses them, BurntSushi reads
    # them (ledger O-12).
    ("toml11-inline-newline", sub('{name = "fern",', '{\n  name = "fern",')),
    ("toml11-trailing-comma", sub('guild = "owls"},', 'guild = "owls",},')),
    ("toml11-escape-e", sub('key_life = "8d"', r'key_life = "8d\e"')),
    ("toml11-escape-x", sub('key_life = "8d"', r'key_life = "8d\x41"')),
    ("toml11-time-hhmm", lambda t: t + "\n[extra]\nwhen = 07:32\n"),
    ("toml11-datetime-hhmm", lambda t: t +
     "\n[extra]\nwhen = 1979-05-27T07:32Z\n"),
    # Numbers tomllib reads and BurntSushi cannot hold (ledger O-12).
    ("big-int-unknown", lambda t: t + "\n[extra]\na = 9223372036854775808\n"),
    ("big-float-unknown", lambda t: t + "\n[extra]\na = 1e400\n"),
    # Types load_rules let through and the game played with; Go refuses
    # them at load (ledger O-1).
    ("individuals-false-text", sub("individuals = true",
                                   'individuals = "false"')),
    ("max-attempts-float", sub("max_attempts = 3", "max_attempts = 3.0")),
    ("quests-float", sub("quests = 99", "quests = 99.0")),
    ("calls-float", sub("calls = 2", "calls = 2.0")),
    ("reply-float", sub("reply = 80", "reply = 80.0")),
    ("per-seat-bool", sub("per_seat = 10", "per_seat = true")),
    ("stream-int", sub("stream = false", "stream = 0")),
]


def run_case(path):
    try:
        r = office.load_rules(path)
    except Died as e:
        return {"die": str(e)}
    except Exception as e:  # Python's crash: Go gives its own message
        return {"crash": type(e).__name__ + ": " + str(e)}
    s = {k: str(r["day"][k]) for k in ("per_week",)}
    s.update({k: str(r["mana"][k]) for k in ("usd", "daily")})
    s["seed"] = str(r["sim"]["seed"])
    try:
        s["daily_comma"] = format(r["mana"]["daily"], ",")
    except ValueError as e:
        s["daily_comma"] = "ValueError: " + str(e)
    return {"rules": json.loads(json.dumps(finite(r))), "str": s}


def finite(v):
    """v with inf and nan as text: JSON has no word for them."""
    if isinstance(v, float) and v != v or v in (float("inf"),
                                                 float("-inf")):
        return repr(v)
    if isinstance(v, dict):
        return {k: finite(x) for k, x in v.items()}
    if isinstance(v, list):
        return [finite(x) for x in v]
    return v


def seat_names(path):
    gs = frontdesk.GameSeats(pathlib.Path(os.path.dirname(path)))
    gs.rules = pathlib.Path(path)
    gs.load_rules()
    return [gs.prefix, sorted(gs.roster)]


SEAT_CASES = CASES + [
    ("prefix-missing", drop_line("seat_prefix = ")),
    ("prefix-float", sub('seat_prefix = "game-"', "seat_prefix = 1.5")),
    ("prefix-bool", sub('seat_prefix = "game-"', "seat_prefix = true")),
    ("prefix-list", sub('seat_prefix = "game-"', 'seat_prefix = ["a"]')),
    ("seats-table", lambda t: t[:t.index("[roster]")] +
     '[roster.seats]\nname = "x"\n'),
    ("seats-strings", lambda t: t[:t.index("[roster]")] +
     '[roster]\nseats = ["fern", "bea"]\n'),
    ("seat-no-name", sub('name = "fern",  ', "")),
    ("seat-name-float", sub('name = "fern"', "name = 2.5")),
    ("seats-empty-text", lambda t: t[:t.index("[roster]")] +
     '[roster]\nseats = ""\n'),
    ("seats-text", lambda t: t[:t.index("[roster]")] +
     '[roster]\nseats = "ab"\n'),
    ("seats-empty-table", lambda t: t[:t.index("[roster]")] +
     '[roster]\nseats = {}\n'),
    ("prefix-datetime", lambda t: sub('name = "fern"', "name = 1979-05-27")(
        sub('seat_prefix = "game-"',
            "seat_prefix = 1979-05-27T07:32:00-08:00")(t))),
    ("prefix-datetime-z", sub('seat_prefix = "game-"',
                              "seat_prefix = 1979-05-27T07:32:00.5Z")),
    ("prefix-datetime-plus", sub('seat_prefix = "game-"',
                                 "seat_prefix = 2026-10-31T21:26:43+05:30")),
    ("prefix-datetime-local", sub('seat_prefix = "game-"',
                                  "seat_prefix = 1979-05-27T07:32:00.999999")),
    ("prefix-date", sub('seat_prefix = "game-"', "seat_prefix = 1979-05-27")),
    ("prefix-time", sub('seat_prefix = "game-"', "seat_prefix = 07:32:00")),
    ("prefix-time-frac", sub('seat_prefix = "game-"',
                             "seat_prefix = 07:32:00.1234567")),
    ("seat-name-dates", sub('name = "fern"',
                            "name = [1979-05-27, 07:32:00, 07:32:05.25, "
                            "1979-05-27T07:32:00-08:00, "
                            "1979-05-27T07:32:00Z, "
                            "1979-05-27T07:32:00+05:30, "
                            "1979-05-27T07:32:00.000001]")),
    ("prefix-big-int", sub('seat_prefix = "game-"',
                           "seat_prefix = 99999999999999999999")),
    ("seat-dup-name", sub('name = "gia"', 'name = "fern"')),
    ("bad-utf8", None),
    ("missing-file", None),
]


def main():
    out, seats = [], []
    with tempfile.TemporaryDirectory() as tmp:
        for name, edit in SEAT_CASES:
            path = os.path.join(tmp, name, "rules.toml")
            os.makedirs(os.path.dirname(path))
            if name == "bad-utf8":
                open(path, "wb").write(BASE.encode() + b"\n# \xff\xfe\n")
            elif name != "missing-file":
                open(path, "w", encoding="utf-8").write(edit(BASE))
            rel = name + "/rules.toml"
            if any(name == c[0] for c in CASES) or name in (
                    "bad-utf8", "missing-file"):
                res = run_case(path)
                for k in ("die", "crash"):
                    if k in res:
                        res[k] = res[k].replace(tmp + "/", "<dir>/")
                out.append([rel, res])
            seats.append([rel, seat_names(path)])
        files = {}
        for name, edit in SEAT_CASES:
            if edit is not None:
                files[name] = edit(BASE)
    common.write("rules", "rules.json", {
        "python": [common.version()], "load": out, "seat_names": seats})
    # The edited files themselves, so the Go test reads the same bytes.
    folder = os.path.join(common.REPO, "internal", "rules", "testdata",
                          "golden", "files")
    os.makedirs(folder, exist_ok=True)
    for name, text in files.items():
        with open(os.path.join(folder, name + ".toml"), "w",
                  encoding="utf-8", newline="\n") as f:
            f.write(text)


if __name__ == "__main__":
    main()

"""Goldens for internal/pgread: the exact bytes Front Desk's Psql and
the game's pg() write to psql, with fixed marks, and the arguments and
folder they run it with. A fake `docker` first on PATH records them.
python3 -B tests/golden/psql.py"""
import json
import os
import stat
import sys
import tempfile

import common

FAKE = r'''#!PYTHON
import json, os, sys
log = os.environ["GOLDEN_LOG"]
n = len([f for f in os.listdir(log) if f.endswith(".json")])
json.dump({"argv": sys.argv[1:], "dir": os.getcwd()},
          open(os.path.join(log, "%d.json" % n), "w"))
out = open(os.path.join(log, "%d.stdin" % n), "wb")
answer = os.environ["GOLDEN_ANSWER"]
if "ON_ERROR_STOP=1" in sys.argv:
    out.write(sys.stdin.buffer.read())
    print(answer)
    sys.exit(0)
for line in sys.stdin.buffer:
    out.write(line)
    out.flush()
    if line.startswith(b"\\echo "):
        sys.stdout.write(answer.replace("|", "\n") + "\n")
        sys.stdout.write(line[6:].decode())
        sys.stdout.flush()
'''


def main():
    frontdesk = common.load("frontdesk", "gui/frontdesk.py")
    office = common.load("office", "game/office.py")
    with tempfile.TemporaryDirectory() as tmp:
        bindir = os.path.join(tmp, "bin")
        log = os.path.join(tmp, "log")
        work = os.path.join(tmp, "work")
        for d in (bindir, log, work):
            os.mkdir(d)
        docker = os.path.join(bindir, "docker")
        with open(docker, "w") as f:
            f.write(FAKE.replace("PYTHON", sys.executable))
        os.chmod(docker, os.stat(docker).st_mode | stat.S_IEXEC)
        os.environ["PATH"] = bindir + os.pathsep + os.environ["PATH"]
        os.environ["GOLDEN_LOG"] = log

        # Front Desk's psql: two questions on one psql, fixed marks.
        marks = iter(["0011aabb", "ccdd2233"])
        frontdesk.secrets.token_hex = lambda n: next(marks)
        os.environ["GOLDEN_ANSWER"] = '{"day": "2026-10-31",|"rows": []}'
        p = frontdesk.Psql(work)
        today = p.query(frontdesk.TODAY_SQL)
        hist = p.query(frontdesk.HISTORY_SQL)
        p.stop()
        session = read(log, 0)

        # The game's pg(): one psql per question.
        os.environ["GOLDEN_ANSWER"] = '[{"n": 3, "spend": 0.25}]'
        office.ROOT = type(office.ROOT)(work)
        sql = ('SELECT key_alias AS seat, spend FROM '
               '"LiteLLM_VerificationToken" WHERE key_alias IN '
               "('game-fern', 'it''s')")
        rows = office.pg(sql)
        once = read(log, 1)

    common.write("pgread", "psql.json", {
        "python": [common.version()],
        "session": [{"marks": ["__fd_0011aabb", "__fd_ccdd2233"],
                     "sql": [frontdesk.TODAY_SQL, frontdesk.HISTORY_SQL],
                     "answers": [today, hist],
                     "argv": session[0], "stdin": session[1]}],
        "once": [{"sql": sql, "rows": rows, "argv": once[0],
                  "stdin": once[1]}],
    })


def read(log, n):
    meta = json.load(open(os.path.join(log, "%d.json" % n)))
    stdin = open(os.path.join(log, "%d.stdin" % n), "rb").read()
    return meta["argv"], stdin.decode()


if __name__ == "__main__":
    main()

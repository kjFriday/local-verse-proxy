"""Goldens for internal/pyrand: random.Random(seed) for 200 str seeds,
each running every operation the game uses, in one fixed order on the
same generator. python3 -B tests/golden/pyrand.py"""
import random

import common


def seeds():
    out = ["42-quests-1", "42-quests-5", "42-1-fern", "42-3-spike", "",
           "x", "seed with spaces", "ünïcödé-日本", "z" * 1000, "0",
           "-1", "game-sid", "1e-05-quests-2"]
    rng = random.Random("pyrand-golden-seeds")
    pool = "abcdefghijklmnopqrstuvwxyz0123456789-_ .ÄΣ日"
    while len(out) < 200:
        out.append("".join(rng.choice(pool)
                           for _ in range(rng.randint(1, 40))))
    return out


def run(seed):
    r = random.Random(seed)
    got = []
    for _ in range(3):
        got.append(["random", repr(r.random())])
    for k in [1, 7, 31, 32, 33, 53, 64]:
        got.append(["getrandbits", k, str(r.getrandbits(k))])
    for a, b in [(0, 0), (1, 6), (300, 700), (-5, 5), (0, 2**31),
                 (3000, 8000)]:
        got.append(["randint", a, b, r.randint(a, b)])
    for a, b in [(0.05, 0.25), (1.0, 2.0), (-3.0, 3.0), (3.0, 6.0)]:
        got.append(["uniform", repr(a), repr(b), repr(r.uniform(a, b))])
    for n in [1, 3, 30, 1000, 2**20 + 7]:
        got.append(["choice", n, r.choice(range(n))])
    for w in [[2, 3, 5], [1], [0.1, 0.2, 0.7], [1, 0, 1], [5, 3, 2]]:
        idx = r.choices(range(len(w)), w)[0]
        got.append(["choices", w, idx])
    for _ in range(3):
        got.append(["random", repr(r.random())])
    # The game's mix: a day's quests and one bot's calls.
    for _ in range(20):
        got.append(["choices", [2, 3, 5], r.choices(range(3), [2, 3, 5])[0]])
    for _ in range(10):
        got.append(["random", repr(r.random())])
        got.append(["uniform", "1.0", "2.0", repr(r.uniform(1.0, 2.0))])
        got.append(["randint", 300, 700, r.randint(300, 700)])
        got.append(["choice", 29, r.choice(range(29))])
    return got


def main():
    common.write("pyrand", "pyrand.json", {
        "python": [common.version()],
        "cases": [[s, run(s)] for s in seeds()],
    })


if __name__ == "__main__":
    main()

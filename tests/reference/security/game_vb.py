import sys
sys.path.insert(0, sys.argv[0].rsplit("/", 1)[0])
import lib
lib.PORT = int(sys.argv[1])
PW = open(sys.argv[2]).read().strip()
SRC = "127.0.0.21"
ck, csrf = lib.session(SRC, PW)
H = {"Cookie": ck, "X-CSRF-Token": csrf,
     "Origin": f"http://192.0.2.2:{lib.PORT}"}
for seat in ("game-x", "GAME-x", "Game-Fern", "game-", "gamex", "my-game"):
    st, j, _, _ = lib.req(SRC, "POST", "/api/mint",
                          {"seat": seat, "models": ["local"],
                           "budget": "1", "duration": "1d"}, H)
    print(f"  {seat:10} -> {st} {(j or {}).get('field')} "
          f"{(j or {}).get('error', 'ok')[:70]!r}")

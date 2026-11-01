# lvp Handbook

One program for local-verse-proxy. It mints and revokes seat keys,
runs Front Desk (the key-card web page), and plays the office game.
It is a single file: no Python, bash, curl or jq needed.

| Command       | What it does                                  |
|---------------|-----------------------------------------------|
| `init`        | make `seats.env`, with its secrets            |
| `check`       | check the whole setup, step by step           |
| `wait`        | wait until the proxy answers                  |
| `test`        | the 3-check test, on a throwaway key          |
| `mint`        | mint a seat key, and save it (`--save FILE`)  |
| `revoke`      | revoke a seat key                             |
| `env`         | set or unset a line in Compose's `.env`       |
| `desk`        | Front Desk, the web page for key cards        |
| `game`        | the office game: practice, setup, run, board, |
|               | reset                                         |
| `mint-key`    | make or cancel one seat's key, for scripts    |
| `fake-ollama` | the practice model server (Docker only)       |

On Linux and macOS type `./lvp`; in PowerShell `.\lvp`; in cmd just
`lvp`. Keys below are shown masked as `sk-...last4`.

Contents:

- [Get it](#get-it)
- [Set up and check](#set-up-and-check)
- [Mint and revoke keys](#mint-and-revoke-keys)
- [Front Desk](#front-desk)
- [Front Desk in Docker](#front-desk-in-docker)
- [The office game](#the-office-game)
- [Windows with WSL](#windows-with-wsl)
- [Getting help](#getting-help)
- [What's not done yet](#whats-not-done-yet)

## Get it

There's no release yet, so build it from the repo folder with Go 1.26
or newer:

```sh
go build ./cmd/lvp
```

That makes `lvp` (`lvp.exe` on Windows) next to `seats.env`. Build it
in the repo's top folder, not inside `cmd/lvp`.

## Set up and check

Run these in the repo folder, the one with `compose.yaml` (the
README's Steps 3 to 6 say more):

```text
$ ./lvp init --ollama http://192.168.1.50:11434
Made seats.env from seats.env.example. Only you may open it.
Wrote new random values in LITELLM_MASTER_KEY, LITELLM_SALT_KEY,
POSTGRES_PASSWORD, DATABASE_URL. They are not shown here; they stay in seats.env
(README.md, 3a. The three secrets).
Set OLLAMA_API_BASE=http://192.168.1.50:11434.
Ollama answers there.
Next: docker compose up -d, then ./lvp wait
$ docker compose up -d
$ ./lvp wait
Waiting for the proxy at http://localhost:4000, up to 3m. A dot every 3 seconds:
.........
The proxy is up.
$ ./lvp test
Minted a test key for seat test-6a66ca: model local only, 1 dollar. It is
revoked at the end.
PASS  1. the key works: local answered.
PASS  2. the wrong model is refused: claude got 403, not available for this key.
PASS  3. a revoked key is refused: revoked, then 401.
All three checks pass: your proxy works, and the test key is gone.
```

- `init` fills only the template's placeholders, so a second run
  changes nothing. `--desk-password` adds a `FRONTDESK_PASSWORD`. It
  will not write a new database password once Postgres has started
  (`--force` does, if you must).
- `check` prints `ok`, `FAIL` (with a `Fix:` line and the README
  section) or `warn` for each step: the folder, `seats.env` (filled
  in, yours only), Docker, `docker compose ps`, the proxy and its
  master key, and Ollama.
- `wait --timeout 5m` waits longer than the 3 minutes it waits by
  default.
- `test` runs the README's 3-check test on a seat `test-` and 6
  random letters and digits, and always revokes it at the end.
- `env set FRONTDESK_BIND=0.0.0.0`, `env unset NAME`, and plain `env`
  to show `.env` (secret values hidden).

lvp reads `seats.env` from `SEATS_ENV` if that is set, else from the
folder you're in, else from the program's own folder. It never prints
the master key, the salt key or a password.

## Mint and revoke keys

A seat is one person, computer or app. Minting gives it its own key,
limited to some models and a budget, until it expires.

```text
$ ./lvp mint alice --save alice.key
Saved the key for seat alice in alice.key; only you may open it. Give it to the
seat privately (README.md, Step 7).
```

That used the defaults from `seats.env` (for example: model `local`,
$5, 30 days). Set them per key; without `--save`, it prints the key:

```text
$ ./lvp mint bob --models local,pro --budget 2 --duration 7d
sk-...QgJQ
```

Add `--budget-duration 1d` (or `1w`, `1mo`) to refill the budget on a
schedule instead of a lifetime total. To cancel:

```text
$ ./lvp revoke bob
revoked seat bob
$ ./lvp revoke --key-file alice.key
revoked
That key no longer works; alice.key may go now (delete it).
```

`lvp mint-key` is the older form, for scripts: `./lvp mint-key alice`
prints only the key, and `--revoke <key>` or `--revoke-seat <seat>`
cancel. `mint-key.sh` and the separate `mint-key` program still work
the same way; they all give the same answers.

## Front Desk

A web page to see every key card, mint new ones, cancel them, and see
what each seat spent today.

```text
$ ./lvp desk
Front Desk is open.
  On this computer: http://127.0.0.1:8040
  Anyone logged in to this computer can use it; on a shared one, add --login.
Stop it with Ctrl+C.
```

Open the address in a browser. It updates live as keys change and
spend comes in.

![Front Desk, Key cards: one card, alice, Working, spent $0.00 of
$5.00](img/desk-key-cards.png)

The Key cards tab, from a demo run with made-up keys. ("Seats connect
to" is the desk's guess at this machine's address; start Front Desk
with `--seat-url` if it is wrong.)

![Front Desk, New card: seat name, models, budget,
refill](img/desk-new-card.png)

New card: the same choices as the `mint-key` flags.

| Flag             | What it does                                   |
|------------------|------------------------------------------------|
| `--login`        | ask for a password on this computer too        |
| `--lan`          | let phones and laptops on your network in      |
|                  | (always asks for a password; plain http)       |
| `--port N`       | page port (default 8040)                       |
| `--host NAME`    | with --lan, a name browsers may use for this   |
|                  | machine besides its IP                         |
| `--seat-url URL` | the proxy address shown to seats               |
| `--env FILE`     | another secrets file instead of seats.env      |

With `--login` or `--lan` and no `FRONTDESK_PASSWORD` in `seats.env`,
the desk prints a new password once at start. Set
`FRONTDESK_PASSWORD` (12 characters or more) to keep one.

## Front Desk in Docker

On the proxy machine, run it next to the proxy instead:

```sh
docker compose --profile frontdesk up -d --build
docker compose logs frontdesk
```

```text
Front Desk is open, in Docker.
  On this computer: http://127.0.0.1:8040
  Other devices: put FRONTDESK_BIND=0.0.0.0 in .env (gui/README.md)
  Password, new at each start: ...
Stop it with: docker compose stop frontdesk
```

In Docker it always asks for a password. The first start builds the
image (a minute or two). The image holds only lvp: no shell, no
Python. It also mints from the command line:

```sh
docker compose run --rm --no-deps frontdesk mint-key laptop
```

## The office game

Bot seats, each with its own key and a small budget that refills,
work through quests on top of the proxy; two leaderboards rank them
([game/README.md](../game/README.md) has the rules). Practice mode
answers with a fake model server, so it costs nothing. Turn it on
once, from the repo folder:

```sh
./lvp game practice on
```

It adds three lines to `.env`, restarts the proxy, waits for it and
shows `docker compose ps`. `./lvp game practice off` takes exactly
those lines out again and restarts the proxy without the practice
server. If `.env` already has a `COMPOSE_FILE` line of your own,
`on` changes nothing and says how to add the game's file by hand. If
you have a `compose.override.yaml`, list it in that line too, as
`compose.yaml:compose.override.yaml:game/compose.game.yaml`: setting
`COMPOSE_FILE` stops Docker reading it on its own.

Then play a week, one command at a time:

```sh
./lvp game setup              # mint 10 game seats, make office.db
./lvp game run --days 2       # play two days (-q: summaries only)
./lvp game board              # both leaderboards
./lvp game board --day 1      # one day
./lvp game board --seat fern  # one seat, quest by quest
./lvp game reset              # end the week: revoke seats, wipe
./lvp game practice off       # back to your own models
```

Rules live in `game/rules.toml`; `--rules FILE` picks another. Game
keys go in `game/keys/`, which git ignores. `fake-ollama` runs inside
Docker for practice mode; you never start it yourself.

## Windows with WSL

When the proxy runs in Docker inside WSL, run lvp inside WSL too, next
to `seats.env`. Every block below goes into **PowerShell**; you never
need to open WSL yourself. Put your WSL user name where it says
`<you>`, and your repo's Windows folder where it says
`D:\local-verse-proxy`.

Build it (and again after every update):

```powershell
cd D:\local-verse-proxy
git pull
$env:GOOS="linux"; $env:GOARCH="amd64"
go build -o \\wsl.localhost\Ubuntu\home\<you>\local-verse-proxy\lvp `
  ./cmd/lvp
Remove-Item Env:GOOS, Env:GOARCH
wsl -d Ubuntu --cd ~/local-verse-proxy chmod +x lvp
```

The last line marks it runnable: files written into WSL from Windows
arrive without that mark.

Check, mint and cancel:

```powershell
wsl -d Ubuntu --cd ~/local-verse-proxy ./lvp check
wsl -d Ubuntu --cd ~/local-verse-proxy ./lvp mint laptop --save laptop.key
wsl -d Ubuntu --cd ~/local-verse-proxy ./lvp revoke laptop
```

Is the proxy reachable?

```powershell
wsl -d Ubuntu --cd ~/local-verse-proxy ./lvp wait --timeout 10s
```

`The proxy is up.` means yes. On some machines `127.0.0.1:4000` hangs
inside WSL while `[::1]:4000` answers
(`wsl -d Ubuntu -- curl -sS -m 5 "http://[::1]:4000/health/liveliness"`
says `"I'm alive!"`). An lvp built from commit 84ab426 on reads
`localhost` as curl does, `::1` first, then `127.0.0.1`, so
`PROXY_URL=http://localhost:4000` in `seats.env` works there as it
is. Only for `mint-key.sh`, or an older lvp, put
`PROXY_URL=http://[::1]:4000` there instead. Only the minting tools
(`mint`, `revoke`, `test`, `wait`, `check`, `mint-key`) read that
line; Front Desk in Docker ignores it.

GitHub's HTTPS clones in WSL (`git pull` above, if the repo lives
there too) need a personal access token as the password, or Git
Credential Manager; GitHub takes no account passwords.

Edit `seats.env`:

```powershell
notepad \\wsl.localhost\Ubuntu\home\<you>\local-verse-proxy\seats.env
```

## Getting help

```sh
./lvp help              # every command
./lvp help mint         # one command
./lvp desk --help
./lvp game run --help
./lvp version
```

## What's not done yet

- There's no release yet: build `lvp` from source (above) until the
  first one.
- Gate A ([tests/e2e/gate-a.md](../tests/e2e/gate-a.md)) isn't met
  yet, and nobody has signed off parity.
- The beginner helpers (`init`, `check`, `wait`, `test`, `mint`,
  `revoke`, `env`, `game practice`) are built and tested, and the
  READMEs now use them. Their commands still need a hand run on a
  real Windows PC (PowerShell) and a Mac.
- The Python tools (`gui/frontdesk.py`, `game/office.py`,
  `game/fake_ollama.py`) are the old version, removed soon (Gate C,
  after the user's go-ahead). They still work until then.

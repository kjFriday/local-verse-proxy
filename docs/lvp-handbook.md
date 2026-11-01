# lvp Handbook

One program for local-verse-proxy. It mints and revokes seat keys,
runs Front Desk (the key-card web page), and plays the office game.
It is a single file: no Python, bash, curl or jq needed.

| Command       | What it does                                  |
|---------------|-----------------------------------------------|
| `mint-key`    | make or cancel one seat's key                 |
| `desk`        | Front Desk, the web page for key cards        |
| `game`        | the office game: setup, run, board, reset     |
| `fake-ollama` | the practice model server (Docker only)       |

On Linux and macOS type `./lvp`; in PowerShell `.\lvp`; in cmd just
`lvp`. Keys below are shown masked as `sk-...last4`.

Contents:

- [Get it](#get-it)
- [Before you start](#before-you-start)
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

## Before you start

lvp talks to your proxy, so the README's Steps 1-4 come first: your
`seats.env` holds the master key, and the proxy is up
(`docker compose up -d`). lvp reads `seats.env` from `SEATS_ENV` if
that is set, else from the folder you're in, else from the program's
own folder. It never prints the master key.

## Mint and revoke keys

A seat is one person, computer or app. Minting gives it its own key,
limited to some models and a budget, until it expires.

```text
$ ./lvp mint-key alice
sk-...fX3g
```

That used the defaults from `seats.env` (for example: model `local`,
$5, 30 days). Set them per key:

```text
$ ./lvp mint-key bob --models local,pro --budget 2 --duration 7d
sk-...QgJQ
```

Add `--budget-duration 1d` (or `1w`, `1mo`) to refill the budget on a
schedule instead of a lifetime total. To cancel:

```text
$ ./lvp mint-key --revoke-seat bob
revoked seat bob
$ ./lvp mint-key --revoke sk-the-full-key
```

`mint-key.sh` and the separate `mint-key` program still work the same
way; lvp gives the same answers.

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
echo 'COMPOSE_FILE=compose.yaml:game/compose.game.yaml' >> .env
docker compose up -d --force-recreate proxy
```

If you have a `compose.override.yaml`, list it too, as
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

Mint and cancel:

```powershell
wsl -d Ubuntu --cd ~/local-verse-proxy ./lvp mint-key laptop
wsl -d Ubuntu --cd ~/local-verse-proxy ./lvp mint-key --revoke-seat laptop
```

Is the proxy reachable?

```powershell
wsl -d Ubuntu -- curl -sS -m 5 "http://[::1]:4000/health/liveliness"
```

`"I'm alive!"` means yes. On some machines `127.0.0.1:4000` hangs
inside WSL while `[::1]:4000` answers; then put
`PROXY_URL=http://[::1]:4000` in `seats.env`. Only the minting tools
read that line; Front Desk in Docker ignores it.

Edit `seats.env`:

```powershell
notepad \\wsl.localhost\Ubuntu\home\<you>\local-verse-proxy\seats.env
```

## Getting help

```sh
./lvp help              # every command
./lvp help mint-key     # one command
./lvp desk --help
./lvp game run --help
./lvp version
```

## What's not done yet

- lvp is on `main` and its tests pass on Linux, macOS and Windows,
  but there's no release yet, and Gate A
  ([tests/e2e/gate-a.md](../tests/e2e/gate-a.md)) isn't met.
- The READMEs still show the Python commands
  (`python3 game/office.py ...`, `python3 gui/frontdesk.py`). Each
  has an lvp twin: `lvp game ...` and `lvp desk`. The Python tools
  still work, and stay until the switch (Gate C).
- `lvp init`, `check`, `wait`, `test`, `mint`, `revoke` and `env`
  (beginner helpers) come in a later step and don't exist yet.

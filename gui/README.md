# Front Desk

A web page for the proxy, for people who prefer clicking to typing.
It shows every key card, makes new ones, cancels old ones, and keeps
the tally, live. It works in any browser: a phone, a tablet, or a
Windows, Mac or Linux computer. Nothing to install on those.

Front Desk is one small Python program. It runs on the proxy
machine, next to `seats.env`, and serves one page. The easiest way to
run it is in Docker, like the proxy: then the proxy machine needs
nothing else, not even Python.

## Contents

- [Run it in Docker](#run-it-in-docker), the easiest way
- [Or run it with Python](#or-run-it-with-python)
- [What each screen does](#what-each-screen-does)
- [Safety notes](#safety-notes)
- [Troubleshooting](#troubleshooting)
- [Under the hood](#under-the-hood)

## Run it in Docker

You need the proxy running first
([main README](../README.md#step-1-check-your-tools), Steps 1 to 4).
Then, in the repo folder:

```sh
docker compose --profile frontdesk up -d
docker compose logs frontdesk
```

A **profile** is a named group of optional containers in
`compose.yaml`. `--profile frontdesk` adds Front Desk to the proxy
and Postgres; without it, `docker compose` leaves Front Desk out. The
first time, Docker builds Front Desk's **image** (a ready-made
program bundle) from `gui/Dockerfile`. That takes a minute or two
and downloads about 130 MB, mostly the tools to build it with.

**Success:** the log ends with `Front Desk is open, in Docker.`, an
address, a password and `Stop it with: ...`. Open
`http://127.0.0.1:8040` in a browser on the proxy machine and type
the password. You stay logged in for 12 hours, on that device. In
Docker, Front Desk always asks for a password: behind Docker, it
cannot tell the proxy machine from a phone.

Like the proxy, Front Desk comes back by itself after a reboot.
`docker compose stop frontdesk` stops it; the proxy keeps running.

### Pick your own password

The password Front Desk makes is new each time it starts. To keep
one, put it in `seats.env`. Run this once, in the repo folder:

```sh
printf '\nFRONTDESK_PASSWORD=%s\n' "$(openssl rand -hex 12)" >> seats.env
grep FRONTDESK_PASSWORD seats.env
docker compose --profile frontdesk up -d
```

The first line adds a random password of 24 letters and digits. The
second shows it. The third starts Front Desk again with it. Your own
password works too: 12 characters or more, and random, not words or
names. Use only letters and digits, as the first line does, or put
the whole password in single quotes (`'like this'`, with no `'`
inside). Other characters, such as `$`, `#`, a space or a quote, can
cut the password short for Docker, or stop `mint-key.sh` reading
`seats.env` at all.

The `up` line restarts the proxy and its database as well, as after
any `seats.env` edit, so seats get errors for about 30 seconds. To
restart only Front Desk, add `--no-deps frontdesk` to that line.

### Let phones in

At first, only the proxy machine can open the page. To let phones
and laptops on your network in, put one line in a file named `.env`,
next to `compose.yaml`, then start Front Desk again:

```sh
echo "FRONTDESK_BIND=0.0.0.0" >> .env
docker compose --profile frontdesk up -d
```

`.env` is not `seats.env`: Docker Compose itself reads `.env`, to
fill in `compose.yaml`. Only Front Desk restarts.

Then on the phone, join the same Wi-Fi and open
`http://<proxy-ip>:8040`, with the proxy machine's address
([find it](../README.md#find-the-proxy-address)). The page warns
that it is plain http. Read the [safety notes](#safety-notes) first.

### More settings

All optional. Put them in `.env`, one per line, then run
`docker compose --profile frontdesk up -d`:

- `FRONTDESK_BIND=0.0.0.0`: let other devices in. The default,
  `127.0.0.1`, means this computer only.
- `FRONTDESK_PORT=8041`: another port for the page. The default is
  8040.
- `FRONTDESK_HOSTS=office-pc,office-pc.local`: names that browsers
  may use for this machine, besides its IP address. Split them with
  commas, and no spaces.
- `FRONTDESK_SEAT_URL=http://192.168.1.20:4000`: the base URL shown
  to seats. Front Desk guesses it from the address you opened the
  page by, which is wrong if that was `127.0.0.1`.
- `COMPOSE_PROFILES=frontdesk`: start Front Desk with every plain
  `docker compose up -d`, so you never type `--profile frontdesk`.

### Mint keys through Docker

The same image holds the Go `mint-key`
([the Go version](../README.md#the-go-version)), so you can mint and
revoke in the terminal with nothing else installed. It takes the
same options as `./mint-key.sh`:

```sh
key=$(docker compose run --rm --no-deps frontdesk mint-key laptop) \
  && echo "$key" > laptop.key
chmod 600 laptop.key
docker compose run --rm --no-deps frontdesk mint-key --revoke-seat laptop
```

`run --rm` starts a new Front Desk container for that one command,
then removes it. `--no-deps` leaves the proxy and its database as
they are, so start them first (`docker compose up -d`); without it,
Compose may restart both if `seats.env` changed. Lines like
`Container ... Running` come from Docker, not from `mint-key`. With
Front Desk running, `docker compose exec frontdesk mint-key ...` does
the same in it.

A message that names `/app/seats.env` means your `seats.env`, next
to `compose.yaml`: Compose passes its settings in, and
`/app/seats.env` is an empty stand-in. Where the usage text says to
type `./mint-key`, type
`docker compose run --rm --no-deps frontdesk mint-key` here.

### Day to day in Docker

```sh
docker compose logs frontdesk          # its messages, and the password
docker compose restart frontdesk       # start again; all log in again
docker compose stop frontdesk          # stop it; the proxy keeps going
docker compose --profile frontdesk down            # stop it all
docker compose --profile frontdesk up -d --build   # after a git pull
```

A plain `docker compose down` stops the proxy but leaves Front Desk
running, and says the network is `still in use`: add
`--profile frontdesk`, as above. Or put `COMPOSE_PROFILES=frontdesk`
in `.env` once, and every `docker compose` line includes Front Desk.

The last line rebuilds the image, so a new Front Desk takes effect.
Compose reads `seats.env` and `.env` when it makes Front Desk's
container, not when it restarts it. After editing either, run
`docker compose --profile frontdesk up -d`: it makes the container
anew. Until then, Front Desk keeps the old settings, master key
included.

Front Desk makes a new password each time it starts, and the log
keeps every one. Only the last `Password` line works. After a
restart, on Linux or macOS,
`docker compose logs frontdesk | grep Password | tail -1` shows it;
elsewhere, run `docker compose logs frontdesk` and take the last
`Password` line.

## Or run it with Python

You need the proxy running first
([main README](../README.md#step-1-check-your-tools), Steps 1 to 4),
and Python 3.11 or newer on the proxy machine. Check with
`python3 --version`. This way, Front Desk runs `mint-key.sh`, so it
also needs bash, curl and jq (main README, Step 1). Front Desk in
Docker needs none of them. Then, in the repo folder:

```sh
python3 gui/frontdesk.py
```

**Success:** it prints `Front Desk is open.` and an address. Open
`http://127.0.0.1:8040` in a browser on the proxy machine. Leave the
terminal open; Ctrl+C stops Front Desk. The proxy keeps running.

On its own, Front Desk listens only to the proxy machine itself, so
nobody else on your network can open it. Anyone logged in to the
proxy machine can, though: on a computer other people log in to,
add `--login` to ask for a password there too.

### Open it from a phone

```sh
python3 gui/frontdesk.py --lan
```

It prints three more lines: the address for your network, like
`http://192.168.1.20:8040`, a reminder that the page is plain http,
and a password, shown once. On the phone, join the same Wi-Fi, open
that address, and type the password. You stay logged in for 12
hours, on that device.

With `--lan`, every device needs the password, the proxy machine
included.

To pick your own password instead, set it for that one run (12
characters or more, and random: not words or names, since a device
on your network can try about one a second):

```sh
FRONTDESK_PASSWORD='<your-password>' python3 gui/frontdesk.py --lan
```

Open it by the machine's IP address. To use a name instead, like
`http://office-pc:8040`, add `--host office-pc`. Other options:
`--port 8041` for another port, and `--help` for the list.

## What each screen does

### Key cards

Every card, with its seat name, models, budget and expiry. A bar
shows how much of the budget is spent; it turns amber at 80% and red
when the budget is used up. Each card says if it is **Working**,
**Expired**, **Blocked**, has its **Budget used up**, or is
**Waiting for refill**, and what to do about it.

**Budgets that refill.** Most budgets are a lifetime total. A card
made with a refill (see New card), or with `--budget-duration` in
the terminal, gets its budget back on a schedule, such as every day.
Its card says how often, and when the next refill is, in your own
time: `Refills every day, next at 1:00 AM (in 3 hours)`. The bar and
the amount spent count only since the last refill. When such a card
uses up its budget, it shows **Waiting for refill**. It works again
by itself soon after the time shown: the proxy hands out refills
about every 10 minutes, so one can come up to 10 minutes late. Its
first round comes up to 15 minutes after the proxy starts, so just
after a restart it can be later still. There is nothing to do, so
don't cancel it.

**Office game seats.** Cards the office game made (see
[game/README.md](../game/README.md)) say **Office game seat**. Front
Desk asks the game which seats are its own. Their budget is the game's
daily mana, so they refill often and are often waiting. In practice
mode the proxy refills them within seconds of the time shown, not
every 10 minutes. To remove them, run `python3 game/office.py reset`
on the proxy machine, when the game's week is over. Cancel card still
works on them, with the same "are you sure?" step, but the game
expects its seats until the reset.

At the top, **Seats connect to** shows the base URL every seat's app
uses, with a Copy button. It is a guess at the proxy machine's
address; if it is wrong, start Front Desk with
`--seat-url http://<proxy-ip>:4000`. In Docker, put
`FRONTDESK_SEAT_URL=http://<proxy-ip>:4000` in `.env` instead.

**Cancel card** asks first, then cancels it at once, using
`./mint-key.sh --revoke-seat` (in Docker, the Go `mint-key`, which
works the same). A note takes the card's place for a
few seconds. Apps using that card stop working; all other cards
carry on. An expired card still holds its seat name, so cancel it to
use the name again.

### New card

Pick a seat name, the models it may use, a budget, a refill and how
long it lasts, then **Make key card**. The defaults come from the
`DEFAULT_` lines in `seats.env`. Front Desk runs `./mint-key.sh` (in
Docker, `mint-key`) for you, so the rules are the same as in the
terminal. A problem shows
right under its field. `Sam` and `sam` count as the same seat. Names
that start with `game-` are kept for the office game.

**Refill** says if the budget comes back:

- **Never** (the default): the budget is a lifetime total. To top
  up, cancel the card and make a new one.
- **Every day**, **Every week** or **Every month**: the budget comes
  back in full, so it is an allowance per day, week or month. The
  proxy's clock is UTC (world time, the same everywhere; each card
  shows its refill in your own time). Refills come soon after
  midnight UTC, on Mondays, or on the 1st of the month. This is
  `./mint-key.sh --budget-duration` with `1d`, `1w` or `1mo`.

The new key appears **once**, with the three things a seat needs:
the key, the base URL and the model name. Use **Copy key** or
**Save as file**, then send them to the seat privately. Close the
box and the key is gone from the page.

### Tally

What each seat and each model used, today and all time: money spent,
requests and tokens (the word pieces models charge by). Cancelled
cards stay in the tally. **Refused** marks requests the proxy turned
down, such as a model the card may not use. When everything used was
free (your own Ollama), the bars show tokens instead of dollars.

"Today" starts at midnight UTC, and the proxy saves the tally in
batches, so new use can take a minute to show.

The status line at the top says if the proxy is answering, and its
LiteLLM version. Every screen updates by itself; no need to reload.

## Safety notes

- **The master key never leaves the proxy machine.** Front Desk reads
  it from `seats.env` and uses it there. Pages get seat names,
  budgets and the tally, never the master key, and never a seat's
  key after the one time it is shown. Error messages from
  `mint-key.sh` are cleaned of anything that looks like a key.
- **Plain http.** With `--lan`, or in Docker with
  `FRONTDESK_BIND=0.0.0.0`, the page and each new key cross your
  network unencrypted. Let other devices in only on a network you
  trust, and never forward port 8040 from your router. The page
  shows a warning as a reminder. On Linux, ports that Docker opens
  may bypass firewall rules such as ufw's.
- **The password is the lock.** Anyone with it can make and cancel
  cards. A wrong password says how many tries are left: five from
  one device lock that device out for 10 minutes. Devices that typed
  a wrong password get one try a second between them all, far too
  few to guess the printed password. A device with no wrong tries is
  never held up by another's guessing. Restart Front Desk for a new
  password; that also logs everyone out.
- **No device can hog the page.** With `--lan`, and always in Docker,
  one device may hold at most 32 connections at once, so the others
  still get in.
- **A login works only on the device that made it.** Its cookie is
  refused from any other address.
- **In Docker, devices can share one address.** Front Desk sees each
  device by its address, but a browser on the proxy machine itself
  reaches it from Docker's own address (on a Mac or Windows, with
  Docker Desktop, every device may). Devices that share one count as
  one: five wrong passwords from any of them lock them all out for
  10 minutes, and they share the 32 connections. Phones on your
  network keep their own address on Linux.
- **Only its own addresses.** Front Desk answers only to the machine's
  IP address, its own name and names given with `--host` (in Docker:
  `localhost`, IP addresses and the names in `FRONTDESK_HOSTS`). That
  stops other websites from borrowing it through a trick called DNS
  rebinding.
- **Keep the proxy machine tidy.** While other devices may reach
  Front Desk, don't run other web services you don't trust on the
  proxy machine: browsers share cookies between ports of one machine.
- **Read-only tally.** Front Desk only reads the tally: one fixed
  question at a time, in a read-only session. That guards against
  mistakes, not against someone who breaks in to Front Desk: in
  Docker, it holds the database owner's password, from `seats.env`.
- **Nothing secret is logged.** The terminal shows only events like
  `minted a key card for seat sam`. In Docker, so does
  `docker compose logs frontdesk`, plus the password Front Desk made,
  if it made one.
- **A locked-down container.** In Docker, Front Desk runs as a user
  with no special powers, cannot change its own files, and cannot
  reach Docker itself. It sees the `game/` folder read-only (not the
  game's keys) and never sees `seats.env` as a file. But Compose
  passes it every setting in `seats.env`, as it does for the proxy:
  the master key, the database password and your provider keys. So
  guard it like `seats.env` itself. Anyone who may use Docker on the
  proxy machine can read them, as they can read `seats.env`.

## Troubleshooting

- **`python3: command not found`**, or `needs Python 3.11 or newer`:
  on Ubuntu 22.04, `sudo apt install python3.11`, then run
  `python3.11 gui/frontdesk.py`. On a Mac, `brew install python`.
- **`missing .../seats.env`**: run it from the repo folder, after
  [Step 3](../README.md#step-3-make-your-secrets-file) of the main
  README.
- **`cannot use port 8040`**: something else uses it. Use the port it
  suggests, like `--port 8041`.
- **The phone can't reach the page**: check that you used `--lan`,
  the phone is on the same Wi-Fi, and no firewall on the proxy
  machine blocks port 8040. On WSL, phones reach WSL programs only
  with WSL's mirrored networking turned on.
- **"Open Front Desk by this computer's address"**: you used a name
  it does not know. Use the IP address it printed, or start it with
  `--host <that-name>`.
- **"Reconnecting" at the top**: Front Desk itself stopped. Start it
  again in the terminal, or in Docker with
  `docker compose --profile frontdesk up -d`; the open page picks up
  by itself, no reload.
- **"Not answering" at the top**: the proxy is down or still
  starting. Run `docker compose ps`, and see
  [Troubleshooting: the proxy](../README.md#troubleshooting-the-proxy)
  in the main README. The page recovers by itself.
- **"The proxy did not accept the master key"**: `seats.env` changed
  after the proxy started. Run
  `docker compose up -d --force-recreate proxy`.
- **In Docker, "Front Desk and the proxy have different master
  keys"**: `seats.env` changed after one of them started. Run
  `docker compose --profile frontdesk up -d`: it makes anew whichever
  is out of date.
- **"The proxy took too long to answer"**: it is busy, or its
  database is slow. It may have done the job anyway. A new card that
  shows up after this never had its key shown: cancel it and make it
  again.
- **The tally says it shows the proxy's own numbers**: Front Desk
  could not run `docker compose exec`, usually because your user may
  not use Docker. Today's numbers still show. All time shows only
  what the proxy still knows: each live card's spend (since its last
  refill, if it refills), and for everything else only today's use.
  Full history needs Docker access (main README,
  [Troubleshooting: setting up](../README.md#troubleshooting-setting-up)).
  In Docker, it could not reach Postgres at `DATABASE_URL`: check
  that `docker compose ps` shows `db` healthy. It tries again by
  itself after 3 seconds, then every 30 seconds.
- **"Too many wrong passwords"**: wait the minutes it says (10 at
  most), or restart Front Desk.
- **"Too many tries just now"**: this device typed a wrong password
  lately, and one was typed less than a second ago, here or on
  another device. Wait a second and try again. If it keeps
  happening, a device on your network is
  guessing passwords: stop Front Desk, and use it without `--lan`
  until you find that device.
- **In Docker, `docker compose logs frontdesk` says
  `FRONTDESK_PASSWORD must be 12 characters or more`**, and Front
  Desk keeps restarting: make the password in `seats.env` longer, or
  delete that line, then run
  `docker compose --profile frontdesk up -d`.
- **In Docker, the browser says it cannot connect**: run
  `docker compose ps`. No `frontdesk` line? Start it with
  `docker compose --profile frontdesk up -d`. From a phone, check
  that `.env` has `FRONTDESK_BIND=0.0.0.0` and that you ran the `up`
  line after.
- **In Docker, "Open Front Desk by this computer's IP address"**: add
  the name you typed to `FRONTDESK_HOSTS` in `.env`, then run the
  `up` line again.
- **`port is already allocated`** or **`address already in use`**,
  naming port 8040: something else uses it, perhaps Front Desk
  started with Python. Stop that, or put `FRONTDESK_PORT=8041` in
  `.env`.
- **`Failed to connect to the proxy at http://proxy:4000`** when
  minting through Docker: the proxy is starting, perhaps restarted
  by a `seats.env` edit. Wait 30 seconds and try again. If it says
  `cannot find the computer named proxy`, the proxy is not running:
  run `docker compose up -d`, wait 30 seconds, then mint again.
- **Copy key says "Press and hold the key to copy"**: over plain http
  this browser would not copy for the page. The key is selected:
  press and hold it (or right-click) and copy, or use Save as file.

## Under the hood

For the curious. All of it was measured on the test setup: 200
seats, a year of made-up history, and chat traffic running.

- **One check for everyone.** A single helper asks the proxy what
  changed, but only while a page is open. Every page shares that one
  answer.
- **Live, but light.** When the answer changes, each open page gets a
  one-line nudge (Server-Sent Events) and fetches the new answer,
  zipped once for everyone. Under traffic that is about 6 MB an hour
  per open page, down from 45 MB when the whole answer was pushed
  each time. An idle page costs a few bytes every 15 s.
- **Key cards every 10 seconds, your changes at once.** The card
  list is the heaviest question for the proxy, so it is asked every
  10 s. A card made or cancelled in Front Desk shows at once; one
  made in the terminal shows within 10 s.
- **The tally is one small query.** Today's numbers are asked every
  5 s, in about 6 ms; the days before only every 10 minutes. Asking
  for all history every time took about 150 ms of database work.
- **psql is started once, not per question.** Starting
  `docker compose exec` costs about 150 ms each time; a psql kept
  open answers in a few ms. In Docker, Front Desk asks Postgres
  directly instead, over one connection it keeps open, with psycopg
  (a Python package for Postgres). The questions and the answers are
  the same.
- **Quick on phones.** Only the cards that changed are redrawn, and
  cards off screen are skipped: a busy update takes about 50 ms on a
  slow phone, down from about 220 ms. Each minute only the times on
  the cards, like "in 3 minutes", are set again.
- **Small, compressed, cached.** The page, script and style are
  zipped once at start, about 18 KB in all, and the browser keeps
  them until they change.
- **Standard library only.** Run with Python, it needs no packages;
  it starts in about 0.1 s and uses about 30 MB of memory.
- **A small image.** The Docker image is Python's own slim image
  (179 MB) plus psycopg (23 MB), the Go `mint-key` (6 MB), the parts
  of Python Front Desk uses, compiled ahead (6 MB), and Front Desk:
  221 MB in all, 53 MB compressed. Docker builds it in stages, so the
  tools it is built with stay out. Compiled ahead, it starts in about
  0.1 s, as with Python; without, each start took half a second more.

Files: `frontdesk.py` is the program; `static/` holds the page.
`Dockerfile` builds the Docker image, with the exact package
versions in `requirements-docker.txt`.

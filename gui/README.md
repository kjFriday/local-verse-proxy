# Front Desk

A web page for the proxy, for people who prefer clicking to typing.
It shows every key card, makes new ones, cancels old ones, and keeps
the tally, live. It works in any browser: a phone, a tablet, or a
Windows, Mac or Linux computer. Nothing to install on those.

Front Desk is one small Python program. It runs on the proxy
machine, next to `seats.env`, and serves one page.

## Contents

- [Start it](#start-it)
- [Open it from a phone](#open-it-from-a-phone)
- [What each screen does](#what-each-screen-does)
- [Safety notes](#safety-notes)
- [Troubleshooting](#troubleshooting)
- [Under the hood](#under-the-hood)

## Start it

You need the proxy running first
([main README](../README.md#step-1-check-your-tools), Steps 1 to 4),
and Python 3.11 or newer on the proxy machine. Check with
`python3 --version`. Then, in the repo folder:

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

## Open it from a phone

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
`--seat-url http://<proxy-ip>:4000`.

**Cancel card** asks first, then cancels it at once, using
`./mint-key.sh --revoke-seat`. A note takes the card's place for a
few seconds. Apps using that card stop working; all other cards
carry on. An expired card still holds its seat name, so cancel it to
use the name again.

### New card

Pick a seat name, the models it may use, a budget, a refill and how
long it lasts, then **Make key card**. The defaults come from the
`DEFAULT_` lines in `seats.env`. Front Desk runs `./mint-key.sh` for
you, so the rules are the same as in the terminal. A problem shows
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
- **Plain http.** With `--lan`, the page and each new key cross your
  network unencrypted. Use `--lan` only on a network you trust, and
  never forward port 8040 from your router. The page shows a warning
  as a reminder.
- **The password is the lock.** Anyone with it can make and cancel
  cards. A wrong password says how many tries are left: five from
  one device lock that device out for 10 minutes. Devices that typed
  a wrong password get one try a second between them all, far too
  few to guess the printed password. A device with no wrong tries is
  never held up by another's guessing. Restart Front Desk for a new
  password; that also logs everyone out.
- **No device can hog the page.** With `--lan`, one device may hold
  at most 32 connections at once, so the others still get in.
- **A login works only on the device that made it.** Its cookie is
  refused from any other address.
- **Only its own addresses.** Front Desk answers only to the machine's
  IP address, its own name and names given with `--host`. That stops
  other websites from borrowing it through a trick called DNS
  rebinding.
- **Keep the proxy machine tidy.** While `--lan` is on, don't run
  other web services you don't trust on the proxy machine: browsers
  share cookies between ports of one machine.
- **Read-only tally.** The tally comes from the proxy's database in a
  read-only session: Front Desk cannot change it.
- **Nothing secret is logged.** The terminal shows only events like
  `minted a key card for seat sam`.

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
  again in the terminal; the open page picks up by itself, no reload.
- **"Not answering" at the top**: the proxy is down or still
  starting. Run `docker compose ps`, and see
  [Troubleshooting: the proxy](../README.md#troubleshooting-the-proxy)
  in the main README. The page recovers by itself.
- **"The proxy did not accept the master key"**: `seats.env` changed
  after the proxy started. Run
  `docker compose up -d --force-recreate proxy`.
- **The tally says it shows the proxy's own numbers**: Front Desk
  could not run `docker compose exec`, usually because your user may
  not use Docker. Today's numbers still show. All time shows only
  what the proxy still knows: each live card's spend (since its last
  refill, if it refills), and for everything else only today's use.
  Full history needs Docker access (main README,
  [Troubleshooting: setting up](../README.md#troubleshooting-setting-up)).
- **"Too many wrong passwords"**: wait the minutes it says (10 at
  most), or restart Front Desk.
- **"Too many tries just now"**: this device typed a wrong password
  lately, and one was typed less than a second ago, here or on
  another device. Wait a second and try again. If it keeps
  happening, a device on your network is
  guessing passwords: stop Front Desk, and use it without `--lan`
  until you find that device.
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
  open answers in a few ms.
- **Quick on phones.** Only the cards that changed are redrawn, and
  cards off screen are skipped: a busy update takes about 50 ms on a
  slow phone, down from about 220 ms. Each minute only the times on
  the cards, like "in 3 minutes", are set again.
- **Small, compressed, cached.** The page, script and style are
  zipped once at start, about 18 KB in all, and the browser keeps
  them until they change.
- **Standard library only.** No packages to install; it starts in
  about 0.1 s and uses about 30 MB of memory.

Files: `frontdesk.py` is the program; `static/` holds the page.

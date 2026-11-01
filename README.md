# local-verse-proxy

One computer on your network holds the real keys to your AI
services. Everyone else gets their own limited pass.

## In plain terms

Say you pay for an AI service and want your household or office to
use it. Usually everyone shares one password, so when a laptop goes
missing, everybody has to switch to a new one.

This repo puts one computer in the middle, like the front desk of an
office building:

- **The real password stays at the desk.** It lives on that one
  machine, and nobody else ever sees it.
- **Everyone gets their own key card**, with limits. For example:
  Sam may use the cheap AI, up to 5 dollars, for 30 days.
- **A laptop goes missing?** Cancel that one card. Everyone else
  carries on.
- **The desk keeps a tally** of who used what, and what it cost.

`lvp mint` prints the key cards. `lvp` is this repo's one program: a
single file for Windows, macOS or Linux that needs nothing else
installed. It also sets things up for you, checks them, and runs
Front Desk, a web page for key cards. The steps below set everything
up one at a time, and say what you should see after each one.

Under the hood, the desk is a LiteLLM **proxy** (a middleman for AI
requests) running on your local network. The desk's own
**master key** makes and cancels cards, so it never leaves the desk.

> **Status: early, but tested.** This setup has run end to end
> against the real LiteLLM v1.106.6 and Postgres, with a stand-in
> Ollama, and passed the [3-check test](#step-6-the-3-check-test).
> Run that test on your own machine to confirm. If something behaves
> differently from this guide, trust what you see.

Text in angle brackets, like `<proxy-ip>`, is a placeholder. Replace
all of it, brackets too, with your own value: `http://<proxy-ip>:4000`
becomes `http://192.168.1.20:4000`. New words are explained where
they first appear, and in the [glossary](#glossary).

**How to type `lvp`.** This guide writes `./lvp`, as on Linux and
macOS. In Windows PowerShell, type `.\lvp` instead; in Windows cmd,
just `lvp`. Everything after it stays the same.

## Contents

- [In plain terms](#in-plain-terms): what this does
- [Quick start](#quick-start), for people who know Docker
- [One lock, many keys](#one-lock-many-keys): how it works
- Setup, step by step:
  1. [Check your tools](#step-1-check-your-tools)
  2. [Get the files](#step-2-get-the-files)
  3. [Make your secrets file](#step-3-make-your-secrets-file)
  4. [Start the proxy](#step-4-start-the-proxy)
  5. [Mint your first key](#step-5-mint-your-first-key)
  6. [Run the 3-check test](#step-6-the-3-check-test)
  7. [Give the key to a seat](#step-7-give-the-key-to-a-seat)
- Using it: [minting more keys](#minting-more-keys),
  [revoking](#revoking-a-key), [adding a model](#adding-a-model),
  [day-to-day commands](#day-to-day-commands),
  [upgrading](#upgrading), [safety rules](#safety-rules),
  [about `lvp mint-key`](#about-lvp-mint-key)
- Troubleshooting: [setting up](#troubleshooting-setting-up),
  [Windows and WSL](#troubleshooting-windows-and-wsl),
  [minting](#troubleshooting-minting),
  [the proxy](#troubleshooting-the-proxy),
  [seats](#troubleshooting-seats)
- [FAQ](#faq), [file map](#file-map), [glossary](#glossary),
  [license](#license)

## Quick start

On the proxy machine, in the folder you unpacked (or built `lvp` in,
see [Step 2](#step-2-get-the-files)):

```sh
./lvp init --ollama http://<ollama-ip>:11434
docker compose up -d
./lvp wait
./lvp mint laptop --save laptop.key
./lvp test
```

The same in Windows PowerShell:

```powershell
.\lvp init --ollama http://<ollama-ip>:11434
docker compose up -d
.\lvp wait
.\lvp mint laptop --save laptop.key
.\lvp test
```

`init` makes `seats.env` with its secrets; `wait` waits until the
proxy answers (the first start takes a minute or two); `mint` saves
the laptop's key in `laptop.key`; `test` runs the
[3-check test](#step-6-the-3-check-test) and prints three `PASS`
lines. Something wrong? `./lvp check` goes through it all, step by
step, and says what to fix.

**Prefer clicking?** Once the proxy is up, `./lvp desk` opens Front
Desk, a web page at `http://127.0.0.1:8040` that makes and cancels
key cards and shows the tally. Or run it in Docker, like the proxy,
with `docker compose --profile frontdesk up -d`; then
`docker compose logs frontdesk` shows its password. Phones can use it
too. See [gui/README.md](gui/README.md).

## One lock, many keys

| In the building | In this repo                            |
|-----------------|-----------------------------------------|
| The front desk  | The proxy, on port 4000                 |
| Master key      | `LITELLM_MASTER_KEY` in `seats.env`     |
| The locked safe | Real provider keys, also in `seats.env` |
| A key card      | A virtual key you mint for one seat     |
| Doors it opens  | Models it may use (`local`, `claude`)   |
| Card runs out   | It spends its budget or expires         |
| Cancel a card   | `./lvp revoke <seat>`                   |

A **seat** is one person, computer or app. Its **virtual key** is an
**API key** (a secret string that lets a program use a service) that
works only through the proxy, within its limits. To **mint** a key is
to create one; to **revoke** it is to cancel it.

### How the pieces fit

```text
  laptop      desk       app
  key A       key B      key C
     \          |          /
      v         v         v
   +----------------------+
   |  proxy on port 4000  | <-- lvp mint
   |  checks every key    |     (uses the
   +----------------------+      master key)
      |       |        |
      |       |        +--> Postgres
      v       v             (keys, budgets,
    local   claude           spending)
  (Ollama) (Anthropic)
```

On each request the proxy checks the key: real, not expired, allowed
this model, and under its **budget** (the most it may spend, in US
dollars)? If so, it calls the real model with the real API key and
adds the cost to the key's spending in **Postgres**, its database.
If not, the app gets an error. Lose a laptop? Revoke its key; nobody
else has to change anything. `local` and `claude` are **model
aliases**: short names that `config.yaml` points at real models.

## Step 1: Check your tools

Pick one computer as the **proxy machine**: always on, on the same
network as your seats, running Windows, macOS or Linux. Open a
**terminal** (the window where you type commands) there:

- **Windows:** open the folder in File Explorer, click the address
  bar, type `powershell` and press Enter. That opens **PowerShell**,
  Windows' terminal, in that folder.
- **macOS:** in Finder, right-click the folder, then Services, then
  New Terminal at Folder. (Or open Terminal and type `cd `, drag the
  folder onto the window, and press Enter.)
- **Linux:** right-click in the folder, then Open in Terminal, or
  `cd` there.

Then run:

```sh
docker version
docker compose version
```

**Success:** each prints a version number, and the first one has a
`Server:` part. **Docker** runs programs in sealed boxes called
**containers**, and Docker Compose starts several of them together.
No `Server:` part means Docker is not running, or your user may not
use it yet (see
[Troubleshooting: setting up](#troubleshooting-setting-up)). Get
Docker Desktop (Windows, macOS) or Docker Engine (Linux) from
docker.com. On Windows, Docker Desktop brings its own Linux; you need
not set up anything else.

### Also: your Ollama server

The test uses `local`, served by **Ollama** (a program that runs AI
models on your own hardware). Find your Ollama machine's address (on
that machine, `hostname -I` on Linux, `ipconfig getifaddr en0` on a
Mac, `ipconfig` in PowerShell). In Step 3, `./lvp init --ollama`
tries it for you. To try it now by hand (on Windows, type
`curl.exe`), giving up after 5 seconds:

```sh
curl -m 5 http://<ollama-ip>:11434
```

**Success:** `Ollama is running`. Keep that whole URL for Step 3. If
it fails and the address is right, Ollama is probably listening only
to its own computer, its default: set `OLLAMA_HOST=0.0.0.0` on that
machine and restart Ollama ([how](#letting-the-proxy-reach-ollama)).
Ollama on the proxy machine itself? Still use the network address
([find it](#find-the-proxy-address)), not `localhost`.

Ollama also needs the model that `local` points to. Open
`config.yaml` in a text editor: the name comes after `ollama_chat/`.
On the Ollama machine, `ollama pull <name>` downloads it. No Ollama?
Use `claude` instead; Steps 3b and 6 say what to change.

### Letting the proxy reach Ollama

On Linux, run `sudo systemctl edit ollama`, and between the two
comment lines at the top add `[Service]` and, on the next line,
`Environment="OLLAMA_HOST=0.0.0.0"`. Save, then run
`sudo systemctl restart ollama`. On a Mac, run
`launchctl setenv OLLAMA_HOST 0.0.0.0`, then quit and reopen Ollama.
On Windows, add a user environment variable `OLLAMA_HOST` with value
`0.0.0.0`, then quit and reopen Ollama. Typing `export OLLAMA_HOST=...`
in a terminal does not change a running Ollama.

Then try the address again, and read
[safety rule 7](#safety-rules): Ollama is now open to your network.

## Step 2: Get the files

**There is no release yet.** Until the first one, build `lvp`
yourself: see [Build from source](#build-from-source) below. The
download steps that follow are ready for when the repo has a release.

### Download a release (once the repo has a release)

On the repo's GitHub page, open **Releases** and pick the file for
the proxy machine:

| File                                        | For                   |
|---------------------------------------------|-----------------------|
| `local-verse-proxy-<tag>-windows-amd64.zip` | most Windows PCs      |
| `local-verse-proxy-<tag>-windows-arm64.zip` | Windows on Arm        |
| `local-verse-proxy-<tag>-macos.tar.gz`      | every Mac             |
| `local-verse-proxy-<tag>-linux-amd64.tar.gz`| most Linux PCs        |
| `local-verse-proxy-<tag>-linux-arm64.tar.gz`| Raspberry Pi 4 or 5   |

Download `SHA256SUMS` too: it lists a **checksum** (a fingerprint of
a file) for each download. Check yours arrived whole, in the folder
you downloaded both to:

- Linux: `sha256sum -c --ignore-missing SHA256SUMS` says `OK`.
- macOS: `shasum -a 256 -c --ignore-missing SHA256SUMS` says `OK`.
- PowerShell: `Get-FileHash <file>` prints a long code; it must match
  that file's line in `SHA256SUMS` (capitals do not matter).

Unpack the **archive** (the .zip or .tar.gz) into a folder in your
own user folder: on Windows, right-click it, Extract All; on a Mac,
double-click it; on Linux, `tar -xzf <file>`. You get one folder,
`local-verse-proxy`, with `lvp` (`lvp.exe` on Windows),
`compose.yaml`, `config.yaml`, `seats.env.example` and this README,
among others. Open a terminal in it (Step 1 says how). The first run
may need one click to allow it:

- **macOS:** "cannot be opened because it is from an unidentified
  developer". Run `xattr -d com.apple.quarantine lvp mint-key` once,
  in that folder.
- **Windows:** "Windows protected your PC" (SmartScreen). Click
  More info, then Run anyway. Your antivirus may ask too.

### Build from source

You need **Go** 1.26 or newer (from go.dev; the `go` command fetches
a newer Go by itself if the repo asks for one) and **git**. In a
terminal:

```sh
git clone https://github.com/kjFriday/local-verse-proxy
cd local-verse-proxy
go build -o . ./cmd/lvp
```

That makes `lvp` (`lvp.exe` on Windows) in the repo folder. The
first build downloads a few Go libraries and takes about a minute.

### Check it runs

```sh
./lvp version
```

**Success:** `lvp` and a version (`dev` for one you built). Run every
later command from this folder: it holds `compose.yaml`, and `lvp`
looks for it there. A new terminal starts in your home folder, so
`cd` back here first.

## Step 3: Make your secrets file

All the secrets live in one **env file**, `seats.env`: a plain text
file of `NAME=value` lines that programs read settings from. One
command makes it from the template, `seats.env.example`, fills in
the secrets, and makes it readable only by you. Give it your Ollama
address from Step 1:

```sh
./lvp init --ollama http://<ollama-ip>:11434
```

**Success:** `Made seats.env from seats.env.example`, a list of the
settings it filled in, `Ollama answers there.`, and the next command
to run. No secret is shown. A `warning:` about Ollama means it did
not answer: check the address (Step 1), then run the same line again.
No Ollama? Leave out `--ollama ...` and see 3b.

### 3a. The three secrets

The master key, the **salt key** (it encrypts stored credentials in
the database) and the database password must be random. `lvp init`
wrote all three into `seats.env`, so nothing was copied by hand:

- `LITELLM_MASTER_KEY`: `sk-` and 48 random letters and digits.
- `LITELLM_SALT_KEY`: the same kind, a different one.
- The database password, 32 random letters and digits, in both
  `POSTGRES_PASSWORD` and `DATABASE_URL`.

`init` fills only lines that still hold the template's placeholders
(`change-me`, or an empty `LITELLM_MASTER_KEY=`), so a second run
changes nothing. It is safe to run again. Keep these values as they
are: Postgres saves its password on its first start, and the salt key
must never change once values are stored with it. That is why `init`
refuses to write a new database password once Postgres has started:
it says why, and what to do.

### 3b. Point at your models

To look or change something, open `seats.env` in a text editor:

- Windows: `notepad seats.env`
- macOS: `open -e seats.env` (TextEdit)
- Linux: `nano seats.env` (save with Ctrl+O then Enter, quit with
  Ctrl+X)

The two **upstreams**, the real services behind the proxy:

- `OLLAMA_API_BASE`: `http://<ollama-ip>:11434`, as `init --ollama`
  set it. Not `localhost`: inside the proxy's container, that means
  the container itself.
- `ANTHROPIC_API_KEY`: your Anthropic API key, for `claude`. Leave it
  empty if you have none; `local` and the test do not need it. No
  Ollama? Fill this in, and further down set `DEFAULT_MODELS=claude`.

Leave the last block as it is: it tells `lvp mint` where the proxy is
(`PROXY_URL`), and that new keys may use only `local`, spend 5 US
dollars in total and expire after 30 days.

### 3c. A final check

```sh
./lvp check
```

**Success, at this point:** the `seats.env` lines say `ok` (filled
in, yours only), and so does Docker. The proxy is not started yet, so
the lines about `db`, `proxy` and its answer say `FAIL`, and the
master key line says `skip`; Step 4 fixes those. Each `FAIL` line has
a `Fix:` line under it, with what to do.

## Step 4: Start the proxy

```sh
docker compose up -d
```

The first time, Docker downloads two **images** (ready-made program
bundles), LiteLLM and Postgres, which can take a few minutes. `-d`
runs everything in the background. The `db` container (Postgres)
starts first; once it reports healthy, the `proxy` container
(LiteLLM) starts on port 4000. LiteLLM is pinned to `v1.106.6`, and
the status codes in this guide come from that version. **Success:**
lines ending in `Created`, `Healthy` and `Started`, then your prompt.

### Is it up?

```sh
./lvp wait
./lvp check
```

`wait` knocks on the proxy every 3 seconds, printing a dot each time,
until it answers: **success** is `The proxy is up.` The first start
can take a minute or two. After 3 minutes it gives up and says what
to look at. `check` then goes through everything once more, and
should end with `Everything checks out.`

Still not up? Or does `docker compose ps` show `proxy` as
`Restarting`, or `Up` with a few seconds that keep going back to 0?
That is a restart loop. Run `docker compose logs proxy` and see
[Troubleshooting: the proxy](#troubleshooting-the-proxy).

> **By hand.** The same knock with curl (on Windows, `curl.exe`):
> `curl -s -o /dev/null -w '%{http_code}\n' localhost:4000/v1/models`
> prints an **HTTP status code**, a web server's three-digit answer.
> `401` ("not allowed") means the proxy is up and wants a key; `000`
> means nothing answered yet.

## Step 5: Mint your first key

Pick a short seat name, such as `laptop`. Each name can belong to
only one key at a time. Mint the key and save it in a file named
after the seat:

```sh
./lvp mint laptop --save laptop.key
```

**Success:** `Saved the key for seat laptop in laptop.key; only you
may open it.` The key has the defaults from Step 3b. Open the file to
see it: one line starting with `sk-`. The file is written only if
minting worked; if not, the reason is on screen (see
[Troubleshooting: minting](#troubleshooting-minting)). `--save`
never overwrites a file, so an old key is never lost by mistake.

`.gitignore` keeps `.key` files out of git. The master key is never
printed, and never put on a command line. (A `401` error shows only
its last 4 characters and a hash.)

## Step 6: The 3-check test

This checks the three promises of "one lock, many keys": a key works
for its allowed models, is refused for others, and stops working once
revoked:

```sh
./lvp test
```

It mints a throwaway key, for a seat named `test-` and 6 random
letters and digits, that may use only `local`, with a 1 dollar
budget. Then:

1. **The key works:** it asks `local` to say hi. The first answer can
   take up to a minute while Ollama loads the model; it waits up to 2.
2. **The wrong model is refused:** the same key asks for `claude`,
   and must get `403` ("not available for this API key"; with an
   older `LITELLM_TAG`, maybe `401`). A real answer means the key was
   not stopped at the door.
3. **A revoked key is refused:** it revokes the key, then knocks with
   it, and must get `401`.

**Success:** three `PASS` lines, then
`All three checks pass: your proxy works, and the test key is gone.`
A `FAIL` line says why. The test key is revoked at the end whatever
happens, even after Ctrl+C. Fix the cause with the Troubleshooting
sections, and run the test again.

**Only set up `claude`?** Check 1 then fails, as it asks `local`.
Check 2 still shows the door works. Try a `claude` key by hand, as in
[Step 7](#seat-check-can-it-reach-the-proxy).

> **By hand.** Check 1 with curl, for a key in `$KEY`:
> `curl -s -m 120 localhost:4000/v1/chat/completions -H
> "Authorization: Bearer $KEY" -H 'Content-Type: application/json'
> -d '{"model":"local","messages":[{"role":"user","content":"hi"}]}'`
> (all on one line) prints JSON with `"choices"` in it. Use `claude`
> as the model for check 2, and `/v1/models` after revoking for check 3.

## Step 7: Give the key to a seat

Send the seat three things, privately, not in a shared chat:

- **Base URL**, the address an app sends requests to:
  `http://<proxy-ip>:4000`
- **API key:** the key you minted for it (in `laptop.key`)
- **Model:** `local`, or `claude` if its key allows it

The seat types them into any **OpenAI-compatible** app or code
library: one that speaks OpenAI's request format and lets you set a
base URL (also called "API base" or "endpoint") and an API key. Some
apps want `/v1` on the end; the proxy accepts both. A seat never
needs `seats.env`, the master key, or this repo.

### Find the proxy address

On the proxy machine, run `hostname -I` (Linux) or
`ipconfig getifaddr en0` (Mac; if it prints nothing, try `en1`). On
Windows, run `ipconfig` in PowerShell and use the IPv4 address of
your Wi-Fi or Ethernet adapter. Use the address your router gave the
machine, usually starting `192.168.` or `10.`. Skip Docker's own
addresses, like `172.17.0.1` and `172.18.0.1`, and WSL's. Unsure?
Check your router's device list.

### Seat check: can it reach the proxy?

On the seat, open `http://<proxy-ip>:4000/v1/models` in a browser. A
short JSON error such as `No api key passed in` means it reaches the
proxy; "can't reach this page" means it does not (see
[Troubleshooting: seats](#troubleshooting-seats)). Then try its key
in a terminal, on one line (on Windows, type `curl.exe`):

```sh
curl http://<proxy-ip>:4000/v1/models -H "Authorization: Bearer <seat-key>"
```

**Pass:** JSON listing the models this key may use.

## Minting more keys

```text
./lvp mint <seat> [--models a,b] [--budget USD] [--duration 30d]
                  [--budget-duration 1d] [--save FILE]
```

Parts in `[ ]` are optional; left out, they use the `DEFAULT_` lines
in `seats.env`, which are reread every time.

- `--models local,claude`: the aliases the key may use, split by
  commas. Name at least one. Names are not checked: a typo such as
  `lcoal` makes a key that gets `403` for everything. Copy them from
  the `model_name:` lines in `config.yaml`. In PowerShell, put quotes
  around the list, as in `--models 'local,claude'`: there, a bare
  comma splits it in two.
- `--budget 20`: the most it may spend in total, in US dollars. Type
  the number only. Never type `$`: the terminal quietly changes it.
  In PowerShell and bash, `$20` becomes nothing or `0`, a key refused
  every time; in zsh, the Mac default, `$20` vanishes.
- `--duration 90d`: time until it expires, as a number and a
  lowercase unit with no space: `d` days, `h` hours, `w` weeks, `mo`
  months (like `3mo`). `m` means minutes, not months.
- `--budget-duration 1d`: refill the budget on a schedule, so
  `--budget` becomes an allowance per day (or per `12h`, `1w`, `1mo`;
  `s` and `m` work too, but months only as `1mo`). Left out, the
  budget is a lifetime total. LiteLLM lines refills up with the
  clock, in UTC (world time; see the [glossary](#glossary)): `1d`
  refills at midnight, `1h` on the hour, `1w` on Mondays, `1mo` on
  the 1st. The proxy checks for due refills about every 10 minutes,
  so a refill can land up to 10 minutes late. Its first check comes
  up to 15 minutes after the proxy starts, so just after a restart a
  refill can be later still.
- `--save desk.key`: save the key in that file, readable only by you,
  instead of printing it. The file must be new.

For example, a desk computer that may use both models:

```sh
./lvp mint desk --models local,claude --budget 20 --duration 90d --save desk.key
```

### Budgets, expiry and seat names

- **A budget is a lifetime total,** unless the key was minted with
  `--budget-duration`. To top up, revoke the key and mint a new one.
  A key past its budget gets `422` until the next refill, if any.
- **`local` costs nothing.** LiteLLM prices the Ollama model at $0, so
  budgets only limit paid models such as `claude`.
- **Expiry is final.** An expired key gets `401` but may still hold
  its seat name. Revoke it by seat name, then mint again.
- **Seat names are unique.** Minting a taken name fails with
  `already exists`. Revoke the old key, or pick a name like `laptop-2`.
  Names that start with `game-` are kept for the
  [office game](game/README.md).

## Revoking a key

Revoke a key when a device is lost, someone leaves, or a key may have
leaked. Use one of these:

```sh
./lvp revoke laptop                  # by seat name
./lvp revoke --key-file laptop.key   # or by the saved key
```

**Success:** `revoked seat laptop` or `revoked`. The seat name works
even if the key itself is lost. That key now gets `401`, every other
key keeps working, and the seat name is free again; delete its file
(`laptop.key`) once it is revoked. An error mentioning `404` or
`No keys found` means there was no such key: already revoked, or
mistyped. `./lvp revoke --key <key>` revokes a key you have pasted.

## Adding a model

Models are listed under `model_list:` in `config.yaml`. Copy an
entry, paste it right after the `claude` entry (above
`general_settings:`, with the same two spaces before the `-`), and
change it. For a second Ollama model:

```yaml
  - model_name: local-small
    litellm_params:
      model: ollama_chat/<name-on-your-ollama>
      api_base: os.environ/OLLAMA_API_BASE
```

`model_name` is the alias seats ask for; `model` is the real model,
with the provider before the `/`. `os.environ/NAME` means "use the
setting NAME from `seats.env`": put any new secret there, never in
`config.yaml`. Apply the change, wait until the proxy answers, then
mint keys with the new alias (old keys keep only the models they were
made with). If the proxy then keeps restarting, its log usually names
the bad line.

```sh
docker compose up -d --force-recreate proxy
./lvp wait
./lvp mint den --models local,local-small --save den.key
```

Removing a model works the same way. Keys made with it still list it
in `/v1/models`, but calls to it get `400` `Invalid model name`.

## Day-to-day commands

```sh
docker compose up -d          # start
./lvp check                   # is everything all right?
docker compose ps             # is it running?
docker compose logs -f proxy  # watch messages (Ctrl+C to stop)
docker compose down           # stop (keys are kept)
docker compose up -d --force-recreate proxy   # apply edits
```

- After starting, the proxy needs about 30 seconds before it
  answers; `./lvp wait` waits for it. Minting before that fails with
  `Failed to connect to the proxy`.
- Use the last line after editing `seats.env` or `config.yaml`; a
  plain `restart` does not reread `seats.env`. After a `seats.env`
  edit it recreates `db` too, which is harmless: keys stay.
- Running [Front Desk](gui/README.md) in Docker? Add
  `--profile frontdesk` to `down`, or Front Desk keeps running. After
  a `seats.env` edit, also run
  `docker compose --profile frontdesk up -d`, or Front Desk keeps the
  old settings, master key included.
- Keys and spending live in a Docker **volume** (storage that
  survives restarts) called `local-verse-proxy_pgdata`. Never add
  `-v` to `docker compose down`: that deletes the volume and every
  key.
- After a reboot both containers return on their own, but only if
  Docker starts at boot: on Linux, `sudo systemctl enable docker`
  once; in Docker Desktop, turn on its start-at-sign-in setting.
- Settings for Docker Compose itself, such as `LITELLM_TAG` or Front
  Desk's `FRONTDESK_BIND`, go in a file named `.env`, not in
  `seats.env`. `./lvp env set NAME=value` writes one line there,
  `./lvp env unset NAME` removes it, and `./lvp env` shows them.

## Upgrading

1. Stop Front Desk if it runs on this computer (`./lvp desk`: press
   Ctrl+C in its window).
2. Unpack the new release over this folder. It never holds
   `seats.env`, `.env`, your `.key` files or the game's `office.db`,
   so those stay as they are. It does hold `config.yaml` and
   `game/rules.toml`: if you edited them, copy yours aside first, and
   put them back after. (Built from source? Run `git pull`, then
   `go build -o . ./cmd/lvp` again.)
3. `docker compose up -d --build`: it rebuilds Front Desk's image, if
   you use it, and keeps the proxy's data.
4. `./lvp version` shows the new version; `./lvp check` should end
   with `Everything checks out.`

## Safety rules

1. **Never commit `seats.env`.** It holds every secret. `.gitignore`
   covers it; keep it that way.
2. **Never give anyone the master key.** Seats get only their own key.
3. **Never change `LITELLM_SALT_KEY` after the first start.** Values
   stored with it could no longer be decrypted.
4. **One key per seat, kept private.** One lost device, one revoke.
5. **Keep port 4000 on your local network.** Do not forward it from
   your router. `compose.yaml` opens it on every network the machine
   is on, as plain `http://` with no encryption, so keys cross your
   network readable. Use it only on a network you trust. On Linux,
   ports that Docker opens may bypass firewall rules such as ufw's.
6. **The admin page shares the port.** LiteLLM serves one at
   `http://<proxy-ip>:4000/ui`, and the master key logs in to it.
   Log in only on the proxy machine (`http://localhost:4000/ui`):
   from another machine, the master key crosses your network
   unencrypted.
7. **Lock down Ollama.** It has no keys. Anyone who reaches its port
   11434 can use it directly, skipping your keys and budgets.
   Firewall it so only the proxy machine can reach it. (Ollama on the
   proxy machine? See [the ufw entry](#troubleshooting-the-proxy).)
8. **Master key leaked?** Put a new one in `seats.env` and run
   `docker compose up -d --force-recreate proxy` (with Front Desk in
   Docker, then `docker compose --profile frontdesk up -d` too). Seat
   keys keep working, and so does any key someone minted with the
   old master key. In a browser on the proxy machine, open
   `http://localhost:4000/ui`, log in as `admin` with the new master
   key as password, and delete every key you did not make.
9. **Keep the folder in your own user folder.** `seats.env` and saved
   keys are readable only by you; `./lvp check` says if that ever
   changes.

## About `lvp mint-key`

`./lvp mint` and `./lvp revoke` are the everyday way. Underneath,
both use `./lvp mint-key`, the older form, kept for scripts: it
prints only the key, takes the same options, and gives the same
answers as before.

```text
./lvp mint-key <seat> [--models a,b] [--budget USD] [--duration 30d]
                      [--budget-duration 1d]
./lvp mint-key --revoke <key>
./lvp mint-key --revoke-seat <seat>
```

Two more programs do exactly the same, for scripts that use them:
the separate `mint-key` program (`mint-key.exe` on Windows), which
comes in this release's archive too; and `mint-key.sh`, the first
version, a bash script for Linux and macOS that needs curl and jq.
Both still work. `tests/mint-parity.sh` runs them side by side
against your running proxy, to show they agree.

`lvp mint-key` looks for `seats.env` in `SEATS_ENV` if that is set,
then in the folder you run it from, then in its own folder. In
PowerShell, `$env:SEATS_ENV = 'C:\<path>\seats.env'` sets it.

**How it differs from `mint-key.sh`**, on purpose:

- Errors from the proxy start with `the proxy returned error: 400
  (Bad Request)`, where curl says `curl: (22) The requested URL
  returned error: 400`; a hint may follow. LiteLLM's own message
  still comes on the next line.
- With no answer at all, it says `Failed to connect to the proxy at
  <address> (<reason>)` and what to do, where curl says `(7)`, `(52)`
  or `(56)`.
- A failed mint exits with code 1, where the script's code is 4 (from
  jq). Usage and value mistakes are 2 in both.
- It never runs anything in `seats.env`. Where bash would run
  `$(...)` or backticks, it refuses, naming the line. Windows line
  endings (from Notepad, say) are fine; bash trips on them.
- It gives up after 25 seconds without an answer; curl keeps waiting.
  A proxy that got the request may still make the key after that, so
  the message says so: revoke the seat, then mint again.
- Like curl, it goes through the **web proxy** (a middleman some
  networks put in front of all web traffic) set in `HTTPS_PROXY` or
  `HTTP_PROXY`, except for the hosts in `NO_PROXY`, and never for
  `localhost` or `127.0.0.1`. It does not follow a redirect, so the
  master key goes nowhere else.
- If a message from the proxy ever held the master key, it would show
  only as `sk-...` and the last 4 characters.

## Troubleshooting: setting up

- **`lvp: command not found`** (Linux, macOS): type `./lvp`, with the
  `./`, in the folder that holds it.
- **`lvp : The term 'lvp' is not recognized`** (PowerShell): type
  `.\lvp`, with the `.\`, in the folder that holds it.
- **`Permission denied`** running `./lvp` (Linux, macOS): the file
  lost its "may run" mark, often after copying. Run `chmod +x lvp`
  once.
- **macOS: "cannot be opened because it is from an unidentified
  developer"**, or "Apple could not verify": it is not signed by a
  paid Apple account. Run `xattr -d com.apple.quarantine lvp mint-key`
  once, in its folder.
- **Windows: "Windows protected your PC"**: SmartScreen does not know
  the program yet. Click More info, then Run anyway. If your
  antivirus moves `lvp.exe` away, restore it and allow it there; or
  [build it yourself](#build-from-source).
- **`cannot find compose.yaml`**: you are not in the repo folder.
  `cd` into the folder you unpacked or cloned (it holds
  `compose.yaml`), and run `lvp` there.
- **`Cannot connect to the Docker daemon`** or **`failed to connect
  to the docker API`**: Docker is not running.
  On Linux, run `sudo systemctl enable --now docker`; or start Docker
  Desktop.
- **`permission denied` mentioning `docker.sock`**: your user may not
  use Docker yet. On Linux, run `sudo usermod -aG docker $USER`, then
  log out and back in. (`lvp` runs `docker` itself, so `sudo` in
  front of `docker` does not help it.)
- **`'compose' is not a docker command`**, `unknown command`, or
  `unknown shorthand flag: 'd'`: Docker came without Compose, as
  distro packages such as Ubuntu's `docker.io` do. On Ubuntu, run
  `sudo apt install docker-compose-v2`; elsewhere, install Docker from
  docker.com, which includes it.
- **`env file .../seats.env not found`** or **`missing ...`**: do
  Step 3.
- **A firewall asks** whether `lvp` may accept connections (Windows
  Defender, macOS) when you start `./lvp desk --lan`: allow it on
  private networks only. Without `--lan` nothing should ask.

## Troubleshooting: Windows and WSL

Docker Desktop on Windows needs no WSL setup from you. But if you
run Docker inside **WSL** (Windows Subsystem for Linux, a Linux that
runs inside Windows), as some setups do:

- **Run `lvp` inside WSL too**, next to `seats.env`, when the proxy
  runs in Docker inside WSL: build or unpack the Linux `lvp` there.
  From PowerShell, prefix each line with
  `wsl -d Ubuntu --cd ~/local-verse-proxy`, as in
  `wsl -d Ubuntu --cd ~/local-verse-proxy ./lvp check`.
  [docs/lvp-handbook.md](docs/lvp-handbook.md#windows-with-wsl) shows
  the whole way.
- **`Permission denied` after building into WSL from PowerShell**
  (with `GOOS=linux`, into `\\wsl.localhost\...`): files written into
  WSL from Windows arrive without the "may run" mark. Run
  `wsl -d Ubuntu --cd ~/local-verse-proxy chmod +x lvp` once after
  each build.
- **`127.0.0.1:4000` hangs inside WSL**, while the proxy runs and
  `[::1]:4000` answers: this happens on some machines. `lvp` (built
  from commit 84ab426 on) reads `localhost` as curl does, trying
  `::1` first, then `127.0.0.1`, so the template's
  `PROXY_URL=http://localhost:4000` works there as it is. Only with
  `mint-key.sh`, or an older `lvp`, put `PROXY_URL=http://[::1]:4000`
  in `seats.env` instead. Only the minting tools (`lvp mint`,
  `revoke`, `test`, `wait`, `check`, `mint-key`) read that line;
  Front Desk in Docker ignores it.
- **`git clone` or `git pull` from GitHub asks for a password in
  WSL**, and refuses yours: GitHub takes no account passwords over
  HTTPS. Use a personal access token as the password, or set up Git
  Credential Manager, which can share Windows' sign-in with WSL.
- **The folder by its Windows name:** in WSL, `wslpath -w .` prints
  it, like `\\wsl.localhost\Ubuntu\home\<you>\local-verse-proxy`.
  `notepad` opens files there.

## Troubleshooting: minting

- **`LITELLM_MASTER_KEY: not set in ...`**: the master key line in
  `seats.env` is empty. Run `./lvp init` (it fills only empty or
  placeholder lines), then
  `docker compose up -d --force-recreate proxy`. Keys you already
  made keep working.
- **`replace the change-me values in ... first`**: a placeholder is
  left in `seats.env`. If you never ran `docker compose up`, run
  `./lvp init` and carry on. If you did, Postgres already saved the
  template password, and `./lvp init` says so. With no keys worth
  keeping yet, start over: `docker compose down -v`, `./lvp init`,
  then `docker compose up -d`.
- **Usage text** (`usage: ./lvp mint <seat> ...`): the seat name is
  missing or starts with `-`, an option is misspelled or has no
  value, or a space slipped into `--models local, claude`.
- **`Failed to connect to the proxy at ...`**: the proxy is not
  running, or is still starting. Run `./lvp wait`, then mint again.
- **`the proxy returned error: 400 (Bad Request)`, then `... already
  exists`**: that seat name is taken. Run `./lvp revoke <seat>`, or
  pick a new name.
- **`the proxy returned error: 401`**, then `Invalid proxy server
  token`: the proxy did not accept the master key, usually because
  `seats.env` changed after it started. Run
  `docker compose up -d --force-recreate proxy`. Minting through
  Front Desk in Docker? Run `docker compose --profile frontdesk up -d`
  instead. The message shows only the key's last 4 characters and a
  hash, not the key.
- **`... is there already, so nothing was minted`**: `--save` never
  overwrites a file. Pick another file name, or revoke the key in it
  first (`./lvp revoke --key-file <file>`) and delete the file.
- **`seats.env line 9: $(...) would run a command`**, or another
  `seats.env line` message: that line has shell code in it. Make it a
  plain `NAME=value` line.
- **`seats.env seems to be saved as UTF-16`**: Windows PowerShell's
  `>` and Notepad's "Unicode" save files that way. Save it again as
  UTF-8 (in Notepad: File, Save as, then Encoding: UTF-8).
- **`(no answer within 25 seconds)`**, then
  **`it may have made the key anyway`**: the proxy is slow, perhaps
  busy or just started. The key may be made but was never shown:
  run `./lvp revoke <seat>`, then mint again.
- **With `mint-key.sh`**, the same problems read as curl's: `curl: (7)`,
  `(52)` or `(56)` for `Failed to connect`, and `curl: (22)` for
  `the proxy returned error`. Causes and fixes are the same.

## Troubleshooting: the proxy

Start with `./lvp check`, then `docker compose logs proxy`. A
**restart loop** shows as `Restarting`, or as `Up` with a few seconds
that keep going back to 0.

- **`proxy` is in a restart loop, and its log says it
  `refused to start` over the master key**: `LITELLM_MASTER_KEY` in
  `seats.env` is empty, or a well-known example such as `sk-1234`
  (`publicly known default`). Empty that line (keep
  `LITELLM_MASTER_KEY=`), run `./lvp init`, then
  `docker compose up -d --force-recreate proxy`. Ignore the log's
  advice about a `.env` file, and never set the `DANGEROUSLY_PERMIT`
  option it offers.
- **`proxy` is in a restart loop, and `docker compose logs db` says
  `password authentication failed`**: Postgres kept the password
  from its first start; put it back in `POSTGRES_PASSWORD` and
  `DATABASE_URL`. If no keys are worth keeping yet (say you started
  once before filling in `seats.env`), `docker compose down -v` and
  then `docker compose up -d` start the database fresh, erasing all
  keys.
- **Check 1 of `./lvp test` fails with `Cannot connect to host`, or
  `no answer within 2m0s`**: check that `OLLAMA_API_BASE` is not
  `localhost`, that `./lvp check` says `Ollama answers`, that
  `OLLAMA_HOST=0.0.0.0` is set on the Ollama machine
  ([how](#letting-the-proxy-reach-ollama)), and that the model is
  downloaded there.
- **Same, with Ollama on the proxy machine behind a firewall such as
  ufw**: the proxy's requests come from Docker's own network
  (addresses starting `172.`), which the firewall may drop. Allow it:
  `sudo ufw allow from 172.16.0.0/12 to any port 11434 proto tcp`
- **`claude` fails, or an edit to `seats.env` or `config.yaml` is
  ignored**: check `ANTHROPIC_API_KEY`, then apply your edits with
  `docker compose up -d --force-recreate proxy`.

## Troubleshooting: seats

- **The seat cannot reach the proxy**: check the
  [address](#find-the-proxy-address), that the seat is on the same
  network, and that no firewall on the proxy machine blocks port
  4000. If `./lvp check` passes on the proxy machine but the seat
  check fails, it is a network problem, not a key problem.
- **`No such file` naming `proxy-ip` or similar**: a placeholder was
  typed as is. Put in the real value, without `< >`. A `401` saying
  `expected to start with 'sk-'` means the same for `<seat-key>`:
  paste the whole key from the seat's `.key` file.
- **`404` with `{"detail":"Not Found"}`**: the app added `/v1` to a
  base URL that already had it. Give that app
  `http://<proxy-ip>:4000`, without `/v1`.
- **A brand-new key gets `422` `Budget has been exceeded!` with
  `Max budget: 0.0`**: it was minted with a 0 budget, often by
  typing `$20`. Revoke it and mint again.
- **A key that worked is refused**: read the message. `401` with
  `Expired Key` or `Invalid proxy server token` means it expired or
  was revoked; `422` with `Budget has been exceeded` means its budget
  is spent. Revoke it by seat name to free the name, then mint the
  seat a new key. A `401` that names Anthropic is not the seat's key:
  the proxy's `ANTHROPIC_API_KEY` is missing or wrong.
- **`403`, "not available for this API key"**: the key was not made
  with that model. Check the spelling, or mint one with `--models`.

## FAQ

**Which LiteLLM version runs?** `compose.yaml` pins `v1.106.6`. To
change it, run `./lvp env set LITELLM_TAG=<tag>`: that goes in
`.env`, next to `compose.yaml`, which Compose reads. It does not work
in `seats.env`, which is for the containers, not for Compose. Then
run `docker compose pull` and `docker compose up -d`, and rerun
`./lvp test`: status codes have changed between releases.

**Does a seat ever see my real keys?** No, only its own virtual key.
The real keys stay in `seats.env` on the proxy machine. There,
`compose.yaml` hands that file to every container (Front Desk's too,
if you run it), so the `db` container can see every secret too.

**Can I mint from another computer?** Best not: you would copy every
secret there, and the master key would cross your network over plain
HTTP. Use [Front Desk](gui/README.md) from the other computer's
browser instead.

## File map

- `README.md`: this guide.
- `LICENSE`: the Apache License 2.0, under which you may use this.
- `THIRD_PARTY_NOTICES.txt` (in release archives): the licenses of
  the Go libraries `lvp` is built with.
- `lvp` (`lvp.exe` on Windows): the program. In a release archive,
  or made by `go build -o . ./cmd/lvp`. Git ignores it.
- `mint-key` (`mint-key.exe`): the separate mint-key program, in this
  release's archives only; `lvp mint-key` does the same.
- `docs/lvp-handbook.md`: every `lvp` command, with Windows and WSL
  steps.
- `docs/port-spec.md`: the design `lvp` was built from; code comments
  cite its sections.
- `config.yaml`: the models, and the names of the settings that hold
  their secrets. No secrets inside, so it is safe to commit.
- `seats.env.example`: the template for your secrets file.
- `seats.env`: your real secrets, made in Step 3. Never commit it.
- `compose.yaml`: runs the proxy and Postgres, with the database in
  the `pgdata` volume; with `--profile frontdesk`, Front Desk too.
- `Dockerfile`: builds Front Desk's Docker image, which holds `lvp`
  and nothing else. The office game's practice model server runs
  from the same image.
- `cmd/`: where the programs start: `cmd/lvp`, and `cmd/mint-key`
  for the separate mint-key program.
- `internal/`: the Go code behind them, in small packages:
  `seats`, `litellm`, `mintkey` (seats.env, the proxy, minting);
  `frontdesk`, `pgread` (Front Desk and its read-only tally); `game`,
  `gamestate`, `rules`, `fakeollama` (the office game); `setup` (the
  helpers `init`, `check`, `wait`, `test`, `mint`, `revoke`, `env`);
  `cli`, `args`, `kit`, `fsx` and the rest (shared parts).
- `go.mod`, `go.sum`: name the Go module and its libraries; `go
  build` reads them.
- `gui/`: Front Desk's page (`gui/static/`, built into `lvp` by
  `gui/embed.go`). See [gui/README.md](gui/README.md).
- `game/`: an optional office game played with seat keys, where
  finished work scores and burned tokens do not. See
  [game/README.md](game/README.md).
- `mint-key.sh`: the first, bash version of mint-key, for Linux and
  macOS; it still works.
- `tests/`: the tests; `tests/mint-parity.sh` compares the mint-key
  versions against your running proxy.
- `.github/workflows/`: what GitHub runs on every push (`ci.yml`:
  tests and checks), and on a version tag like `v1.0.0`
  (`release.yml`: builds the downloads).
- `.env` (hidden, optional): settings for Docker Compose itself, such
  as `LITELLM_TAG` and Front Desk's `FRONTDESK_` settings.
  `./lvp env` writes it; git ignores it.
- `.gitignore`, `.dockerignore` (hidden): keep `seats.env`, `.env`,
  `*.key` files, game state and built programs out of git and out of
  Docker images.
- `*.key`: seat keys you saved. Never commit them.
- The old version, removed soon: `gui/frontdesk.py`,
  `game/office.py`, `game/fake_ollama.py` and
  `gui/requirements-docker.txt`, the Python programs that `lvp desk`,
  `lvp game` and `lvp fake-ollama` replace. They still work, with
  Python 3.11 or newer, until they are removed.

## Glossary

- **API key:** a secret string that lets a program use a service.
- **Archive:** one file that holds a folder of files, packed small:
  a .zip or .tar.gz. Unpack it to get the folder.
- **Budget:** the most a key may spend, in US dollars, over its life
  (or per refill, with `--budget-duration`).
- **Checksum:** a fingerprint of a file. If yours matches the one in
  `SHA256SUMS`, the download arrived whole and unchanged.
- **Docker:** runs programs in sealed boxes called containers.
- **Env file:** a text file of `NAME=value` lines, here `seats.env`.
- **Go:** a programming language. A Go program is one file that runs
  without anything else installed.
- **Image:** a ready-made program bundle that Docker runs as a
  container.
- **Master key:** `LITELLM_MASTER_KEY`, the admin key that makes and
  cancels other keys. Never give it out.
- **Mint, revoke:** create a virtual key; cancel one.
- **Ollama:** a program that runs AI models on your own hardware.
- **Port:** a numbered door on a computer. The proxy uses 4000.
- **Postgres:** a database. Here it stores keys, budgets and spending.
- **PowerShell:** Windows' terminal. There, type `.\lvp`.
- **Profile:** a named group of optional containers in
  `compose.yaml`, like `frontdesk`, that start only when asked for.
- **Proxy:** a middleman between apps and the real AI services.
- **Release:** a published version of this repo, with ready-made
  downloads, listed under Releases on GitHub.
- **Salt key:** `LITELLM_SALT_KEY`; encrypts stored credentials.
- **Seat:** one person, computer or app with its own key.
- **Terminal:** the window where you type commands: PowerShell on
  Windows, Terminal on a Mac.
- **UTC:** world time, the same everywhere. Midnight UTC is 8 PM the
  day before in New York (in summer) and 1 AM in Paris (in winter).
  Front Desk shows refill times in your own time.
- **Virtual key:** a seat's own key, limited by models, budget and
  expiry, that works only through this proxy.
- **WSL:** Windows Subsystem for Linux, a Linux that runs inside
  Windows. Not needed here, but some people run Docker in it.

## License

Apache License 2.0: see [LICENSE](LICENSE). You may use, change and
share this project, also commercially, if you keep the license and
its notices with it and say what you changed. The programs it builds
on keep their own licenses; each release lists them in
`THIRD_PARTY_NOTICES.txt`.

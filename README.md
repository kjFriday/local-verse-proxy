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

`mint-key.sh` prints the key cards. (On Windows, or anywhere without
bash, its twin `mint-key` does the same: see
[the Go version](#the-go-version).) The steps below set everything
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
  [the Go version](#the-go-version), [revoking](#revoking-a-key),
  [adding a model](#adding-a-model),
  [day-to-day commands](#day-to-day-commands),
  [safety rules](#safety-rules)
- Troubleshooting: [setting up](#troubleshooting-setting-up),
  [minting](#troubleshooting-minting), [the proxy](#troubleshooting-the-proxy),
  [seats](#troubleshooting-seats)
- [FAQ](#faq), [file map](#file-map), [glossary](#glossary), [license](#license)

## Quick start

```sh
# on the proxy machine
git clone https://github.com/kjFriday/local-verse-proxy
cd local-verse-proxy
cp seats.env.example seats.env && chmod 600 seats.env
nano seats.env                # fill it in (Step 3 shows how)
grep -E 'change-me|^LITELLM_MASTER_KEY=$' seats.env  # prints nothing
docker compose up -d
docker compose ps             # db (healthy) and proxy, both Up
# first start takes a minute or two; Ctrl+C after 3 min, see "Is it up?"
until curl -sfo /dev/null localhost:4000/health/liveliness; do sleep 3; done
key=$(./mint-key.sh laptop) && echo "$key" > laptop.key
chmod 600 laptop.key
cat laptop.key                # one line starting with sk-
```

In `seats.env`: two different random `sk-` keys, one letters-and-digits
password in both `POSTGRES_PASSWORD` and `DATABASE_URL`, and
`OLLAMA_API_BASE`. Then run the [3-check test](#step-6-the-3-check-test).

**Prefer clicking?** Once the proxy is up,
`docker compose --profile frontdesk up -d` starts Front Desk, a web
page at `http://127.0.0.1:8040` that makes and cancels key cards and
shows the tally. Like the proxy, it runs in Docker, so there is
nothing else to install. Open that address in a browser on this
machine, with the password that `docker compose logs frontdesk`
shows. Phones can use it too, and it also runs with Python 3.11 or
newer (`python3 gui/frontdesk.py`). See [gui/README.md](gui/README.md).

## One lock, many keys

| In the building | In this repo                            |
|-----------------|-----------------------------------------|
| The front desk  | The proxy, on port 4000                 |
| Master key      | `LITELLM_MASTER_KEY` in `seats.env`     |
| The locked safe | Real provider keys, also in `seats.env` |
| A key card      | A virtual key you mint for one seat     |
| Doors it opens  | Models it may use (`local`, `claude`)   |
| Card runs out   | It spends its budget or expires         |
| Cancel a card   | `./mint-key.sh --revoke-seat <seat>`    |

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
   |  proxy on port 4000  | <-- mint-key.sh
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
network as your seats, running Linux or macOS. (On Windows, do every
step inside **WSL**, Windows Subsystem for Linux: a Linux that runs
inside Windows. `wsl --install` in PowerShell sets it up. Turn on
Docker Desktop's WSL integration too.)
Open a **terminal** (the window where you type commands) there and
run:

```sh
docker version --format '{{.Server.Version}}'
docker compose version
curl --version
jq --version
openssl version
git --version
```

**Success:** each prints a version number. **Docker** runs programs
in sealed boxes called **containers**, and Docker Compose starts
several of them together. The first command prints a number only if
Docker is running and your user may use it (if not, see
[Troubleshooting: setting up](#troubleshooting-setting-up)). curl must
be 7.76 or newer; Ubuntu 20.04 and RHEL 8 ship older ones. Only
`mint-key.sh` needs jq: skip it if you will mint with
[Front Desk](gui/README.md) in Docker or with
[the Go version](#the-go-version).

### Missing a tool?

Ubuntu or Debian: `sudo apt install curl jq openssl git`.
Fedora: `sudo dnf install curl jq openssl git`. Mac:
`brew install jq`, with Homebrew (an installer for Mac programs,
from brew.sh). Get Docker from docker.com. `mint-key.sh` also needs
bash, which Linux and macOS have. Minting from Windows, or a machine
without bash, curl or jq? [The Go version](#the-go-version) needs
none of them.

### Also: your Ollama server

The test uses `local`, served by Ollama. From the proxy machine, try
your Ollama machine's address (on that machine, `hostname -I` shows
it; on a Mac, `ipconfig getifaddr en0`). `-m 5` gives up after 5
seconds instead of hanging:

```sh
curl -m 5 http://<ollama-ip>:11434
```

**Success:** `Ollama is running`. Keep that whole URL for Step 3b. If
it fails and the address is right, Ollama is probably listening only
to its own computer, its default: set `OLLAMA_HOST=0.0.0.0` on that
machine and restart Ollama ([how](#letting-the-proxy-reach-ollama)).
Ollama on the proxy machine itself? Still use the network address
([find it](#find-the-proxy-address)), not `localhost`.

Ollama also needs the model that `local` points to. Run
`grep ollama_chat config.yaml`; the name comes after `ollama_chat/`.
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

Then run the `curl` check above again, and read
[safety rule 7](#safety-rules): Ollama is now open to your network.

## Step 2: Get the files

```sh
git clone https://github.com/kjFriday/local-verse-proxy
cd local-verse-proxy
ls
```

**Success:** `ls` lists `compose.yaml`, `config.yaml`, `mint-key.sh`,
`seats.env.example` and `README.md`, among others. Run every later
command from this folder. A new terminal starts in your home folder,
so `cd` back here first.

## Step 3: Make your secrets file

All the secrets live in one **env file**: a plain text file of
`NAME=value` lines that programs read settings from. Copy the
template and make it readable only by you:

```sh
cp seats.env.example seats.env
chmod 600 seats.env
```

Run the copy only once: later, it would wipe your filled-in file.

### 3a. The three secrets

The master key, the **salt key** (it encrypts stored credentials in
the database) and the database password must be random. This block
makes all three and writes them in, so nothing is copied by hand:

```sh
pw="$(openssl rand -hex 16)"
mk="sk-$(openssl rand -hex 24)"
sk="sk-$(openssl rand -hex 24)"
sed -i.bak \
  -e "s/^LITELLM_MASTER_KEY=$/LITELLM_MASTER_KEY=$mk/" \
  -e "s/^LITELLM_SALT_KEY=sk-change-me$/LITELLM_SALT_KEY=$sk/" \
  -e "s/change-me/$pw/g" seats.env && rm seats.env.bak
unset pw mk sk
```

It prints nothing, and it fills only lines that still hold the
template's placeholders, so a second run changes nothing. The
password goes in both database lines and is letters and digits only,
which keeps `DATABASE_URL` valid. Do this **before the first
`docker compose up`**: Postgres saves its password on first start,
and the salt key must never change once values are stored with it.

### 3b. Point at your models

Open the file in nano, a simple text editor: `nano seats.env`. Move
with the arrow keys, save with Ctrl+O then Enter, quit with Ctrl+X.
`[ New File ]` at the bottom means you are in the wrong folder. To
paste, use Ctrl+Shift+V (Linux terminals) or Cmd+V (Mac).

Set the two **upstreams**, the real services behind the proxy:

- `OLLAMA_API_BASE`: `http://<ollama-ip>:11434`, the address that
  worked in Step 1 (the template's `192.168.1.50` is only an example).
  Not `localhost`: inside the proxy's container, that means the
  container itself.
- `ANTHROPIC_API_KEY`: your Anthropic API key, for `claude`. Leave it
  empty if you have none; `local` and the test do not need it. No
  Ollama? Fill this in, and further down set `DEFAULT_MODELS=claude`.

### 3c. Mint defaults, and a final check

Leave the last block as it is: it tells `mint-key.sh` where the proxy
is, and that new keys may use only `local`, spend 5 US dollars in
total and expire after 30 days. Save, quit, then check:

```sh
grep -E 'change-me|^LITELLM_MASTER_KEY=$' seats.env
```

**Success:** it prints nothing. A printed line still needs a value.
The proxy will not start with an empty master key, but it would start
with the template's salt key or password, which anyone can read here.

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
docker compose ps
curl -s -o /dev/null -w '%{http_code}\n' localhost:4000/v1/models
```

**Success:** `ps` shows two rows, `db` and `proxy`, both `Up`, and
`db` also says `healthy`. The `curl` line knocks without a key and
prints an **HTTP status code**, a web server's three-digit answer.
`401` ("not allowed") means the proxy is up and wants a key. `000`
means nothing answered yet; the first start can take a minute or two.

Still `000` after 3 minutes? Or does `proxy` show `Restarting`, or
`Up` with a few seconds that keep going back to 0? That is a restart
loop. Run `docker compose logs proxy` and see
[Troubleshooting: the proxy](#troubleshooting-the-proxy).

## Step 5: Mint your first key

Pick a short seat name, such as `laptop`. Each name can belong to
only one key at a time. Mint the key and save it in a file named
after the seat:

```sh
key=$(./mint-key.sh laptop) && echo "$key" > laptop.key
chmod 600 laptop.key
cat laptop.key
```

**Success:** one line starting with `sk-`: the laptop's key, with the
defaults from Step 3c. The file is saved only if minting worked. If
not, the reason is on screen above the `cat` output; see
[Troubleshooting: minting](#troubleshooting-minting). Careful when you
run this again for the same name: minting fails with
`already exists`, and `cat` still shows the old key from last time.

Skipped jq in Step 1? Wherever this guide says `./mint-key.sh`, type
`docker compose run --rm --no-deps frontdesk mint-key` (Docker builds
Front Desk's image the first time), or `./mint-key` if you have
[the Go version](#the-go-version).

`.gitignore` keeps `.key` files out of git. The master key is never
printed in full: the script hands it to curl through standard input,
so it is not on the command line that `ps` shows, nor in your shell
history. (A `401` error shows only its last 4 characters and a hash.)

## Step 6: The 3-check test

This checks the three promises of "one lock, many keys": a key works
for its allowed models, is refused for others, and stops working once
revoked. First mint a throwaway key that may use only `local`, with a
1 dollar budget, held in a shell variable called `KEY`:

```sh
KEY=$(./mint-key.sh test-seat --models local --budget 1)
[[ $KEY == sk-* ]] && echo "key made" || echo "mint failed"
```

**Success:** `key made`. On `mint failed`, read the error above it.
`already exists` means an earlier test stopped partway: run
`./mint-key.sh --revoke-seat test-seat` and mint again. Run all three
checks in this same terminal window; `KEY` exists only there.

### Check 1: the key works

```sh
curl -s -m 120 localhost:4000/v1/chat/completions \
  -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"local","messages":[{"role":"user","content":"hi"}]}'
```

The first answer can take up to a minute while Ollama loads the
model. **Pass:** a block of **JSON** (structured text) with
`"choices"` in it and a `"content"` holding the reply. **Fail:** JSON
with `"error"`; its message usually says why. Nothing at all after 2
minutes (`-m 120` makes curl give up quietly) means the proxy cannot
reach Ollama. See
[Troubleshooting: the proxy](#troubleshooting-the-proxy).

### Check 2: the wrong model is refused

Same key, but ask for `claude`. The status code prints last:

```sh
curl -s -w '\n%{http_code}\n' localhost:4000/v1/chat/completions \
  -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"claude","messages":[{"role":"user","content":"hi"}]}'
```

**Pass:** an error saying the model `is not available for this API
key`, then `403` (with an older `LITELLM_TAG`, maybe `401`).
**Fail:** a real answer, or an error about a missing or bad Anthropic
key. Either means the key was not stopped at the door.

### Check 3: a revoked key is refused

```sh
./mint-key.sh --revoke "$KEY"
curl -s -o /dev/null -w '%{http_code}\n' localhost:4000/v1/models \
  -H "Authorization: Bearer $KEY"
```

**Pass:** `revoked`, then `401`. All three pass? Your proxy works,
and the test key is already gone.

**If a check fails:** clean up with
`./mint-key.sh --revoke-seat test-seat` (an error mentioning `404`
just means it was already gone), fix the cause with the
Troubleshooting sections, and run the test again from the top.

**Only set up `claude`?** Swap `local` and `claude` everywhere in the
test, including after `--models`. Check 1 then spends a tiny part of
the 1 dollar budget.

## Step 7: Give the key to a seat

Send the seat three things, privately, not in a shared chat:

- **Base URL**, the address an app sends requests to:
  `http://<proxy-ip>:4000`
- **API key:** the key you minted for it (`cat laptop.key`)
- **Model:** `local`, or `claude` if its key allows it

The seat types them into any **OpenAI-compatible** app or code
library: one that speaks OpenAI's request format and lets you set a
base URL (also called "API base" or "endpoint") and an API key. Some
apps want `/v1` on the end; the proxy accepts both. A seat never
needs `seats.env`, the master key, or this repo.

### Find the proxy address

On the proxy machine, run `hostname -I` (Linux) or
`ipconfig getifaddr en0` (Mac; if it prints nothing, try `en1`), or
check your router's device list. With WSL, `hostname -I` shows only
WSL's own `172.` address: open Windows PowerShell, run `ipconfig`,
and use the IPv4 address of your Wi-Fi or Ethernet adapter. Use the
address your router gave the machine, usually starting `192.168.` or
`10.`. Skip Docker's own addresses, like `172.17.0.1` and
`172.18.0.1`, and WSL's. Unsure? Check your router's device list.

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

```sh
./mint-key.sh <seat> [--models a,b] [--budget USD] [--duration 30d]
                     [--budget-duration 1d]
```

Parts in `[ ]` are optional; left out, they use the `DEFAULT_` lines
in `seats.env`, which the script rereads every run.

- `--models local,claude`: the aliases the key may use, split by
  commas. Name at least one. Names are not checked: a typo such as
  `lcoal` makes a key that gets `403` for everything. Copy them from
  the `model_name:` lines in `config.yaml`.
- `--budget 20`: the most it may spend in total, in US dollars. Type
  the number only. Never type `$`: the shell quietly mangles it. In
  bash, `$20` becomes `0` (a key refused every time) and `$25` becomes
  `5`; in zsh, the Mac default, `$20` vanishes.
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

For example, a desk computer that may use both models, saved the same
way as in Step 5:

```sh
key=$(./mint-key.sh desk --models local,claude --budget 20 \
  --duration 90d) && echo "$key" > desk.key
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

### The Go version

`mint-key` (`mint-key.exe` on Windows) is `mint-key.sh` rewritten in
**Go**, a programming language whose programs are a single file that
needs nothing else installed: no bash, curl or jq. It runs in Windows
cmd and PowerShell (Windows' two terminals), on macOS and on Linux.
It takes the same flags, prints the same things and reads the same
`seats.env`. On Linux and macOS, `./mint-key.sh` is all you need; the
Go version is there for Windows, and for machines without bash, curl
or jq.

**Get it** in one of three ways:

- **Download it**, once the repo has a release (until then, its
  **Releases** list on GitHub is empty: use one of the other ways).
  Under Releases, pick the file for your computer: `windows`,
  `darwin` (macOS) or `linux`, then `amd64` (most PCs, older Macs)
  or `arm64` (Macs with Apple chips, a 64-bit Raspberry Pi, Windows
  on Arm). Unpack it, and move `mint-key` (or `mint-key.exe`) into
  this folder, next to `seats.env`. The release's `SHA256SUMS` file
  lists a **checksum** (a fingerprint of a file) for each download.
  To check yours arrived whole, run this where you downloaded both:
  `sha256sum -c SHA256SUMS --ignore-missing` should say `OK`. On a
  Mac, type `shasum -a 256` in place of `sha256sum`. In PowerShell,
  run `Get-FileHash <file>`; in cmd,
  `certutil -hashfile <file> SHA256`. Compare what it prints with
  that file's line in `SHA256SUMS`; capitals do not matter.
- **Or build it**, with Go 1.26 or newer (from go.dev), in this
  folder: `go build ./cmd/mint-key`. That makes `mint-key`, or
  `mint-key.exe` on Windows. In WSL,
  `GOOS=windows go build ./cmd/mint-key` makes `mint-key.exe` for
  PowerShell and cmd.
- **Or run it in Docker**, on the proxy machine: Front Desk's image
  holds it, so there is nothing else to install.
  `docker compose run --rm --no-deps frontdesk mint-key laptop`
  mints (start the proxy first), and
  [gui/README.md](gui/README.md#mint-keys-through-docker) shows more.

**Run it** from this folder. On Linux and macOS, every
`./mint-key.sh` line in this guide works with the Go version in its
place. The same mint on Linux or macOS, in Windows cmd, and in
PowerShell:

```text
./mint-key laptop --budget 2
mint-key.exe laptop --budget 2
.\mint-key.exe laptop --budget 2
```

To save the key in PowerShell, only if minting worked:

```powershell
$key = .\mint-key.exe laptop
if ($LASTEXITCODE -eq 0) { $key | Set-Content laptop.key }
```

To revoke by the saved key in PowerShell:
`.\mint-key.exe --revoke (Get-Content laptop.key)`. Put quotes
around a list of models, as in `--models 'local,claude'`: there, a
bare comma splits it in two. The guide's other lines around
`./mint-key.sh`, such as the 3-check test, are for bash: run those
in WSL, or on Linux or a Mac.

It looks for `seats.env` in the folder you run it from, then in its
own folder. To use another file, set `SEATS_ENV` to its path: in
PowerShell, `$env:SEATS_ENV = 'C:\<path>\seats.env'`; in cmd,
`set SEATS_ENV=C:\<path>\seats.env`. On a Mac, a downloaded
`mint-key` may be stopped as coming from an "unidentified
developer"; run `xattr -d com.apple.quarantine mint-key` once to
allow it.

**On Windows**, the repo folder is inside WSL (Step 1), and there
`./mint-key.sh` works as on Linux. To mint from PowerShell instead,
go to that folder by its Windows name: in WSL, `wslpath -w .` prints
it, like `\\wsl.localhost\Ubuntu\home\<you>\local-verse-proxy`.
In PowerShell, `cd` there and run `.\mint-key.exe`. cmd cannot use
such a folder as its own: type `pushd` in place of `cd`, and it
lends the folder a drive letter.

**How it differs**, on purpose:

- Errors from the proxy start with `the proxy returned error: 400
  (Bad Request)`, where curl says `curl: (22) The requested URL
  returned error: 400`; a hint may follow. LiteLLM's own message
  still comes on the next line.
- With no answer at all, it says `Failed to connect to the proxy at
  <address> (<reason>)` and what to do, where curl says `(7)`, `(52)`
  or `(56)`.
- A failed mint exits with code 1, where the script's code is 4 (from
  jq). Usage and value mistakes are 2 in both.
- If the proxy says yes but sends no key, it says so and exits 1; the
  script prints `null`.
- It looks for `seats.env` in the current folder before its own.
- It never runs anything in `seats.env`. Where bash would run
  `$(...)` or backticks, `mint-key` refuses, naming the line. Windows
  line endings (from Notepad, say) are fine; bash trips on them.
- It gives up after 25 seconds without an answer; curl keeps waiting.
  A proxy that got the request may still make the key after that, so
  the message says so: revoke the seat, then mint again.
- A `PROXY_URL` that ends in `/` works; the script gets `404`.
- Like curl, it goes through the **web proxy** (a middleman some
  networks put in front of all web traffic) set in `HTTPS_PROXY` or
  `HTTP_PROXY`, except for the hosts in `NO_PROXY`. Unlike curl, it
  never does so for `localhost` or `127.0.0.1`, it reads an
  uppercase `HTTP_PROXY` (curl reads only `http_proxy`), and it
  ignores `ALL_PROXY`.
- It does not follow a redirect (an answer that says "ask elsewhere");
  it says where the redirect pointed, so the master key goes nowhere
  else.
- A budget too long for a computer to hold as a number (hundreds of
  digits) is refused as a bad `--budget`.
- If a message from the proxy ever held the master key, it would show
  only as `sk-...` and the last 4 characters.
- `mint-key --version` prints its version.

## Revoking a key

Revoke a key when a device is lost, someone leaves, or a key may have
leaked. Use one of these:

```sh
./mint-key.sh --revoke-seat laptop          # by seat name
./mint-key.sh --revoke "$(cat laptop.key)"  # or by the key itself
```

**Success:** `revoked seat laptop` or `revoked`. The seat name works
even if the key itself is lost. That key now gets `401`, every other
key keeps working, and the seat name is free again; delete its file
with `rm laptop.key`. An error mentioning `404` or `No keys found`
means there was no such key: already revoked, or mistyped. If `cat`
says `No such file or directory` before the usage text, the key file
is gone and nothing was revoked: use the seat name.

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
until curl -sfo /dev/null localhost:4000/health/liveliness; do sleep 3; done
key=$(./mint-key.sh den --models local,local-small) && echo "$key" > den.key
```

Removing a model works the same way. Keys made with it still list it
in `/v1/models`, but calls to it get `400` `Invalid model name`.

## Day-to-day commands

```sh
docker compose up -d          # start
docker compose ps             # is it running?
docker compose logs -f proxy  # watch messages (Ctrl+C to stop)
docker compose down           # stop (keys are kept)
docker compose up -d --force-recreate proxy   # apply edits
```

- After starting, the proxy needs about 30 seconds before it
  answers; minting before that fails with `curl: (7)` or `(56)`.
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
   keys keep working, and so does any key someone minted with the old master
   key. In a browser on the proxy machine, open
   `http://localhost:4000/ui`, log in as `admin` with the new master
   key as password, and delete every key you did not make.

## Troubleshooting: setting up

- **`Cannot connect to the Docker daemon`** or **`failed to connect
  to the docker API`**: Docker is not running.
  On Linux, run `sudo systemctl enable --now docker`; or start Docker
  Desktop.
- **`permission denied` mentioning `docker.sock`**: your user may not
  use Docker yet. Put `sudo` before `docker`, or run
  `sudo usermod -aG docker $USER`, then log out and back in.
- **`'compose' is not a docker command`**, `unknown command`, or
  `unknown shorthand flag: 'd'`: Docker came without Compose, as
  distro packages such as Ubuntu's `docker.io` do. On Ubuntu, run
  `sudo apt install docker-compose-v2`; elsewhere, install Docker from
  docker.com, which includes it.
- **`no configuration file provided`**, or
  `./mint-key.sh: No such file or directory`: you are not in the repo
  folder. `cd` into it.
- **`env file .../seats.env not found`** or **`missing ...`**: do
  Step 3.
- **`./mint-key.sh: Permission denied`**: the file lost its "may run"
  flag, often after a download. Run `chmod +x mint-key.sh` once.
- **`curl: option --fail-with-body: is unknown`**: your curl is older
  than 7.76. Use a newer system as the proxy machine (Ubuntu 22.04 or
  later, say), or install a newer curl.

## Troubleshooting: minting

- **`LITELLM_MASTER_KEY: not set in ...`**: the master key line in
  `seats.env` is empty. Rerun the Step 3a block (it fills only empty
  or placeholder lines), then
  `docker compose up -d --force-recreate proxy`. Keys you already
  made keep working.
- **`replace the change-me values in ... first`**: a placeholder is
  left in `seats.env`. If you never ran `docker compose up`, do Step
  3a and carry on. If you did, Postgres already saved the template
  password, so start over (no keys exist yet):
  `docker compose down -v`, Step 3a, then `docker compose up -d`.
- **Usage text** (`Mint a scoped key for one seat...`): the seat name
  is missing or starts with `-`, an option is misspelled or has no
  value, or a space slipped into `--models local, claude`.
- **`jq: command not found`** (or `curl: command not found`):
  `mint-key.sh` needs it. Install it as Step 1 shows, or mint with
  [the Go version](#the-go-version) or
  `docker compose run --rm --no-deps frontdesk mint-key <seat>`
  ([Front Desk in Docker](gui/README.md#mint-keys-through-docker)),
  which need neither.
- **`curl: (7) Failed to connect`**, **`(52) Empty reply from server`**
  or **`(56) Recv failure`**: the proxy is not running, or is still
  starting. Wait until [Is it up?](#is-it-up) prints `401`, then mint
  again.
- **`curl: (22)` with `400`, then `... already exists`**: that seat
  name is taken. Run `./mint-key.sh --revoke-seat <seat>`, or pick a
  new name.
- **With the Go `mint-key`**, the curl lines in this list read
  differently: `Failed to connect to the proxy at ...` for `(7)`,
  `(52)` and `(56)`, and `the proxy returned error: 400` (or `401`)
  for `curl: (22)`. Causes and fixes are the same.
- **`seats.env line 9: $(...) would run a command`**, or another
  `seats.env line` message from the Go `mint-key`: that line has
  shell code in it, which `mint-key.sh` would run. Make it a plain
  `NAME=value` line.
- **`seats.env seems to be saved as UTF-16`**: Windows PowerShell's
  `>` and Notepad's "Unicode" save files that way. Save it again as
  UTF-8 (in Notepad: File, Save as, then Encoding: UTF-8).
- **`(no answer within 25 seconds)`**, then
  **`it may have made the key anyway`**, from the Go `mint-key`: the
  proxy is slow, perhaps busy or just started. The key may be made
  but was never shown: run the same program with
  `--revoke-seat <seat>` (`./mint-key --revoke-seat <seat>`; in
  PowerShell `.\mint-key.exe --revoke-seat <seat>`; in Docker
  `docker compose run --rm --no-deps frontdesk mint-key
  --revoke-seat <seat>`), then mint again.
- **`curl: (22)` with `401`**, then `Invalid proxy server token`:
  the proxy did not accept the master key, usually because
  `seats.env` changed after it started. Run
  `docker compose up -d --force-recreate proxy`. Minting through
  Front Desk in Docker? Run `docker compose --profile frontdesk up -d`
  instead. The message shows only the key's last 4 characters and a
  hash, not the key.

## Troubleshooting: the proxy

Start with `docker compose ps`, then `docker compose logs proxy`. A
**restart loop** shows as `Restarting`, or as `Up` with a few seconds
that keep going back to 0.

- **`proxy` is in a restart loop, and its log says it
  `refused to start` over the master key**: `LITELLM_MASTER_KEY` in
  `seats.env` is empty, or a well-known example such as `sk-1234`
  (`publicly known default`). Empty that line, rerun the Step 3a
  block, then `docker compose up -d --force-recreate proxy`. Ignore
  the log's advice about a `.env` file, and never set the
  `DANGEROUSLY_PERMIT` option it offers.
- **`proxy` is in a restart loop, and `docker compose logs db` says
  `password authentication failed`**: Postgres kept the password
  from its first start; put it back in `POSTGRES_PASSWORD` and
  `DATABASE_URL`. If no keys are worth keeping yet (say you started
  once before filling in `seats.env`), `docker compose down -v` and
  then `docker compose up -d` start the database fresh, erasing all
  keys.
- **Check 1 fails with `Cannot connect to host`, or prints nothing
  until curl gives up**: check that
  `OLLAMA_API_BASE` is not `localhost`, the Step 1 Ollama check
  passes, `OLLAMA_HOST=0.0.0.0` is set on the Ollama machine
  ([how](#letting-the-proxy-reach-ollama)), and the model is
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
  4000. If the Step 4 knock works on the proxy machine but the seat
  check fails, it is a network problem, not a key problem.
- **`No such file` naming `proxy-ip` or similar**: a placeholder was
  typed as is. Put in the real value, without `< >`. A `401` saying
  `expected to start with 'sk-'` means the same for `<seat-key>`:
  paste the whole key from `cat <seat>.key`.
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
change it, put `LITELLM_TAG=<tag>` in a file named `.env` next to
`compose.yaml`, or set it in your shell. It does not work in
`seats.env`, which is for the containers, not for Compose. Then run
`docker compose pull` and `docker compose up -d`, and rerun the
3-check test: status codes have changed between releases.

**Does a seat ever see my real keys?** No, only its own virtual key.
The real keys stay in `seats.env` on the proxy machine. There,
`compose.yaml` hands that file to every container (Front Desk's too,
if you run it), so the `db` container can see every secret too.

**Can I run `mint-key.sh` from another computer?** Best not: you
would copy every secret there, and the master key would cross your
network over plain HTTP.

## File map

- `README.md`: this guide.
- `config.yaml`: the models, and the names of the settings that hold
  their secrets. No secrets inside, so it is safe to commit.
- `seats.env.example`: the template for your secrets file.
- `seats.env`: your real secrets, made in Step 3. Never commit it.
- `mint-key.sh`: mints and revokes seat keys, using the master key.
- `cmd/mint-key/`: the same in Go, for Windows and machines without
  bash. See [the Go version](#the-go-version).
- `internal/`: Go code that `mint-key` uses, for later Go programs to
  share too: `seats` reads `seats.env`, `litellm` talks to the proxy.
- `go.mod`: names the Go module (a Go project); `go build` reads it.
- `tests/mint-parity.sh`: runs `mint-key.sh` and the Go `mint-key`
  side by side against your running proxy, and compares them.
- `.github/workflows/`: what GitHub runs on every push (`ci.yml`:
  tests and checks), and on a version tag like `v1.0.0`
  (`release.yml`: builds the downloads).
- `gui/`: Front Desk, an optional web page for key cards and the
  tally. It runs `mint-key.sh` for you (in Docker, the Go
  `mint-key`). See
  [gui/README.md](gui/README.md).
- `Dockerfile`: builds Front Desk's Docker image, which holds one Go
  program, `lvp`, and nothing else (no Python). The practice model
  server of the office game runs from the same image.
- `compose.yaml`: runs the proxy and Postgres, with the database in
  the `pgdata` volume; with `--profile frontdesk`, Front Desk too.
- `.env` (hidden, optional): settings for Docker Compose itself, such
  as `LITELLM_TAG` and Front Desk's `FRONTDESK_` settings. You make
  it; git ignores it.
- `.gitignore` (hidden; `ls -a` shows it): keeps `seats.env` and its
  copies, `.env`, `*.key` files and built `mint-key` programs out of
  git.
- `.dockerignore` (hidden): what Docker may not see when it builds an
  image. It keeps `seats.env`, `.env`, keys and game state out.
- `*.key`: seat keys you saved. Never commit them.
- `game/`: an optional office game played with seat keys, where
  finished work scores and burned tokens do not. See
  [game/README.md](game/README.md).

## Glossary

- **API key:** a secret string that lets a program use a service.
- **Budget:** the most a key may spend, in US dollars, over its life
  (or per refill, with `--budget-duration`).
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
- **Profile:** a named group of optional containers in
  `compose.yaml`, like `frontdesk`, that start only when asked for.
- **Proxy:** a middleman between apps and the real AI services.
- **Salt key:** `LITELLM_SALT_KEY`; encrypts stored credentials.
- **Seat:** one person, computer or app with its own key.
- **UTC:** world time, the same everywhere. Midnight UTC is 8 PM the
  day before in New York (in summer) and 1 AM in Paris (in winter).
  Front Desk shows refill times in your own time.
- **Virtual key:** a seat's own key, limited by models, budget and
  expiry, that works only through this proxy.
- **WSL:** Windows Subsystem for Linux, a Linux that runs inside
  Windows. `wsl --install` in PowerShell sets it up.

## License

To be decided.

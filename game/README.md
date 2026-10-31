# The office game

A small game on top of this proxy. Each seat is a player, its AI
budget is **mana**, and the score is **finished work**, not tokens.

It exists to make one point with real numbers: if you reward people
for using AI a lot, you get a lot of AI use, not a lot of work. The
game's scoring rewards work that got done, done cheaply.

> **Status: a prototype.** Practice mode runs end to end against the
> real LiteLLM v1.106.6 and Postgres, with bots as players and a fake
> model server. Real mode, for people, is a plan, not a feature yet.

## In plain terms

Picture the front desk from the main README: one desk, one pass per
person. This game gives some of those passes a daily allowance,
called **mana**, and posts jobs, called **quests**.

Robot workers take the quests, use AI to do them, and hand them in.
A pretend manager approves the work or sends it back. A worker that
runs out of mana is turned away at the desk until tomorrow.

At the end of each day there are two boards. One ranks who used the
most AI. The other ranks who finished the most work, cheaply. They
do not agree, and that is the point: the robots that burn the most
AI finish the least.

Teams are called **guilds**. They are ranked by points per member,
so a big team does not win just by being big.

## Contents

- [In plain terms](#in-plain-terms)
- [The idea in one table](#the-idea-in-one-table)
- [Why: the Goodhart trap](#why-the-goodhart-trap)
- [Try it: practice mode](#try-it-practice-mode)
- [How a day works](#how-a-day-works)
- [How scoring works](#how-scoring-works), and
  [why it works that way](#why-it-works-that-way)
- [The rules file](#the-rules-file)
- [Privacy and consent](#privacy-and-consent)
- [What is real, what is simulated](#what-is-real-what-is-simulated)
- [How the mana refills](#how-the-mana-refills)
- [Files](#files), [troubleshooting](#troubleshooting)

## The idea in one table

| In the game   | In the proxy                                |
|---------------|---------------------------------------------|
| A player      | A seat with its own key (`game-fern`)       |
| Mana          | The key's budget. 1 mana = $0.00001         |
| Daily mana    | `--budget-duration`: LiteLLM refills it     |
| Out of mana   | The proxy refuses the call (HTTP `422`)     |
| A quest       | A task worth points, done with model calls  |
| Proof of work | Calls tagged with the quest, in spend logs  |
| A guild       | A team. Scores are per member               |
| The manager   | Approves work, or sends it back for a redo  |

Two models: `local`, cheap, and `pro`, about 10 times the price. Work
done on `pro` is approved more often, but with these rules it never
pays for itself (see [why](#why-it-works-that-way)).

## Why: the Goodhart trap

Goodhart's law: when a measure becomes a target, it stops being a
good measure. "Tokens used" is easy to count, so it is tempting to
praise the people at the top of that list. But long prompts, repeat
calls and the priciest model all push you up it, and none of them
finish anything.

Here are both boards from a real practice run, side by side. Same
office, same three days, same bots:

```text
Office 975865, practice week: after day 3 of 5 (2m days)
1 mana = $0.00001; daily mana 1,500 per seat

GUILDS: points per member (finished, efficient work)
  foxes    3 members   512.9  ##############################
  owls     4 members   503.7  #############################
  bees     3 members   353.0  #####################

WHO FINISHED WORK (game score)      | WHO BURNED TOKENS (naive board)
 # seat   persona  done  eff    pts |  # seat      tokens   mana  done
 1 moss   frugal     31 1.50 1369.5 |  1 spike    286,718  4,604   13
 2 fern   frugal     35 1.50 1185.0 |  2 blitz    190,125  4,581    9
 3 ben    balanced   40 1.03  822.8 |  3 ben       51,161  1,873   40
 4 bea    balanced   28 0.75  529.5 |  4 bea       47,487  3,029   28
 5 gus    gearhead   23 0.33  169.0 |  5 gus       33,384  4,604   23
 6 gia    gearhead   22 0.33  156.1 |  6 gia       33,131  4,577   22
 7 sid    slacker     3 1.50  131.2 |  7 fern      26,620    356   35
 8 spike  spammy     13 0.33  107.1 |  8 moss      25,807    342   31
 9 blitz  spammy      9 0.33   80.2 |  9 sid        4,043     55    3
10 sal    slacker     3 1.37   62.0 | 10 sal        3,099     43    3

GOODHART CHECK
  Most tokens: spike (spammy), 286,718 tokens, 13 done, 107 pts
  Most mana:   spike (spammy), 4,604 mana, 13 done, 107 pts
  Top score:   moss (frugal), 25,807 tokens, 31 done, 1370 pts
  spike burned 11.1x the tokens of moss, for 0.42x the finished quests
  and 0.08x the points.
  Rank match, tokens vs score: -0.07 (+1 same order, 0 none, -1 reversed)
```

How to read it: **done** is quests that counted, **eff** their
average efficiency, **pts** the points (see
[How scoring works](#how-scoring-works)). **tokens** and **mana** are
what each seat spent on models; 1 mana = $0.00001. The mana column
is the spend board.

Spammy `spike` tops the token board with 11 times the tokens of
frugal `moss`, yet finished fewer than half as many quests, for a
twelfth of the points. The frugal bots sit near the bottom of the
token board and on top of the real one. The two orders do not agree
at all: a rank match of -0.07, where +1 would mean the same order.

Ranking by spend is no better. Each of the four bots that hit its
daily mana cap every day (the gearheads and the spammy ones) spent
about 13 times the mana of a frugal bot, and scored about a tenth as
much. Even slacker `sid`, with 3 quests done cheaply, beat both
spammy bots. Pay people by either naive board and you buy waste.

Two more things to spot. `ben` finished the most quests, 40, but
most at about par, and some on `pro` at many times par. The frugal
bots did theirs for under half of par, so they score more. And
`moss` ranks above `fern` with fewer quests: it
drew more large ones, which pay a bit more per call
(`board --seat moss` lists every quest).

The day summary shows the proxy's real refusals. These bots ran out
of mana and sat idle for the rest of day 3:

```text
Day 3 done: bots worked 82 s, 342 calls answered.
  spend logs: 342 of 342 rows after 2 s
  quests: 100 posted, 74 counted, 3 thrown out (too few calls), 2 given up,
          8 unfinished, 13 never picked up
  blitz: out of mana at 0:17 into the day (HTTP 422), idle after that
  gus: out of mana at 0:52 into the day (HTTP 422), idle after that
  gia: out of mana at 0:52 into the day (HTTP 422), idle after that
  spike: out of mana at 0:57 into the day (HTTP 422), idle after that
  bea: out of mana at 1:15 into the day (HTTP 422), idle after that
```

## Try it: practice mode

You need the proxy from the [main README](../README.md) working
first (Steps 1 to 4). Practice mode needs no Ollama and no API key.
The game needs Python 3.11 or newer:

```sh
python3 --version   # 3.11 or newer
```

Older? On Ubuntu 22.04, `sudo apt install python3.11`, then type
`python3.11` where this guide says `python3`. On a Mac,
`brew install python`.

The game makes and cancels its seats' keys with `mint-key.sh`, so it
also needs bash, curl and jq
([main README, Step 1](../README.md#step-1-check-your-tools)).

**Heads up:** while practice mode is on, the proxy serves only the
two fake practice models, to every seat, and real seats pay the
practice prices from their own budgets (tiny amounts). Try it on a
spare machine, or when nobody needs the real models.

From the repo folder, run:

```sh
echo 'COMPOSE_FILE=compose.yaml:game/compose.game.yaml' >> .env
docker compose up -d --force-recreate proxy
# about 30 s; still waiting after 3 min? Ctrl+C, see below
until curl -sfo /dev/null localhost:4000/health/liveliness; do sleep 3; done
docker compose ps
python3 game/office.py setup
python3 game/office.py run --days 2
python3 game/office.py board
```

The first line tells Docker Compose to add the game's overlay file,
`compose.game.yaml`, on top of `compose.yaml`. Your proxy now uses
`game/config.game.yaml` and a fake model server, `fake-ollama`. If
the `until` line never ends, see
[Is it up?](../README.md#is-it-up) in the main README.

**Success:** `docker compose ps` shows three rows, `db`, `proxy` and
`fake-ollama`, all `Up`. `fake-ollama` has no port: only the proxy
talks to it.

`setup` mints ten game seats, one per bot, and saves their keys in
`game/keys/`. **Success:** ten `game-...` lines, then
`Office ... is open.` If you use [Front Desk](../gui/README.md), the
seats show there as office game seats. Don't cancel them there
mid-week: `reset` removes them all when the week ends.

`run` plays two 2-minute days, printing a summary and both boards
after each day. It takes about 5 minutes: the first day may wait up
to 2 minutes for the clock. **Success:** two `Day N done` summaries,
each with `spend logs: N of N rows`. `board` prints the boards
again.

### Back to normal

```sh
python3 game/office.py reset
sed -i.bak '/compose.game.yaml/d' .env && rm .env.bak
docker compose up -d --force-recreate --remove-orphans proxy
```

`reset` revokes the game seats and wipes the game. If it cannot
reach the proxy, it says so and wipes nothing. The `sed` line removes
the line you added to `.env`, and `--remove-orphans` stops
`fake-ollama`. Your real seats keep their keys; only the practice
prices they paid stay on their budgets.

## How a day works

1. At the start of the day LiteLLM refills every game seat to 1,500
   mana ($0.015). The game waits until it sees that happen.
2. The game posts quests: small, medium and large, each worth points.
3. Each bot takes a quest and does the work: model calls through the
   proxy, with its own key. Every call is tagged with the quest and
   the seat, so the proxy's spend logs know who spent what, on what.
4. The bot hands the quest in. The manager approves it, or sends it
   back for a redo, which costs more calls.
5. A bot that runs out of mana gets the proxy's real refusal, HTTP
   `422` `budget_exceeded`, and sits idle until tomorrow. LiteLLM
   checks the budget before each call, so the call that crosses the
   line still goes through: a seat can end a little over 1,500.
6. 35 seconds before the day ends, the bots stop. The scorekeeper
   waits until every call is in the spend logs, then scores the day.

There are five personas, set in `rules.toml`:

- **frugal**: short prompts, always `local`.
- **balanced**: medium prompts, `pro` for one quest in four.
- **spammy**: very long prompts, two calls per step, retries.
- **gearhead**: always `pro`.
- **slacker**: two quests a day, and sometimes hands one in after a
  single call, hoping nobody checks.

frugal, balanced and gearhead work at the same pace, so only their AI
habits differ. spammy's quick fire is part of spamming.

## How scoring works

For each quest a player hands in:

```text
points     = value x efficiency x (1 + streak bonus)
efficiency = par mana / mana spent on it, kept in 0.33 .. 1.5
```

**Par mana** is what a quest should cost, done sensibly. Each quest
size has its own value, par and `calls` in `rules.toml`. An example,
a small quest: worth 10 points, needs 2 calls, par 12 mana.

- Done for 8 mana: efficiency 12 / 8 = 1.5, so 15 points.
- Done for 24 mana: 0.5, so 5 points.
- Done for 40 mana: 12 / 40 = 0.3, under the floor, so 0.33 and
  3.3 points.

A quest **counts** only if:

- the manager approved it, and
- the spend logs show at least the quest's `calls` answered requests
  tagged with that quest, paid for by that player's own key.

**Streak bonus:** +10% for each day in a row before today with at
least one counted quest, up to +50%. Day 1 has no streak yet, so a
5-day week tops out at +40%. **Guild score:** the guild's points
divided by its number of members.

The **naive board** ranks players by total tokens, with mana spent
beside it. It is there only to be compared with.

To see where one seat's points came from, quest by quest:

```sh
python3 game/office.py board --seat fern
```

A large quest pays a bit more per call than a small one (50 points
for 7 calls, against 10 for 2). So a seat can rank above its twin
with fewer quests done: it drew more large ones. `--seat` shows it.

### Why it works that way

- **Value first.** Only handed-in, approved work scores. Calls on
  their own score nothing.
- **Efficiency, kept in a range.** Cheaper work scores more, so
  burning mana never pays. The floor of 0.33, hit at 3 times par,
  means work done the expensive way still counts a third. The
  ceiling of 1.5 means skimping past a point earns nothing more.
- **The daily mana cap is a brake too.** A seat that burns its mana
  early sits idle for the rest of the day. With bigger budgets the
  cap bites less, which is why the efficiency floor is low.
- **`pro` is a trap here.** At 10 times the price, a `pro` quest
  lands on the efficiency floor. Its better approval rate hardly
  helps: rejected `local` work is redone at half the size, up to 3
  tries, and nearly always passes in the end. With these numbers
  `pro` would have to cost about the same as `local` to pay. That is
  the lesson: the priciest model is no shortcut to a better score.
- **Proof from the proxy.** The count comes from LiteLLM's own
  spend logs, not from what a player says. Handing in a quest with
  too few calls behind it is "thrown out". The seat tag must match
  the key that paid, so nobody can tag calls in someone else's name.
- **Guilds over individuals.** Points per member, so a team does not
  win by being big, and nobody has to be singled out.
- **A small streak.** It rewards steady work. The cap keeps it from
  snowballing.

## The rules file

Every number lives in `game/rules.toml`, with comments:

- `[day]`: day length (practice `"2m"`; it is also each key's
  `--budget-duration`), when bots stop, days per week.
- `[mana]`: what 1 mana is worth in dollars, and the daily mana.
- `[quests]`: how many are posted, and each size's value, `calls`
  (the proof needed) and par mana. Redo cost, attempts allowed.
- `[score]`: the efficiency range, streak bonus, how long to wait
  for the spend logs.
- `[board]`: `individuals = false` shows only the guild board.
- `[personas.*]` and `[roster]`: who plays, how, and in which guild.

Change the rules between weeks: `reset`, edit, then `setup`.

## Privacy and consent

The game is meant to be played with people some day, so it is built
to be kind to them from the start.

- **Opt in.** Game seats are separate keys named `game-...`. Only
  people who want to play get one. Never turn someone's everyday key
  into a scored one without asking.
- **Guilds first.** The board leads with guilds. For a real office,
  set `individuals = false` in `[board]`.
- **Weekly reset, by hand.** `reset` is not automatic: someone runs
  it when the week ends. It revokes every game seat and deletes the
  game's own database, `office.db`. No score carries over.
- **Prompts are not stored.** `config.game.yaml` sets
  `store_prompts_in_spend_logs: false`, so LiteLLM's spend logs keep
  tags, token counts and spend, never the text. The game's own
  `office.db` keeps only counts and scores. `fake-ollama` logs
  nothing.
- **Read only.** The scorekeeper reads Postgres inside a `READ ONLY`
  transaction. It cannot change a budget or a key.

**What stays in the proxy.** `reset` wipes the game, not the
proxy's own records. `LiteLLM_SpendLogs` keeps a row per game call
(seat name, tags, tokens, spend), and `LiteLLM_DailyTagSpend` keeps
daily totals per seat tag and quest tag, with no time limit.
Whoever runs the proxy can see them, as for every key, game or not.
LiteLLM also keeps a record of each revoked game key (its name) and
per-key daily totals; the retention settings below do not remove
those.

To expire them, uncomment the two `maximum_..._retention_period`
lines in `config.game.yaml` (`"7d"`). They are off on purpose: they
apply to **every seat**. Once a day LiteLLM then deletes all spend
logs and tag totals older than that, your real seats' history too.
A real mode with people should set them, and tell everyone.

## What is real, what is simulated

**Real:** the proxy (LiteLLM v1.106.6), the keys, the budgets and
their daily refill, the `422` refusals, the request tags, the spend
logs, token counts and costs as LiteLLM records them, and the
scoreboard read straight from Postgres.

**Simulated:** the models (`fake-ollama` answers with filler text,
but counts about 4 characters per token, so long prompts really cost
more), the work itself, the manager (a dice roll: persona skill, plus
a boost for `pro`), and the prices (shadow prices in
`config.game.yaml`; real models cost what they cost).

**Not built yet: real mode.** For people, a day would be `"1d"`
(LiteLLM refills at midnight UTC, world time) and players would tag
their own requests, for example with an `x-litellm-tags: quest:<id>`
header (LiteLLM reads tags from that header too). What is missing
is a way for people to take and hand in quests, and an honest way to
judge real work.

## How the mana refills

Each game key is minted with `--budget-duration` set to the day
length, so LiteLLM itself refills it. Two details matter:

- **LiteLLM's reset job** runs every ~10 minutes by default. The
  overlay sets `PROXY_BUDGET_RESCHEDULER_MIN_TIME` and `..._MAX_TIME`
  to 5 seconds.
- **Refills follow the clock.** `2m` refills at :00, :02, :04 and so
  on (UTC); `1d` at midnight. Each key's `budget_reset_at` in
  Postgres says when. The game waits for that moment, then starts the
  day once every seat's `budget_reset_at` has moved on.

In testing, the day started within about 5 seconds of the boundary
(the reset job runs every 5 s), and a key refused with `422` worked
again right after. If the job is ever more than 60 seconds late, the
game re-mints the late seats instead (revoke, then mint: a new key
starts with full mana), says so, and plays a shorter day. That path
was tested with the job slowed down.

**Spend logs are written in batches.** The overlay sets
`PROXY_BATCH_WRITE_AT=5` and `SPEND_LOG_QUEUE_SIZE_THRESHOLD=1`, so
they land within seconds. The scorekeeper still waits until the logs
hold every call the bots got an answer to (up to `flush_wait`).

## Files

- `README.md`: this guide.
- `office.py`: the game. `setup`, `run`, `board` (`--seat`,
  `--day`), `reset`.
- `rules.toml`: every number the game uses.
- `compose.game.yaml`: the overlay for practice mode.
- `config.game.yaml`: the practice models and their prices.
- `fake_ollama.py`: the fake model server, standard library only.
- `keys/`: game seat keys, made by `setup`. Git ignores `*.key`.
- `office.db`: the game's state (SQLite). Git ignores it.

## Troubleshooting

- **`office.py needs Python 3.11 or newer`**: see
  [Try it](#try-it-practice-mode) for how to get one.
- **`cannot read the proxy database`**: run the commands from the
  repo folder, with the proxy running (`docker compose ps`).
  `office.py` runs `docker` itself, so it cannot use `sudo`. If you
  need `sudo` for docker, add yourself to the `docker` group (main
  README,
  [Troubleshooting: setting up](../README.md#troubleshooting-setting-up)).
- **`reset` says `could not revoke`**: nothing was wiped. If the
  error is `curl: (7)`, the proxy is down: `docker compose up -d`,
  then `reset` again. If it is `401`, seats.env changed after the
  proxy started: see
  [Troubleshooting: minting](../README.md#troubleshooting-minting) in
  the main README.
- **`setup` fails with `already exists`**: a game seat is still
  there from an earlier try. Run `python3 game/office.py reset`.
- **`no key in the proxy for: ...`**: game keys were revoked or
  expired (they last 8 days). `reset`, then `setup`.
- **The day waits before it starts**: it starts on the clock, when
  LiteLLM refills the mana. At most one day length.
- **Every bot stops at once with `error ...`**: `HTTP 0` means
  nothing answered, so the proxy is down. Any other code: practice
  mode may be off, so `pro` does not exist. Check `.env` and
  `docker compose ps`.
- **Your real seats get filler text**: practice mode is still on.
  See [Back to normal](#back-to-normal).

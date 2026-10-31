# The office game

A small game on top of this proxy. Each seat is a player, its AI
budget is **mana**, and the score is **finished work**, not tokens.

It exists to make one point with real numbers: if you reward people
for using AI a lot, you get a lot of AI use, not a lot of work. The
game's scoring rewards work that got done, done cheaply.

> **Status: a prototype.** Practice mode runs end to end against the
> real LiteLLM v1.106.6 and Postgres, with bots as players and a fake
> model server. Real mode, for people, is a plan, not a feature yet.

## Contents

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
done on `pro` is approved more often. Using it is a real trade-off.

## Why: the Goodhart trap

Goodhart's law: when a measure becomes a target, it stops being a
good measure. "Tokens used" is easy to count, so it is tempting to
praise the people at the top of that list. But long prompts, repeat
calls and the priciest model all push you up it, and none of them
finish anything.

Here are both boards from a real practice run, side by side. Same
office, same two days, same bots:

```text
Office 5ed781, practice week: after day 2 of 5 (2m days)
1 mana = $0.00001; daily mana 1,500 per seat

GUILDS: points per member (finished, efficient work)
  owls     4 members   308.4  ##############################
  foxes    3 members   301.8  #############################
  bees     3 members   161.3  ################

WHO FINISHED WORK (game score)      | WHO BURNED TOKENS (naive board)
 # seat   persona  done  eff    pts |  # seat      tokens   mana  done
 1 fern   frugal     21 1.50  706.5 |  1 spike    256,716  3,015   12
 2 moss   frugal     20 1.50  695.2 |  2 blitz    126,153  3,144    7
 3 bea    balanced   19 1.02  382.9 |  3 bea       25,683    614   19
 4 ben    balanced   17 0.85  247.8 |  4 ben       25,641  1,977   17
 5 spike  spammy     12 0.50  178.5 |  5 gia       22,265  3,067   11
 6 gia    gearhead   11 0.50  165.8 |  6 gus       22,119  3,034   17
 7 gus    gearhead   17 0.50  144.2 |  7 fern      15,053    202   21
 8 blitz  spammy      7 0.50   70.5 |  8 moss      14,685    193   20
 9 sal    slacker     2 1.50   31.5 |  9 sal        1,285     17    2
10 sid    slacker     0 0.00    0.0 | 10 sid          802     11    0

GOODHART CHECK
  Token board #1: spike (spammy), 256,716 tokens, 12 done, 178 pts
  Spend board #1: blitz (spammy), 3,144 mana, 7 done, 70 pts
  Score board #1: fern (frugal), 15,053 tokens, 21 done, 706 pts
  spike burned 17.1x the tokens of fern, for 0.57x the finished quests
  and 0.25x the points.
  Rank match, tokens vs score: +0.24 (+1 same order, 0 none, -1 reversed)
```

Spammy `spike` tops the token board with 17 times the tokens of
frugal `fern`, yet finished fewer quests, for a quarter of the
points. The frugal bots sit near the bottom of the token board and on
top of the real one. The two orders barely agree: a rank match of
+0.24, where +1 would mean the same order.

Ranking by spend is no better. The four bots that hit their daily
mana cap spent 31 times the mana of the two frugal ones, and scored
40% as much. Pay people by either naive board and you buy waste.

The day summary shows the proxy's real refusals. These bots ran out
of mana and sat idle for the rest of day 2:

```text
Day 2 done: bots worked 84 s, 358 calls answered.
  spend logs: 358 of 358 rows after 0 s
  quests: 80 posted, 72 counted, 3 thrown out (too few calls), 0 given up,
          5 unfinished, 0 never picked up
  blitz: out of mana at 0:38 into the day (HTTP 422), idle after that
  gus: out of mana at 0:49 into the day (HTTP 422), idle after that
  gia: out of mana at 0:50 into the day (HTTP 422), idle after that
  spike: out of mana at 0:56 into the day (HTTP 422), idle after that
```

## Try it: practice mode

You need the proxy from the [main README](../README.md) working
first (Steps 1 to 4). Practice mode needs no Ollama and no API key.

**Heads up:** while practice mode is on, the proxy serves only the
two fake practice models, to every seat. Try it on a spare machine,
or when nobody needs the real models.

From the repo folder, run:

```sh
echo 'COMPOSE_FILE=compose.yaml:game/compose.game.yaml' >> .env
docker compose up -d --force-recreate proxy
until curl -sfo /dev/null localhost:4000/health/liveliness; do sleep 3; done
python3 game/office.py setup
python3 game/office.py run --days 2
python3 game/office.py board
```

The first line tells Docker Compose to add the game's overlay file,
`compose.game.yaml`, on top of `compose.yaml`. Your proxy now uses
`game/config.game.yaml` and a fake model server, `fake-ollama`.

`setup` mints ten game seats, one per bot, and saves their keys in
`game/keys/`. `run` plays two 2-minute days (the first may wait up to
2 minutes for the clock), printing a summary and both boards after
each day. `board` prints them again.

### Back to normal

```sh
python3 game/office.py reset
sed -i.bak '/compose.game.yaml/d' .env && rm .env.bak
docker compose up -d --force-recreate --remove-orphans proxy
```

`reset` revokes the game seats and wipes the game. The `sed` line
removes the line you added to `.env`, and `--remove-orphans` stops
`fake-ollama`. Your real seats were never touched.

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
   `422` `budget_exceeded`, and sits idle until tomorrow.
6. 35 seconds before the day ends, the bots stop. The scorekeeper
   waits until every call is in the spend logs, then scores the day.

There are five personas, set in `rules.toml`:

- **frugal**: short prompts, always `local`.
- **balanced**: medium prompts, `pro` for one quest in four.
- **spammy**: very long prompts, two calls per step, retries.
- **gearhead**: always `pro`.
- **slacker**: two quests a day, and sometimes hands one in after a
  single call, hoping nobody checks.

## How scoring works

For each quest a player hands in:

```text
points     = value x efficiency x (1 + streak bonus)
efficiency = par mana / mana spent on it, kept in 0.5 .. 1.5
```

A quest **counts** only if:

- the manager approved it, and
- the spend logs show at least the quest's `calls` answered requests
  tagged with that quest, paid for by that player's own key.

**Streak bonus:** +10% for each day in a row before today with at
least one counted quest, up to +50%. **Guild score:** the guild's
points divided by its number of members.

The **naive board** ranks players by total tokens, with mana spent
beside it. It is there only to be compared with.

### Why it works that way

- **Value first.** Only handed-in, approved work scores. Calls on
  their own score nothing.
- **Efficiency, kept in a range.** Cheaper work scores more, so
  burning mana never pays. The floor of 0.5 means work done the
  expensive way still counts half: sometimes `pro` is right. The
  ceiling of 1.5 means skimping past a point earns nothing more.
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
- **Weekly reset.** `reset` revokes every game seat and deletes the
  game's database. Nothing carries over to the next week.
- **Prompts are not stored.** `config.game.yaml` sets
  `store_prompts_in_spend_logs: false`, so LiteLLM's spend logs keep
  tags, token counts and spend, never the text. The game's own
  `office.db` keeps only counts and scores. `fake-ollama` logs
  nothing.
- **Read only.** The scorekeeper reads Postgres inside a `READ ONLY`
  transaction. It cannot change a budget or a key.

Keep in mind: whoever runs the proxy can see every key's spend and
tags, game or not.

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
(LiteLLM refills at midnight UTC) and players would tag their own
requests, for example with an `x-litellm-tags: quest:<id>` header
(LiteLLM reads tags from that header too). What is missing is a way
for people to take and hand in quests, and an honest way to judge
real work.

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

In testing, the refill landed within a second of the boundary, and a
key refused with `422` worked again right after. If the job is ever
more than 60 seconds late, the game re-mints the late seats instead
(revoke, then mint: a new key starts with full mana), says so, and
plays a shorter day. That path was tested with the job slowed down.

**Spend logs are written in batches.** The overlay sets
`PROXY_BATCH_WRITE_AT=5` and `SPEND_LOG_QUEUE_SIZE_THRESHOLD=1`, so
they land within seconds. The scorekeeper still waits until the logs
hold every call the bots got an answer to (up to `flush_wait`).

## Files

- `README.md`: this guide.
- `office.py`: the game. `setup`, `run`, `board`, `reset`.
- `rules.toml`: every number the game uses.
- `compose.game.yaml`: the overlay for practice mode.
- `config.game.yaml`: the practice models and their prices.
- `fake_ollama.py`: the fake model server, standard library only.
- `keys/`: game seat keys, made by `setup`. Git ignores `*.key`.
- `office.db`: the game's state (SQLite). Git ignores it.

## Troubleshooting

- **`cannot read the proxy database`**: run the commands from the
  repo folder, with the proxy running (`docker compose ps`).
- **`setup` fails with `already exists`**: a game seat is still
  there from an earlier try. Run `python3 game/office.py reset`.
- **`no key in the proxy for: ...`**: game keys were revoked or
  expired (they last 8 days). `reset`, then `setup`.
- **The day waits before it starts**: it starts on the clock, when
  LiteLLM refills the mana. At most one day length.
- **Every bot stops at once with `error`**: `HTTP 0` means nothing
  answered, so the proxy is down. Any other code: practice mode may
  be off, so `pro` does not exist. Check `.env` and
  `docker compose ps`.
- **Your real seats get filler text**: practice mode is still on.
  See [Back to normal](#back-to-normal).

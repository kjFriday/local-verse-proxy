# Hand runs (Gate A, G-12)

What no CI runner and no sandbox can do: real machines, real phones,
real firewall prompts. Do each run from a release archive (a draft
release of a fork tag is fine), on:

- a Windows 11 PC with Docker Desktop (PowerShell),
- an Apple silicon Mac with Docker Desktop (Terminal),
- a Linux amd64 PC with Docker Engine,
- an arm64 board (Raspberry Pi 4 or 5) if one is at hand.

Write each result in the table at the end: the date, the machine, the
archive's tag, "passed" or what went wrong. A step that was not done
says "not done", never "passed".

## 1. Unpack and first start

1. Download the archive for the machine and `SHA256SUMS`. Check the
   sum: `sha256sum -c --ignore-missing SHA256SUMS` (Linux),
   `shasum -a 256 -c --ignore-missing SHA256SUMS` (macOS),
   `Get-FileHash <archive>` in PowerShell, compared by eye.
2. Unpack it into a folder in your user folder whose path has a
   space in it (`Proxy test/`).
3. macOS: run `./lvp version`. Gatekeeper refuses it the first time;
   note the exact words, then run
   `xattr -d com.apple.quarantine lvp mint-key` and try again.
   Windows: run `.\lvp version`; note SmartScreen's or Defender's
   words and the clicks it took.
4. `lvp --help` and `lvp help desk` print the commands; every line
   fits in 80 columns.
5. Double-click `lvp.exe` in Explorer (Windows): it shows the short
   help and waits for Enter.

## 2. The README path, as it stands

Follow README.md from Step 3 to the 3-check test, word for word, in
that folder. Note every command that did not work as written on that
system, with the shell used (PowerShell, cmd, Terminal, bash). Save
seats.env with Notepad once (CRLF line ends, maybe a byte order
mark): `lvp mint-key laptop` still reads it.

## 3. Front Desk on the machine

1. `lvp desk`: the page opens at the printed address; mint a card,
   use its key for one chat call, cancel it. Ctrl+C stops it at once,
   with no traceback and no `psql` left running (Task Manager,
   Activity Monitor or `ps`).
2. `lvp desk --lan`: the firewall asks once (Windows Defender, macOS).
   Note the words, allow private networks only.
3. From a phone on the same Wi-Fi, open the printed address, log in
   with the printed password, mint a card.
4. Two phones: on the first, type a wrong password five times; the
   second must still log in at once. (Docker Desktop shows every
   device as one address: in step 4 the lock hits both phones. That
   is known, gui/README.md says so; note what you see.)
5. `--port 8041`, `--host <name>.local`: the page opens by that name.

## 4. Front Desk in Docker

1. `docker compose --profile frontdesk up -d`: the first time builds
   the image (note the time and how much it downloaded). Open
   `http://127.0.0.1:8040`, log in, mint and cancel.
2. `docker compose run --rm --no-deps frontdesk mint-key laptop`
   prints a key; `... mint-key --revoke-seat laptop` revokes it.
3. `docker compose stop frontdesk` stops it within a second.
4. `docker compose exec proxy printenv FRONTDESK_PASSWORD` prints an
   empty line.

## 5. The office game

1. Practice mode: put `COMPOSE_FILE=compose.yaml:game/compose.game.yaml`
   in `.env`, `docker compose up -d` (it builds fake-ollama from the
   same Dockerfile). `lvp game setup`, `lvp game run --days 2`,
   `lvp game board`.
2. With `lvp game run` still playing, in a second window
   `lvp game reset`: on Windows it waits about 2 s for office.db,
   then says plainly that the game holds it. After Ctrl+C on the run,
   reset works.
3. Front Desk shows the game seats with the game mark; the reset
   command on the page is the one for this system (`.\lvp game reset`
   in PowerShell).

## 6. Results

| Date | Machine | Tag | Section | Result |
|------|---------|-----|---------|--------|
|      |         |     |         |        |

Not done yet: every section on every machine. The sandbox this was
written in has none of these machines (tests/e2e/gate-a.md, G-12).

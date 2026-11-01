# Ledger: container (chunk C4)

The Docker image (`Dockerfile`, which replaces `gui/Dockerfile`) and
the compose files. Format and rules: README.md here, spec section 19.

The image is `FROM scratch`: one static Go program, `/lvp`, the web's
CA certificates, `THIRD_PARTY_NOTICES.txt`, an empty `/app/seats.env`
stand-in, the `/app/game` mount point and `/tmp` (mode 1777, the
mount point of compose's tmpfs). User 10001, `ENTRYPOINT ["/lvp"]`,
`CMD ["desk", "--docker"]`, `STOPSIGNAL SIGINT`. Measured here: 13.8
MB of files for amd64 (13.1 MB arm64), 5.9 MB compressed; the Python
image was 221 MB, 53 MB compressed.

```text
ID      C-1
What    The tally's `why` after a database error reads in pgconn's
        words, not psycopg's (the entry lives in base.md, with the
        code that makes the text).
Why     pgread.Direct talks to Postgres with pgx's pgconn.
Safe    Same reasons, scrubbed the same way; a DATABASE_URL pgconn
        cannot read gives fixed words that show no part of it.
Tests   internal/pgread (tag pg); base.md C-1
```

```text
ID      C-2
What    SIGTERM (docker stop without compose's STOPSIGNAL, or a
        Docker that ignores it) stops Front Desk cleanly, as Ctrl+C:
        the listener closes, streams end, the database connection is
        closed. Python's server died at once on SIGTERM.
Why     One stop path for both signals (frontdesk.md F-20).
Safe    Cleaner: nothing is cut half way.
Tests   internal/frontdesk TestEndToEnd* (SIGINT and SIGTERM steps);
        here: docker compose stop and restart of the live container
        (tests/e2e/gate-a.md G-9)
```

```text
ID      C-3
What    No shell, no package manager and no Python in the image.
        `docker compose run --rm --no-deps frontdesk mint-key ...`
        still works (it runs `/lvp mint-key ...`). But
        `docker compose exec frontdesk mint-key ...` does not: exec
        skips the entrypoint and the image has no `mint-key` on its
        PATH. It is `docker compose exec frontdesk /lvp mint-key ...`
        now; gui/README.md says so. Messages still name
        `/app/seats.env`, as gui/README.md tells readers to expect.
Why     Decision 16: FROM scratch. One program, so no second file to
        put on a PATH.
Safe    Less in the image to attack or to patch; the same mint-key
        rules and words (base.md B-1).
Tests   parity.yml `image` job (no /bin/sh, no python, user 10001,
        version and mint-key --version); here, on stack lvp-c4: the
        README's `compose run` lines minted, used and revoked a key;
        `compose exec frontdesk mint-key` failed with "executable
        file not found", `exec frontdesk /lvp mint-key --version`
        worked
```

```text
ID      C-4
What    Practice mode builds fake-ollama from the root Dockerfile
        (the same image as Front Desk, `command: ["fake-ollama"]`)
        instead of pulling python:3.12-slim. The first `up` builds it:
        the Go builder image and the modules, about 130 MB once, a
        minute or two. It runs read only, with no powers
        (cap_drop ALL, no-new-privileges), as user 10001.
Why     Go only: no Python image to pull or to keep patched.
Safe    Same answers (game.md P-1 to P-4; the twin harness's
        fake-ollama cases); fewer powers than the Python container
        had.
Tests   tests/parity TestTwinFakeOllama; here: the image's
        fake-ollama served the stack's practice models (a chat call
        through a minted key answered)
```

```text
ID      C-5
What    FRONTDESK_PASSWORD is empty in the proxy's and the database's
        containers: compose.yaml sets it to "" in their environment,
        which beats env_file. Before, every container that read
        seats.env had Front Desk's password in its environment.
Why     Gap 2: a secret goes only where it is used.
Safe    Stricter. LiteLLM and Postgres never used it.
Tests   here, on stack lvp-c4: `docker compose exec proxy printenv
        FRONTDESK_PASSWORD` and the same for db printed an empty
        line; docker inspect of both showed FRONTDESK_PASSWORD=
        (empty); the frontdesk container had it set
```

```text
ID      C-6
What    The Front Desk image's Go is 1.27.3 (pinned by digest), where
        gui/Dockerfile had 1.26.10; release.yml builds with the same
        1.27.3. go.mod still says go 1.26.0.
Why     Decision 14: release builds and the image on the newest 1.27
        patch.
Safe    A newer Go with the same language version; CI tests both
        1.26 and 1.27.
Tests   parity.yml `image`, release.yml
```

```text
ID      C-7
What    The image's size check measures the files (docker export),
        13.8 MB here, not `docker image inspect`'s Size, which with
        Docker's containerd image store also counts the compressed
        layers (19.7 MB here, for the same image).
Why     One number on both of Docker's image stores.
Safe    The limit (20 MB) is the spec's; the measure is the larger
        of the two honest ones on the classic store.
Tests   parity.yml `image`
```

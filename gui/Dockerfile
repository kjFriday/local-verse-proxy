# Front Desk in a container, so Docker is all you install. Build it
# from the repo folder; compose.yaml does that for you:
#   docker compose --profile frontdesk up -d
# See gui/README.md, "Run it in Docker".

# The two base images, pinned to exact builds by their sha256 digests
# (a tag can move; a digest cannot): Go 1.26.10 on Alpine 3.24, and
# Python 3.12.16 on Debian 13. To update one, pull its tag, then put
# the new digest from `docker image inspect` here.
ARG GO_DIGEST=a2012b4a1d1f9f7e9f44e0d39b805a901e856b1dd80b4e6ee4338db547050308
ARG PY_DIGEST=aa943ce411423c7582288f18707ff66eddcbaeb510502d747a72aeb34f30d159

# 1. Build the Go mint-key: one file, with no C parts, so it needs
# nothing else at run time. It uses Go's standard library only, so
# this step downloads nothing.
FROM golang:1.26-alpine@sha256:${GO_DIGEST} AS mint
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/ cmd/
COPY internal/ internal/
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/mint-key ./cmd/mint-key

# 2. Fetch psycopg, ready-built, checked against the hashes in
# requirements-docker.txt.
FROM python:3.12-slim@sha256:${PY_DIGEST} AS deps
COPY gui/requirements-docker.txt /tmp/
RUN pip install --no-cache-dir --disable-pip-version-check \
      --root-user-action ignore --require-hashes --only-binary :all: \
      --prefix /deps -r /tmp/requirements-docker.txt

# 3. What runs: Front Desk, mint-key and psycopg, as a user that may
# change nothing. Build tools and pip's downloads stay behind.
FROM python:3.12-slim@sha256:${PY_DIGEST}
COPY --from=deps /deps /usr/local
COPY --from=mint /out/mint-key /usr/local/bin/mint-key
COPY gui/frontdesk.py /app/gui/
COPY gui/static/ /app/gui/static/
# Python's own library comes without its compiled (.pyc) files, and
# this container may not save any, so each start would compile what
# Front Desk uses again: about half a second. One import here saves
# them for good (6 MB). frontdesk.py's own is never used: drop it.
RUN cd /app/gui && python3 -c "import frontdesk, psycopg" \
 && rm -r __pycache__
# mint-key reads the file SEATS_ENV names. Here that file is empty on
# purpose: Compose passes your seats.env in as environment variables
# (env_file in compose.yaml), and a setting the file leaves out comes
# from those. Never copy the real seats.env into an image.
RUN useradd --uid 10001 --user-group --no-create-home \
      --home-dir /nonexistent --shell /usr/sbin/nologin frontdesk \
 && echo "# Empty on purpose: see gui/Dockerfile." > /app/seats.env
ENV SEATS_ENV=/app/seats.env \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1
WORKDIR /app
USER 10001:10001
EXPOSE 8040
# Ctrl+C, in effect: Front Desk stops at once, and cleanly.
STOPSIGNAL SIGINT
CMD ["python3", "gui/frontdesk.py", "--docker"]

# Front Desk and the practice model server, Go only. Compose builds
# it for you:
#   docker compose --profile frontdesk up -d
# See gui/README.md, "Run it in Docker".
#
# What runs is one program, /lvp, with nothing else: no shell, no
# package manager, no Python. It runs as user 10001 and may change
# nothing in its own files (compose.yaml makes the root read only).

# The Go image the program is built with, pinned to one exact build by
# its sha256 digest (a tag can move; a digest cannot): Go 1.27.3 on
# Alpine 3.24, the same Go as .github/workflows/release.yml. To update
# it, look the new digest up (docker buildx imagetools inspect
# golang:<version>-alpine<version>) and change both files together.
ARG GO_IMAGE=golang:1.27.3-alpine3.24
ARG GO_DIGEST=d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb

# 1. Build lvp for the system the image is for (amd64 or arm64), on
# the system that builds it, and write the notices of what it links.
FROM --platform=$BUILDPLATFORM ${GO_IMAGE}@sha256:${GO_DIGEST} AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
# The modules first, so a code change does not download them again.
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY gui/ gui/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
      go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/lvp ./cmd/lvp \
 && go run ./internal/notices -bin /out/lvp \
      > /out/THIRD_PARTY_NOTICES.txt \
 && mkdir -p /out/root/app/game /out/root/tmp \
 && chmod 1777 /out/root/tmp \
 && echo "# Empty on purpose: see the Dockerfile." \
      > /out/root/app/seats.env

# 2. What runs: lvp, the certificates it trusts, its notices, and two
# folders. Nothing from the build stays but these. /app/game is where
# compose mounts the game folder, read only, and /tmp the mount point
# for compose's tmpfs (scratch has no /tmp).
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/lvp /lvp
COPY --from=build /out/THIRD_PARTY_NOTICES.txt /
COPY --from=build /out/root/ /
# mint-key in the image reads this empty file, then the environment
# that compose passes in from seats.env (env_file in compose.yaml). A
# setting the file leaves out comes from there. Never copy the real
# seats.env into an image.
ENV SEATS_ENV=/app/seats.env
WORKDIR /app
USER 10001:10001
EXPOSE 8040
# Ctrl+C, in effect: Front Desk stops at once, and cleanly.
STOPSIGNAL SIGINT
ENTRYPOINT ["/lvp"]
CMD ["desk", "--docker"]

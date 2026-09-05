# publix, as a container.
#
# Running publix this way means a server needs nothing installed but Docker
# itself — no Go, no Node, no toolchain. The image carries the git and
# docker clients publix shells out to.
#
# One rule governs how this is run, and breaking it breaks deploys in ways
# that are hard to diagnose: every host path publix touches must be mounted
# at the *same* path inside the container. publix hands paths to the Docker
# daemon, which resolves them on the host, so a path that means one thing
# inside and another outside produces containers bind-mounting the wrong
# directory. See deploy/docker-compose.yml.

# --- dashboard ---------------------------------------------------------------
FROM node:22-alpine AS ui
WORKDIR /src

# Dependencies first: the dashboard's lockfile changes far less often than
# its source, so this layer survives most rebuilds.
COPY web/package.json web/package-lock.json ./web/
RUN cd web && npm ci --no-audit --no-fund

COPY web ./web
# Vite is configured to emit into the Go package that embeds it.
RUN mkdir -p internal/api/dist && cd web && npm run build

# --- binary ------------------------------------------------------------------
FROM golang:1.24-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY --from=ui /src/internal/api/dist ./internal/api/dist

ARG VERSION=docker
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/publix ./cmd/publix

# --- runtime -----------------------------------------------------------------
FROM alpine:3.21

# git clones repositories; the docker CLI and its compose plugin are what
# publix drives compose projects with. Everything else it does goes over the
# Docker API directly.
RUN apk add --no-cache ca-certificates git openssh-client docker-cli docker-cli-compose tzdata

COPY --from=build /out/publix /usr/local/bin/publix

# publix needs to reach the Docker socket, which on most hosts is owned by
# root or a docker group. It runs as root here for that reason: it is a
# control plane with the host's container runtime in its hands either way,
# and pretending otherwise would be security theatre. Keep it on loopback
# and behind Traefik.
ENV PUBLIX_HOME=/var/lib/publix \
    PUBLIX_ADDR=0.0.0.0:4321

EXPOSE 4321

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:4321/api/health >/dev/null 2>&1 || exit 1

ENTRYPOINT ["publix"]
CMD ["serve"]

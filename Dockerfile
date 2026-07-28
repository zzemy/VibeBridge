# syntax=docker/dockerfile:1.7
# Multi-stage build for the VibeBridge relay (viberelay). The image
# produces a statically linked, non-root container that exposes the
# WebSocket switchboard on 8788 and the ticket issuance control plane
# on 8789. Build with `docker build -t vibebridge/viberelay:local .`
# or via the docker-compose file at the repo root.
#
# The build context is the VibeBridge repo root; viberelay shares a
# module with the agent so the .dockerignore trims everything the
# relay does not need (UI, pty, VCS metadata) before this stage runs.

FROM golang:1.26.5-alpine AS build

# git is needed for `go mod download` when the module graph references
# a private mirror. apk add is cached on the layer so it does not
# slow down incremental builds.
RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Download module dependencies first so a source-only change does not
# bust the dependency layer cache.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source tree. .dockerignore keeps test
# artifacts, the web UI, and the vibebridge agent out of the build
# context.
COPY cmd/    cmd/
COPY gen/    gen/
COPY internal/ internal/

# CGO is disabled: viberelay is pure Go and a static binary makes the
# runtime image one layer thinner. -trimpath strips local paths from
# the binary so builds are reproducible across hosts.
RUN CGO_ENABLED=0 \
    go build \
        -trimpath \
        -ldflags="-s -w" \
        -o /out/viberelay \
        ./cmd/viberelay

# Runtime image. alpine brings busybox utilities (wget, nc) that the
# compose healthcheck depends on, at a cost of about 5 MB on top of
# the static binary.
FROM alpine:3.20

# ca-certificates lets the relay reach HTTPS-only ticket issuer
# mirrors (e.g. a control plane behind a corporate CA). The non-root
# user matches the distroless convention compose users are used to.
RUN apk add --no-cache ca-certificates && \
    addgroup -S viberelay && \
    adduser  -S -G viberelay viberelay

COPY --from=build /out/viberelay /usr/local/bin/viberelay

# 8788 is the WebSocket switchboard default. 8789 is the ticket
# issuance control plane default. The compose file binds the latter
# to host loopback only.
EXPOSE 8788 8789

USER viberelay

# The entrypoint is the relay itself. The compose file supplies the
# runtime flags; running the image directly works as long as the
# caller knows the path to the issuer key file.
ENTRYPOINT ["/usr/local/bin/viberelay"]

# Stage 1: build
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Cache module downloads separately from the source build.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/github-mirror \
      ./cmd/github-mirror

# Stage 2: runtime
FROM alpine:3.20

# git, git-lfs, ca-certificates (for HTTPS clones), tini (PID 1 signal handling)
RUN apk add --no-cache git git-lfs ca-certificates tini \
    && git lfs install --system

# Run as a non-root user.
RUN addgroup -S app && adduser -S -G app -u 1000 app

# Bare-mirror data (bind-mounted in compose).
WORKDIR /data
VOLUME /data

# Sync-protocol SQLite DB lives in a managed Docker volume so the health
# probe and the daemon can both reach it without leaking state onto the host.
RUN mkdir -p /var/lib/github-mirror && chown app:app /var/lib/github-mirror
VOLUME /var/lib/github-mirror

COPY --from=builder /out/github-mirror /usr/local/bin/github-mirror

USER app

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/github-mirror"]

# syntax=docker/dockerfile:1.7

# Runtime base for the final stage. MUST be declared before the first FROM:
# an ARG declared after a FROM is scoped to that stage and is invisible to
# later FROM lines, so a global ARG is the only way to parameterize the base.
# OSS installs use the public GHCR mirror of our internal cloud-collector base
# image (Alpine + pre-baked tooling). EE deploys override RUNTIME_BASE to their
# internal registry via --build-arg.
ARG RUNTIME_BASE=ghcr.io/nudgebee/nudgebee-cloud-collector-base:alpine3.22

FROM golang:1.26.5-alpine AS build-stage

# Use the builder image's bundled Go toolchain, never an auto-downloaded one.
# GOTOOLCHAIN=local guarantees the compile uses whatever go the base image ships
# (currently 1.26.5) and future-proofs a base bump: raising the builder to
# golang:1.26.6-alpine automatically compiles with 1.26.6 — with a hardcoded
# version this line would silently download and reuse the old toolchain. It also
# gives the `go build` layer a distinct cache key so edge.yaml's registry cache
# (mode=max) can't reuse a layer compiled by an older toolchain — the cause of
# stdlib CVEs persisting in published images despite the pinned builder.
ENV GOTOOLCHAIN=local

WORKDIR /app

# Install git (needed for go mod)
RUN apk add --no-cache git

# Copy only go mod files first (better caching)
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# Copy application source
COPY . .

# Build binary
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 \
    GOOS=linux \
    go build -o /app/cloud-collector-server ./cmd

FROM ${RUNTIME_BASE}

WORKDIR /app

# Copy only the compiled binary
COPY --from=build-stage /app/cloud-collector-server /app/cloud-collector-server

RUN chmod +x /app/cloud-collector-server

EXPOSE 8000

CMD ["/app/cloud-collector-server"]

# Patched mirror of the official PostgreSQL image on Debian 12 (bookworm, glibc).
# The planned replacement for the frozen bitnami postgresql subchart image; the
# alpine mirror in ../postgres stays for the db-check init container. Two fixes:
#
#  1. apt-get upgrade — the pinned tag snapshot lags Debian security releases.
#     Bump OS_PKG_EPOCH (ISO week) to force a weekly re-pull.
#
#  2. Rebuild gosu on a current Go. The image ships a prebuilt /usr/local/bin/gosu
#     (the privilege-drop helper the entrypoint uses) built on an older Go, whose
#     stdlib CVEs apt can't touch. We rebuild the SAME upstream source
#     (github.com/tianon/gosu@1.19 — the image's GOSU_VERSION) on the current Go
#     and swap it in; gosu just execs a command after dropping to a target uid, so
#     the rebuild is behaviour-identical.
#
# Same postgres version + base -- no behaviour change. The official image runs as
# root at build, so no USER switch is needed for apt.

# Stage 1: rebuild gosu on a patched Go toolchain.
FROM ghcr.io/nudgebee/golang:1.26.10-alpine AS gosu
# Use the builder's bundled Go (1.26.10), never an auto-downloaded one. CGO off ->
# a static binary, so the musl builder output runs on the glibc runtime.
ENV GOTOOLCHAIN=local CGO_ENABLED=0
RUN go install github.com/tianon/gosu@1.19

ARG OS_PKG_EPOCH=2026-W44
FROM docker.io/library/postgres:16.15-bookworm

# Re-declare in the build-stage scope (an ARG before FROM is only visible to FROM
# lines).
ARG OS_PKG_EPOCH

RUN export DEBIAN_FRONTEND=noninteractive \
    && echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apt-get update \
    && apt-get upgrade -y \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
# Swap the stale gosu for the current-Go rebuild (same root-owned 0755 layout
# the entrypoint expects).
COPY --from=gosu /go/bin/gosu /usr/local/bin/gosu

# Patched mirror of the official PostgreSQL image (used as the db-check init
# container across the service pods, via global.initImages.postgres). Two fixes:
#
#  1. apk upgrade — the pinned tag snapshot lags Alpine security releases, so
#     without this the image ships stale OS-package CVEs (e.g. libcrypto3/libssl3).
#     Bump OS_PKG_EPOCH (ISO week) to force a weekly re-pull.
#
#  2. Rebuild gosu on a current Go. The official image ships a prebuilt
#     /usr/local/bin/gosu (the privilege-drop helper the postgres entrypoint uses)
#     compiled on an old Go, which carries 1 CRITICAL + 14 HIGH Go-stdlib CVEs.
#     apk can't touch a Go binary, and gosu is a rarely-rebuilt release binary, so
#     a postgres tag bump doesn't clear it. We rebuild the SAME upstream source
#     (github.com/tianon/gosu@1.19 — the version 16.15-alpine ships) with a patched
#     Go and swap it in; gosu just execs a command after
#     dropping to a target uid, so the rebuild is behaviour-identical. Verified:
#     the resulting image scans 0/0/0 (was 1/14/21 fixable, 0 unfixable — alpine
#     has no OS NOFIX floor, so this image goes fully clean).
#
# Same postgres version + base -- no behaviour change. The official image runs as
# root at build, so no USER switch is needed for apk.

# Stage 1: rebuild gosu on a patched Go toolchain.
FROM ghcr.io/nudgebee/golang:1.26.10-alpine AS gosu
# Use the builder's bundled Go (1.26.10), never an auto-downloaded one, so gosu
# picks up the stdlib fixes instead of a stale toolchain. CGO off -> the same
# static binary the upstream release ships.
ENV GOTOOLCHAIN=local CGO_ENABLED=0
RUN go install github.com/tianon/gosu@1.19

ARG OS_PKG_EPOCH=2026-W44
FROM docker.io/library/postgres:16.15-alpine

# Re-declare in the build-stage scope (an ARG before FROM is only visible to FROM
# lines).
ARG OS_PKG_EPOCH

RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apk upgrade --no-cache
# Swap the stale gosu for the current-Go rebuild (same source, same
# root-owned 0755 layout the postgres entrypoint expects).
COPY --from=gosu /go/bin/gosu /usr/local/bin/gosu

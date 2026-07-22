# Patched mirror of bitnami Redis. Two fixes over the frozen bitnamilegacy image:
#
#  1. apt-get upgrade — Bitnami sunset the free docker.io/bitnami/* images
#     mid-2025; the archived docker.io/bitnamilegacy/* tags are frozen (no OS
#     patches since ~Aug 2025), so the pinned tag accrues Debian CVEs. Rebuilding
#     the SAME image (same Redis version + base -- no data/behaviour change) lets
#     the still-security-supported Debian base pick up current patches. Bump
#     OS_PKG_EPOCH (ISO week) to force a weekly re-pull.
#
#  2. Rebuild wait-for-port on a current Go. The image's startup helper
#     /opt/bitnami/common/bin/wait-for-port is compiled with Go 1.19.12 and
#     carries 2 CRITICAL + 19 HIGH + 43 MEDIUM Go-stdlib CVEs — which apt cannot
#     touch (it is a Go binary, not an OS package), and which no bitnamilegacy tag
#     fixes (they are all frozen on old Go). We rebuild the SAME upstream source
#     (github.com/bitnami/wait-for-port v1.0.10 — the version bitnami itself ships)
#     with Go 1.26.5 and swap it in. It is a trivial TCP-port waiter run once at
#     container start, so the rebuild is behaviour-identical. Verified: the
#     resulting image scans 0 fixable C/H/M (was 2/19/43); only the debian-11
#     NOFIX OS floor remains (chased via the weekly apt refresh / a future base
#     migration, not this change).

# Stage 1: rebuild wait-for-port on a patched Go toolchain.
FROM golang:1.26.5-alpine AS wait-for-port
# Use the builder's bundled Go (1.26.5), never an auto-downloaded one, so the
# helper picks up the stdlib fixes (e.g. CVE-2026-39822 / -42505 are fixed in
# 1.26.5) instead of a stale toolchain. CGO_ENABLED=0 forces a fully static
# binary — the builder is musl (alpine) but the runtime is glibc (debian-11), so
# a static binary guarantees it runs there regardless of libc.
ENV GOTOOLCHAIN=local CGO_ENABLED=0
RUN go install github.com/bitnami/wait-for-port@v1.0.10

ARG OS_PKG_EPOCH=2026-W29
FROM docker.io/bitnamilegacy/redis:7.0.12-debian-11-r0

# Re-declare in the build-stage scope (an ARG before FROM is only visible to FROM
# lines). Without this the RUN below sees an unset var and bitnami's `set -u`
# shell aborts.
ARG OS_PKG_EPOCH

# Bitnami images run as non-root (uid 1001); switch to root to patch, then back.
USER root
RUN export DEBIAN_FRONTEND=noninteractive \
    && echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apt-get update \
    && apt-get upgrade -y \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
# Swap the stale Go 1.19.12 helper for the current-Go rebuild (same source,
# same uid-1001 ownership expectations — it is world-executable).
COPY --from=wait-for-port /go/bin/wait-for-port /opt/bitnami/common/bin/wait-for-port
USER 1001

# Patched mirror of bitnami Redis on Debian 12 (bookworm). Successor to
# ../redis (7.0.12-debian-11-r0): Debian 11 LTS ended Aug 2026, so that base can
# no longer be patched. 7.0.15 is the newest debian-12 tag on the same 7.0 line —
# the smallest jump from 7.0.12 (patch-level only, same RDB/AOF format) and the
# line the bitnami redis chart 17.11.x ships. Same two fixes as ../redis:
#
#  1. apt-get upgrade — the docker.io/bitnamilegacy/* tags are frozen (no OS
#     patches since ~Aug 2025), so the pinned tag accrues Debian CVEs. Rebuilding
#     the SAME image lets the still-security-supported bookworm base pick up
#     current patches. Bump OS_PKG_EPOCH (ISO week) to force a weekly re-pull.
#
#  2. Rebuild wait-for-port on a current Go. The frozen image's startup helper
#     /opt/bitnami/common/bin/wait-for-port is built on a 2025 Go and carries
#     Go-stdlib CVEs apt cannot touch. We rebuild the same upstream source
#     (github.com/bitnami/wait-for-port v1.0.10) on the current Go and swap it in;
#     it is a trivial TCP-port waiter run once at container start, so the rebuild
#     is behaviour-identical.

# Stage 1: rebuild wait-for-port on a patched Go toolchain.
FROM ghcr.io/nudgebee/golang:1.26.10-alpine AS wait-for-port
# Use the builder's bundled Go (1.26.10), never an auto-downloaded one.
# CGO_ENABLED=0 forces a fully static binary — the builder is musl (alpine) but
# the runtime is glibc (debian-12).
ENV GOTOOLCHAIN=local CGO_ENABLED=0
RUN go install github.com/bitnami/wait-for-port@v1.0.10

ARG OS_PKG_EPOCH=2026-W44
FROM docker.io/bitnamilegacy/redis:7.0.15-debian-12-r20

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
# Swap the stale helper for the current-Go rebuild (it is world-executable).
COPY --from=wait-for-port /go/bin/wait-for-port /opt/bitnami/common/bin/wait-for-port
USER 1001

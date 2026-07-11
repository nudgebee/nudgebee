# Patched mirror of bitnami Redis.
#
# Bitnami sunset the free docker.io/bitnami/* images mid-2025; the archived
# docker.io/bitnamilegacy/* tags are frozen (no OS patches since ~Aug 2025), so
# our pinned tag accrues Debian CVEs. This adds an apt-get upgrade layer on top
# of the exact same bitnami image (same Redis version + base -- no data/behaviour
# change) so the still-security-supported Debian base picks up current patches.
# Bump OS_PKG_EPOCH (ISO week) to force a weekly re-pull.
ARG OS_PKG_EPOCH=2026-W28
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
USER 1001

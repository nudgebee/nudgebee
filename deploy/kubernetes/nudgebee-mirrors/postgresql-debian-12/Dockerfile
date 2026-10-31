# Patched mirror of bitnami PostgreSQL on Debian 12 (bookworm). Successor to
# ../postgresql (16.1.0-debian-11-r16): Debian 11 LTS ended Aug 2026, so that base
# can no longer be patched. 16.6.0-debian-12-r2 is the newest 16.x bitnamilegacy
# tag (the archive froze before 16.7); a minor bump within 16, so no pg_upgrade.
#
# The docker.io/bitnamilegacy/* tags are frozen (no OS patches since ~Aug 2025),
# so this adds an apt-get upgrade layer on top of the exact same bitnami image so
# the still-security-supported bookworm base picks up current patches. Bump
# OS_PKG_EPOCH (ISO week) to force a weekly re-pull.
ARG OS_PKG_EPOCH=2026-W44
FROM docker.io/bitnamilegacy/postgresql:16.6.0-debian-12-r2

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

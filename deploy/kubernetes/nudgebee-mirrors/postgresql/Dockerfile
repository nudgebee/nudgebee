# Patched mirror of bitnami PostgreSQL.
#
# Bitnami sunset the free docker.io/bitnami/* images mid-2025; the archived
# docker.io/bitnamilegacy/* tags are frozen (no OS patches since ~Aug 2025), so
# our pinned tag accrues Debian CVEs. This adds an apt-get upgrade layer on top
# of the exact same bitnami image (same PostgreSQL version + base -- no data/behaviour
# change) so the still-security-supported Debian base picks up current patches.
# Bump OS_PKG_EPOCH (ISO week) to force a weekly re-pull.
ARG OS_PKG_EPOCH=2026-W36
FROM docker.io/bitnamilegacy/postgresql:16.1.0-debian-11-r16

# Re-declare in the build-stage scope (an ARG before FROM is only visible to FROM
# lines). Without this the RUN below sees an unset var and bitnami's `set -u`
# shell aborts.
ARG OS_PKG_EPOCH

# Bitnami images run as non-root (uid 1001); switch to root to patch, then back.
USER root
# Debian 11 (bullseye) LTS ended Aug 2026 and its final point/security updates
# moved to archive.debian.org. security.debian.org still serves the
# bullseye-security index but its package pool is gone (every .deb 404s), so
# point every bullseye source at the archive, which holds the same final release
# plus the packages. The sources are frozen now, so this is the last OS refresh
# these debian-11 images can get.
RUN export DEBIAN_FRONTEND=noninteractive \
    && echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && find /etc/apt/ -name '*.list' -exec sed -i -E 's#https?://(deb|security)\.debian\.org#http://archive.debian.org#g' {} + \
    && apt-get update \
    && apt-get upgrade -y \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
USER 1001

# Patched mirror of bitnami ClickHouse (used by the nudgebee-agent chart's
# clickhouse subchart).
#
# Bitnami sunset the free docker.io/bitnami/* images mid-2025; the archived
# docker.io/bitnamilegacy/* tags are frozen (no OS patches since ~Aug 2025).
# The previous ghcr nb-2 build (a one-off, manually pushed from nudgebee-infra
# deploy/containers/clickhouse) only INSTALLED extra packages and never ran
# `apt-get upgrade`, so it shipped the frozen base's package set unpatched —
# Trivy: 5 CRITICAL / 40 HIGH / 63 MEDIUM fixable (libgnutls30, libxml2,
# libssl3/openssl, ...). This mirror adds the upgrade and joins the weekly
# infra-mirrors rebuild so it keeps picking up Debian security releases.
# Bump OS_PKG_EPOCH (ISO week) to force a re-pull outside the schedule.
#
# The extra packages (libxml2, libxslt1.1, perl) are inherited from the
# original build: ClickHouse UDFs / dictionary sources shell out to them.
ARG OS_PKG_EPOCH=2026-W29
FROM docker.io/bitnamilegacy/clickhouse:24.12.4-debian-12-r0

# Re-declare in the build-stage scope (an ARG before FROM is only visible to
# FROM lines). Without this the RUN below sees an unset var and bitnami's
# `set -u` shell aborts.
ARG OS_PKG_EPOCH

# Bitnami images run as non-root (uid 1001); switch to root to patch, then back.
USER root
RUN export DEBIAN_FRONTEND=noninteractive \
    && echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        libxml2 \
        libxslt1.1 \
        libc6 \
        perl \
        perl-base \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
USER 1001

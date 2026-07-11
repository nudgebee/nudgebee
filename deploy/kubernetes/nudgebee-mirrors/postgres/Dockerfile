# Patched mirror of the official PostgreSQL image (used as the db-check init
# container across the service pods, via global.initImages.postgres).
#
# Re-mirror of docker.io/library/postgres + an apk upgrade layer: the pinned tag
# snapshot lags Alpine security releases, so without this the image ships stale
# OS-package CVEs (e.g. libcrypto3/libssl3). Same postgres version + base -- no
# behaviour change. Bump OS_PKG_EPOCH (ISO week) to force a weekly re-pull. The
# official image runs as root at build, so no USER switch is needed for apk.
ARG OS_PKG_EPOCH=2026-W28
FROM docker.io/library/postgres:16.10-alpine

# Re-declare in the build-stage scope (an ARG before FROM is only visible to FROM
# lines).
ARG OS_PKG_EPOCH

RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apk upgrade --no-cache

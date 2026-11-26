# Patched mirror of the official Redis image (the umbrella chart's own Redis,
# templates/redis/, redis.image).
#
# Re-mirror of docker.io/library/redis + an apk upgrade layer, same as the nginx
# and postgres mirrors: the pinned tag snapshot lags Alpine security releases.
# 7.2.17-alpine ships zlib 1.3.2-r1 (CRITICAL CVE-2026-4738) and libssl3/libcrypto3
# 3.5.9-r0 (HIGH), so the plain copy clone-third-party.sh used to make is no longer
# clean. OS_PKG_EPOCH (ISO week) is fed by infra-mirrors-image.yaml to force a
# re-pull. The official image runs as root at build, so no USER switch is needed.
#
# Same Redis version and base -- no behaviour change.
ARG OS_PKG_EPOCH=2026-W48
FROM docker.io/library/redis:7.2.17-alpine
# Re-declare in the build-stage scope (an ARG before FROM is only visible to FROM
# lines).
ARG OS_PKG_EPOCH
RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apk upgrade --no-cache

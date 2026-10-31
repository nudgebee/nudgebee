# Patched mirror of the official nginx image (runs as a long-lived, network-
# facing container in the app/frontend pod).
#
# Re-mirror of docker.io/library/nginx + an apk upgrade layer: the pinned tag
# snapshot lags Alpine security releases, so without this the image ships stale
# OS-package CVEs (e.g. libcrypto3/libssl3). Bump OS_PKG_EPOCH (ISO week) to
# force a weekly re-pull. The official image runs as root at build, so no USER
# switch is needed for apk.
#
# Alpine 3.20 stopped at nginx 1.26.3, which carries unfixed nginx CVEs there, so
# this follows the current stable line instead of the old Alpine branch.
ARG OS_PKG_EPOCH=2026-W44
FROM docker.io/library/nginx:stable-alpine3.24-slim

# Re-declare in the build-stage scope (an ARG before FROM is only visible to FROM
# lines).
ARG OS_PKG_EPOCH

RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apk upgrade --no-cache

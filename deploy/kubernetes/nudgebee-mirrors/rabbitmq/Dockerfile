# Patched mirror of the official RabbitMQ image (the umbrella chart's own
# RabbitMQ, templates/rabbitmq/, rabbitmq.image).
#
# Re-mirror of docker.io/library/rabbitmq + an apk upgrade layer, same as the nginx
# and postgres mirrors: the pinned tag snapshot lags Alpine security releases.
# 4.2.9-management-alpine ships zlib 1.3.2-r1 (CRITICAL CVE-2026-4738), so the plain
# copy clone-third-party.sh makes is no longer clean. OS_PKG_EPOCH (ISO week) is fed
# by infra-mirrors-image.yaml to force a re-pull. The official image runs as root at
# build, so no USER switch is needed for apk.
#
# Not fixed here: the OpenSSL the official image compiles into /opt/openssl for
# Erlang (3.5.9, HIGH only). apk cannot touch it, and upstream 4.4.1 still ships it.
#
# Same RabbitMQ version and base -- no behaviour change.
ARG OS_PKG_EPOCH=2026-W48
FROM docker.io/library/rabbitmq:4.2.9-management-alpine
# Re-declare in the build-stage scope (an ARG before FROM is only visible to FROM
# lines).
ARG OS_PKG_EPOCH
RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apk upgrade --no-cache

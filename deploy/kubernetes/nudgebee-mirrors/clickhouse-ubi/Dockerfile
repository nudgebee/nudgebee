# ClickHouse 26.10 on Red Hat UBI 9, for the nudgebee-agent chart (Red Hat chart
# certification; the Ubuntu-based ../clickhouse-official-26 image cannot pass it).
# Same ClickHouse build: the binary, default config and entrypoint are copied from
# the official image, pinned by digest, so volumes and chart settings carry over.
# (The official RPMs are unsigned, so they are not used.)
#
# Differences from the official image: the default user is the numeric 101 (it
# started as root and dropped to 101), so it no longer chowns a root-owned volume,
# and the locale is C.UTF-8. The chart already runs it as a non-root uid.
#
# Not ubi-micro: the chart's schema jobs run bash and clickhouse-client, and the
# entrypoint needs wget for init scripts.
FROM docker.io/clickhouse/clickhouse-server:26.10.4.207@sha256:4f491abc44d9f4a1747c033c35ddd5bc385521305d85ca8251ba0824ff597035 AS upstream

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.9-1795198179

# The weekly infra-mirrors rebuild feeds OS_PKG_EPOCH (ISO week) to pick up UBI
# security releases.
ARG OS_PKG_EPOCH=2026-W48
ARG IMAGE_RELEASE=1

LABEL name="nudgebee-clickhouse" \
      maintainer="nudgebee" \
      vendor="nudgebee" \
      version="26.10.4.207" \
      release="${IMAGE_RELEASE}" \
      summary="ClickHouse server" \
      description="ClickHouse server, the Nudgebee agent's local store, on Red Hat UBI 9."

RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && groupadd -r -g 101 clickhouse \
    && useradd -r -u 101 -g 101 -d /var/lib/clickhouse -s /sbin/nologin clickhouse \
    && microdnf update -y --nodocs --setopt=install_weak_deps=0 \
    && microdnf install -y --nodocs --setopt=install_weak_deps=0 wget gzip \
    && microdnf clean all \
    && rm -rf /var/cache/dnf /var/cache/yum

# cp -a keeps clickhouse-server, clickhouse-client and the rest as symlinks to the one
# binary; COPY would follow them and duplicate it.
RUN --mount=type=bind,from=upstream,source=/,target=/upstream \
    cp -a /upstream/usr/bin/clickhouse* /usr/bin/ \
    && cp -a /upstream/etc/clickhouse-server /upstream/etc/clickhouse-client /etc/
COPY --from=upstream --chmod=0755 /entrypoint.sh /entrypoint.sh

# Red Hat certification (HasLicense). The repo LICENSE comes in through the `repo`
# named context:  docker buildx build --build-context repo=<repo root> .
COPY LICENSE.clickhouse /licenses/LICENSE.clickhouse
COPY --from=repo LICENSE /licenses/LICENSE

# Group 0 and group-writable, so an arbitrary OpenShift uid (always gid 0) can use
# them. /etc/clickhouse-server too: the entrypoint writes users.d/default-user.xml.
RUN mkdir -p /var/lib/clickhouse /var/log/clickhouse-server /docker-entrypoint-initdb.d \
        /etc/clickhouse-server/users.d /etc/clickhouse-client \
    && chown -R 101:0 /var/lib/clickhouse /var/log/clickhouse-server /etc/clickhouse-server /etc/clickhouse-client \
    && chmod -R g=u /var/lib/clickhouse /var/log/clickhouse-server /etc/clickhouse-server /etc/clickhouse-client

ENV LANG=C.UTF-8 TZ=UTC CLICKHOUSE_CONFIG=/etc/clickhouse-server/config.xml
VOLUME /var/lib/clickhouse
EXPOSE 8123 9000 9009
# Numeric uid so runAsNonRoot can be verified without an /etc/passwd entry.
USER 101
ENTRYPOINT ["/entrypoint.sh"]

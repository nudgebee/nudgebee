# Patched mirror of the official ClickHouse server image, used by the
# nudgebee-agent chart once it stops depending on the Bitnami clickhouse
# subchart. Same ClickHouse version (24.12.4) as the Bitnami-based mirror in
# ../clickhouse, so the data on existing volumes is read as-is.
#
# The Bitnami-based mirror (../clickhouse, tag 24.12.4-debian-12-r0-nb-3)
# stays: charts released before the switch still pull it.
#
# Not -alpine / distroless: the chart's pre-upgrade schema job runs bash and
# clickhouse-client from this image.
#
# The apt-get upgrade and the extra packages are copied from ../clickhouse.
# The weekly infra-mirrors rebuild feeds OS_PKG_EPOCH (ISO week) to pick up
# Ubuntu security releases; bump it to force a re-pull outside the schedule.
ARG OS_PKG_EPOCH=2026-W45
FROM docker.io/clickhouse/clickhouse-server:24.12.4

# Re-declare in the build-stage scope (an ARG before FROM is only visible to
# FROM lines).
ARG OS_PKG_EPOCH

# The image installs ClickHouse from packages.clickhouse.com (stable channel),
# so a plain `apt-get upgrade` would move ClickHouse to the newest stable
# release. Hold the ClickHouse packages so only the OS is patched, and fail the
# build if the server version changed anyway.
#
# The official image already runs as root and its entrypoint drops to the
# clickhouse user (or the pod's runAsUser), so there is no USER to switch back.
RUN export DEBIAN_FRONTEND=noninteractive \
    && echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apt-mark hold clickhouse-server clickhouse-client clickhouse-common-static \
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

RUN clickhouse-server --version \
    && clickhouse-server --version | grep -q ' 24\.12\.4\.' \
    || { echo "ClickHouse version changed during the OS upgrade" >&2; exit 1; }

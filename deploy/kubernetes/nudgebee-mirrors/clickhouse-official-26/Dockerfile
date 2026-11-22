# Patched mirror of the official ClickHouse 26.10 server image, for the
# nudgebee-agent chart from its 26.10 upgrade on. 26.10 reads volumes 24.12
# wrote, but 24.12 cannot read volumes 26.10 has written.
#
# The 24.12 mirror (../clickhouse-official, tag 24.12.4-nb-1) stays: charts
# released before the upgrade still pull it.
#
# Not -alpine / distroless: the chart's pre-upgrade schema job runs bash and
# clickhouse-client from this image.
#
# The apt-get upgrade and the extra packages are copied from ../clickhouse.
# The weekly infra-mirrors rebuild feeds OS_PKG_EPOCH (ISO week) to pick up
# Ubuntu security releases; bump it to force a re-pull outside the schedule.
ARG OS_PKG_EPOCH=2026-W47
FROM docker.io/clickhouse/clickhouse-server:26.10.4.207

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

RUN version="$(clickhouse-server --version)" \
    && echo "${version}" \
    && echo "${version}" | grep -q ' 26\.10\.4\.' \
    || { echo "ClickHouse version changed during the OS upgrade" >&2; exit 1; }

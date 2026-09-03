# Migration Job image — Atlas Community edition (Apache 2.0, ariga/atlas)
# replaces golang-migrate as the sole engine. Atlas tracks per-file revisions
# in nudgebee.atlas_schema_revisions and applies out-of-order arrivals
# natively (--exec-order non-linear in atlas.hcl), eliminating the
# silent-skip / phantom-version / CONCURRENTLY-in-tx incident classes that
# golang-migrate's single-row tracker kept producing (Fixes #33007). The
# golang-migrate binary and its multi-stage builder are no longer needed and
# have been removed.

# Alpine base: its OS packages are continuously patched, unlike ubuntu:24.04
# whose noble snapshot accrues fixable-but-unpatched CVEs. This image only
# needs psql + the atlas binary + bash + curl, all clean on Alpine.
FROM alpine:3.22

ARG TARGETARCH

ENV TZ=Etc/UTC

# postgresql-client provides psql, used by run-migrations.sh to
#   * pre-create the nudgebee schema (Atlas does not auto-create the schema
#     its revisions table lives in)
#   * detect cutover state (legacy nudgebee.schema_migrations vs new
#     nudgebee.atlas_schema_revisions) before handing off to atlas
# bash runs the script; curl does the services-server / RabbitMQ health
# probes; ca-certificates/tzdata cover TLS + timezone; libc6-compat lets the
# glibc-linked atlas binary run on Alpine's musl.
# OS-package cache-bust: bumping OS_PKG_EPOCH (ISO week) changes this layer's
# cache key so BuildKit re-runs the apk upgrade and pulls current Alpine security
# patches — a registry cache otherwise serves a stale package layer forever.
# Committed (not a build-arg) so each refresh is a reviewable, revertable diff.
# W36 pulls patched Alpine 3.22 packages including OpenSSL 3.5.8-r0 and PostgreSQL client 17.11-r0.
ARG OS_PKG_EPOCH=2026-W36
RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" && \
    apk upgrade --no-cache && \
    apk add --no-cache bash postgresql-client curl ca-certificates tzdata libc6-compat

RUN mkdir -p /hasura-migrations

COPY ./ /hasura-migrations

WORKDIR /hasura-migrations

# Atlas Community edition (Apache 2.0, ariga/atlas). ATLAS_NO_UPDATE_NOTIFIER
# suppresses the default daily HTTP update check so the migration Job has no
# implicit egress requirement on release.ariga.io (relevant for air-gapped /
# NetworkPolicy environments like rackspace). The downloaded binary is
# SHA256-verified against the per-arch checksum the release pipeline
# publishes alongside it.
ARG ATLAS_VERSION=v1.3.0
ENV ATLAS_NO_UPDATE_NOTIFIER=1
RUN case "${TARGETARCH}" in \
        amd64) ATLAS_ARCH="amd64"; ATLAS_SHA256="10d7913e3dce43ab99b8d71534a4cbadaf11a16dc293adf3b91d10e83a0ac70b" ;; \
        arm64) ATLAS_ARCH="arm64"; ATLAS_SHA256="082188c57a53439596a3ee52173b12e785aa83ec718505674e2052db0fec0c4c" ;; \
        *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && curl -fsSL \
        --connect-timeout 30 \
        --max-time 300 \
        --retry 5 \
        --retry-delay 10 \
        --retry-all-errors \
        "https://release.ariga.io/atlas/atlas-community-linux-${ATLAS_ARCH}-${ATLAS_VERSION}" \
        -o /usr/local/bin/atlas \
    && echo "${ATLAS_SHA256}  /usr/local/bin/atlas" | sha256sum -c - \
    && chmod +x /usr/local/bin/atlas \
    && atlas version

# Run the Job as a non-root user. run-migrations.sh only reads the world-
# readable migration files under /hasura-migrations and talks to Postgres /
# RabbitMQ over the network — it writes nothing to disk — so no root is
# needed. Alpine has no built-in unprivileged user, so create one at uid 1000
# (matching the uid of the `ubuntu` user the previous ubuntu:24.04 base
# provided). USER lands AFTER the atlas install so we still have root to
# write /usr/local/bin/atlas; atlas is chmod +x above and readable by all
# users at runtime.
RUN adduser -D -u 1000 appuser
USER appuser

CMD ["/bin/bash", "./run-migrations.sh"]

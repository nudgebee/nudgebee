# Migration Job image — Atlas Community edition (Apache 2.0, ariga/atlas)
# replaces golang-migrate as the sole engine. Atlas tracks per-file revisions
# in nudgebee.atlas_schema_revisions and applies out-of-order arrivals
# natively (--exec-order non-linear in atlas.hcl), eliminating the
# silent-skip / phantom-version / CONCURRENTLY-in-tx incident classes that
# golang-migrate's single-row tracker kept producing (Fixes #33007). The
# golang-migrate binary and its multi-stage builder are no longer needed and
# have been removed.

# Alpine 3.23: 3.22 stays on curl 8.14.1, which scanners report with 23
# critical/high CVEs and no fix. Alpine base: its OS packages are continuously
# patched, unlike ubuntu:24.04
# whose noble snapshot accrues fixable-but-unpatched CVEs. This image only
# needs psql + the atlas binary + bash + curl, all clean on Alpine.
# Atlas Community (Apache 2.0, ariga/atlas) compiled from a pinned commit, not the
# release binary: that binary carries the Go toolchain and module versions frozen
# at release time, so its CVEs stay until upstream cuts a new release. Only the
# modules a scan flags are bumped, each to an explicit version, so every build is
# reproducible and every bump shows up in a diff. When a new Atlas release ships
# them, drop the go get. x/net@v0.60.0 requires x/text@v0.42.0.
# CGO is off because only SQLite needs it; we only talk to Postgres.
FROM --platform=$BUILDPLATFORM ghcr.io/nudgebee/golang:1.27.3-alpine AS atlas-build
ARG TARGETOS
ARG TARGETARCH
ARG ATLAS_VERSION=v1.3.0
ARG ATLAS_COMMIT=9a6bc601212130aaaefcbc8dd36c710baf9716ff
RUN apk add --no-cache git && \
    git init -q /src && cd /src && \
    git remote add origin https://github.com/ariga/atlas && \
    git fetch -q --depth 1 origin "${ATLAS_COMMIT}" && git checkout -q FETCH_HEAD && \
    cd cmd/atlas && \
    go get \
        golang.org/x/net@v0.60.0 \
        golang.org/x/text@v0.42.0 \
        google.golang.org/grpc@v1.83.2 && \
    go mod tidy && \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" go build -trimpath \
        -ldflags "-s -w -X ariga.io/atlas/cmd/atlas/internal/cmdapi.version=${ATLAS_VERSION}" \
        -o /out/atlas .

FROM ghcr.io/nudgebee/alpine:3.23

ENV TZ=Etc/UTC

# postgresql17-client provides psql (pinned: same major as on Alpine 3.22), used by run-migrations.sh to
#   * pre-create the nudgebee schema (Atlas does not auto-create the schema
#     its revisions table lives in)
#   * detect cutover state (legacy nudgebee.schema_migrations vs new
#     nudgebee.atlas_schema_revisions) before handing off to atlas
# bash runs the script; curl does the services-server / RabbitMQ health
# probes; ca-certificates/tzdata cover TLS + timezone. python3 runs the
# search_path checks in run-migrations.sh. Stdlib only, no pip or venv.
# OS-package cache-bust: bumping OS_PKG_EPOCH (ISO week) changes this layer's
# cache key so BuildKit re-runs the apk upgrade and pulls current Alpine security
# patches — a registry cache otherwise serves a stale package layer forever.
# Committed (not a build-arg) so each refresh is a reviewable, revertable diff.
# W44 pulls ca-certificates 20261020, which drops the distrusted emSign ECC Root CA - G3.
ARG OS_PKG_EPOCH=2026-W44
RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" && \
    apk upgrade --no-cache && \
    apk add --no-cache bash postgresql17-client curl ca-certificates tzdata python3

RUN mkdir -p /hasura-migrations

COPY ./ /hasura-migrations

WORKDIR /hasura-migrations

# ATLAS_NO_UPDATE_NOTIFIER suppresses the default daily HTTP update check so the
# migration Job has no implicit egress requirement on release.ariga.io (relevant
# for air-gapped / NetworkPolicy environments).
ENV ATLAS_NO_UPDATE_NOTIFIER=1
COPY --from=atlas-build /out/atlas /usr/local/bin/atlas
RUN atlas version

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

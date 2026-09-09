# Mirror of the upstream Prometheus Community postgres_exporter.
#
# Replaces the previous frozen `bitnamilegacy/postgres-exporter:0.15.0-debian-11`
# build. That image's CVEs live in a stale Go binary (built on Go 1.20.12, with
# old x/crypto / x/net) PLUS the debian-11 base — 2 CRITICAL / 35 HIGH fixable
# and a debian-11 NOFIX floor that an `apt-get upgrade` layer cannot touch
# (verified: bumping within bitnamilegacy does not rebuild the Go binary; 0.17.1
# is actually worse, 2C/60H).
#
# The upstream image is the SAME postgres_exporter project bitnami repackaged,
# but current and distroless: it scans 0 CRITICAL and 0 fixable HIGH except the
# industry-wide Go-stdlib pair (CVE-2026-39822 / -42505, the same one in gh),
# and 0 unfixable. The bitnami PostgreSQL chart's metrics sidecar drives it with
# the upstream DATA_SOURCE_URI / DATA_SOURCE_USER / DATA_SOURCE_PASS env, so this
# is a drop-in for how we invoke it (verified end-to-end: pg_up=1 against
# postgres:16 running as uid 1001, metrics served on :9187).
#
# Re-publish to ghcr.io/nudgebee so the cluster pulls it from our namespace
# instead of quay directly. `OS_PKG_EPOCH` is accepted from the shared
# infra-mirrors workflow but unused here — the image is distroless, there is
# nothing to apt-upgrade; freshness comes from bumping the pinned tag.
#
# -nb1 swaps in the exporter rebuilt on Go 1.26.5. v0.20.1 is the newest upstream
# release and its published binary is built on Go 1.26.4, which carries the
# stdlib pair CVE-2026-39822 (HIGH, os.Root symlink traversal) / CVE-2026-42505
# (MEDIUM, crypto/tls ECH) — the only fixable findings left in this image. No tag
# bump clears them because there is no newer release, so rebuild the SAME version
# from source on the patched toolchain. Same technique as the redis
# wait-for-port / postgres gosu / llm-server gh rebuilds.
#
# The ldflags mirror what upstream's promu build stamps, so
# `postgres_exporter_build_info` keeps reporting version 0.20.1 rather than an
# empty string — dashboards and alerts key on that label. PGEXP_REVISION is the
# commit the v0.20.1 tag points at; bump both together.
FROM golang:1.26.6-alpine AS exporter-build
ENV GOTOOLCHAIN=local CGO_ENABLED=0
ARG PGEXP_VERSION=v0.20.1
ARG PGEXP_REVISION=eeda61f11c918c1b8c0d410b911e4b004669e57a
RUN go install \
      -ldflags="-X github.com/prometheus/common/version.Version=${PGEXP_VERSION#v} \
                -X github.com/prometheus/common/version.Revision=${PGEXP_REVISION} \
                -X github.com/prometheus/common/version.Branch=HEAD \
                -X github.com/prometheus/common/version.BuildUser=nudgebee-mirror" \
      "github.com/prometheus-community/postgres_exporter/cmd/postgres_exporter@${PGEXP_VERSION}"

ARG OS_PKG_EPOCH
FROM quay.io/prometheuscommunity/postgres-exporter:v0.20.1
# Upstream ENTRYPOINT is ["/bin/postgres_exporter"] — overwrite in place.
COPY --from=exporter-build /go/bin/postgres_exporter /bin/postgres_exporter

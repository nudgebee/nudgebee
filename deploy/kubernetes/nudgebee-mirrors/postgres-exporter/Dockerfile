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
# Pure re-publish to ghcr.io/nudgebee (no modification) so the cluster pulls it
# from our namespace instead of quay directly. `OS_PKG_EPOCH` is accepted from
# the shared infra-mirrors workflow but unused here — the image is distroless,
# there is nothing to apt-upgrade; freshness comes from bumping the pinned tag.
ARG OS_PKG_EPOCH
FROM quay.io/prometheuscommunity/postgres-exporter:v0.20.1

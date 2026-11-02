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
# -nb2 swaps in the exporter rebuilt on Go 1.26.10 with x/text 0.42.0. v0.20.1 is
# the newest upstream release, so no tag bump clears those findings, and rebuilds
# the SAME version from source. Same technique as the redis wait-for-port /
# postgres gosu / llm-server gh rebuilds. The runtime moves from upstream's
# busybox image to distroless static, which drops busybox's unfixed CVEs: the
# exporter needs no shell, and the chart's probes are HTTP.
#
# -nb3 adds x/net 0.62.0 and x/crypto 0.58.0 (1 CRITICAL / 8 HIGH, GO-2026-6612
# and others). Grype's direct image scan missed them; see security-scan.yaml.
#
# The ldflags mirror what upstream's promu build stamps, so
# `postgres_exporter_build_info` keeps reporting version 0.20.1 rather than an
# empty string, since dashboards and alerts key on that label. PGEXP_REVISION is
# the commit the v0.20.1 tag points at; bump both together.
FROM ghcr.io/nudgebee/golang:1.26.10-alpine AS exporter-build
ENV GOTOOLCHAIN=local CGO_ENABLED=0
ARG PGEXP_VERSION=v0.20.1
ARG PGEXP_REVISION=eeda61f11c918c1b8c0d410b911e4b004669e57a
RUN apk add --no-cache git \
    && git clone --depth 1 --branch "${PGEXP_VERSION}" \
        https://github.com/prometheus-community/postgres_exporter /src
WORKDIR /src
RUN go get golang.org/x/text@v0.43.0 golang.org/x/net@v0.62.0 golang.org/x/crypto@v0.58.0 \
    && go mod tidy
RUN go build -trimpath \
      -ldflags="-X github.com/prometheus/common/version.Version=${PGEXP_VERSION#v} \
                -X github.com/prometheus/common/version.Revision=${PGEXP_REVISION} \
                -X github.com/prometheus/common/version.Branch=HEAD \
                -X github.com/prometheus/common/version.BuildUser=nudgebee-mirror" \
      -o /postgres_exporter ./cmd/postgres_exporter

ARG OS_PKG_EPOCH
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=exporter-build /postgres_exporter /bin/postgres_exporter
EXPOSE 9187
ENTRYPOINT ["/bin/postgres_exporter"]

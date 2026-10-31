# Patched mirror of the official Debian PostgreSQL image, the chart's database.
# Debian rather than Alpine because existing indexes use glibc collation.
#
# The official image's prebuilt gosu is built on Go 1.24.6, and those stdlib CVEs
# are the only fixable findings in the image. Rebuild the same gosu source on a
# patched Go, as the Alpine postgres mirror does, and take current Debian
# patches. Same PostgreSQL version and base.

# Stage 1: rebuild gosu on a patched Go toolchain.
FROM ghcr.io/nudgebee/golang:1.26.10-alpine AS gosu
ENV GOTOOLCHAIN=local CGO_ENABLED=0
RUN go install github.com/tianon/gosu@1.19

ARG OS_PKG_EPOCH=2026-W44
FROM docker.io/library/postgres:16.15-trixie

ARG OS_PKG_EPOCH

RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get upgrade -y --no-install-recommends \
    && rm -rf /var/lib/apt/lists/*
COPY --from=gosu /go/bin/gosu /usr/local/bin/gosu

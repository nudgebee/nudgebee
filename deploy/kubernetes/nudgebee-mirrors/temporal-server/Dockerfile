# Patched mirror of temporalio/server. Every Critical and High finding in the
# upstream 1.31.4 image is inside the temporal-server binary, built on Go 1.26.8
# with x/net v0.58.0 and go-jose v4.1.4. No newer 1.31.x exists, so rebuild the same
# tag on a patched Go, with upstream's build tag. Drop this mirror once an upstream
# release carries the fixes.
#
# The final stage is Red Hat UBI 9 minimal (Red Hat container certification, so the
# image can ship in OpenShift / Red Hat catalogs), not the upstream Alpine image. It
# keeps upstream's layout: /usr/local/bin/temporal-server, /etc/temporal/entrypoint.sh
# as the default CMD, uid 1000. It also runs under an arbitrary OpenShift uid in group 0.
#
# Keep TEMPORAL_VERSION and temporal-admin-tools/Dockerfile on the
# same release. Admin-tools runs the schema jobs and must match the server.

# Stage 1: rebuild temporal-server on a patched Go toolchain.
FROM ghcr.io/nudgebee/golang:1.26.10-alpine AS build
ENV GOTOOLCHAIN=local CGO_ENABLED=0
ARG TEMPORAL_VERSION=v1.31.4
RUN apk add --no-cache git \
    && git clone --depth 1 --branch "${TEMPORAL_VERSION}" \
        https://github.com/temporalio/temporal /src
WORKDIR /src
RUN go get golang.org/x/net@v0.60.0 github.com/go-jose/go-jose/v4@v4.1.6 \
    && go mod tidy
RUN go build -trimpath -tags disable_grpc_modules -ldflags "-s -w" \
    -o /out/temporal-server ./cmd/server

# Stage 2: UBI 9 minimal runtime. hostname and getent (glibc) are what entrypoint.sh uses
# to find the pod IP. Bump OS_PKG_EPOCH to force a fresh package layer.
FROM registry.access.redhat.com/ubi9/ubi-minimal:9.9
ARG TEMPORAL_VERSION=1.31.4
ARG IMAGE_RELEASE=1
ARG OS_PKG_EPOCH=2026-W46
LABEL name="nudgebee-temporal-server" \
      maintainer="nudgebee" \
      vendor="nudgebee" \
      version="${TEMPORAL_VERSION}" \
      release="${IMAGE_RELEASE}" \
      summary="Temporal server" \
      description="Temporal server ${TEMPORAL_VERSION}, rebuilt on a patched Go toolchain for the Nudgebee platform." \
      com.temporal.server.version="${TEMPORAL_VERSION}" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.url="https://github.com/temporalio/temporal"

RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && microdnf -y upgrade --refresh \
    && microdnf -y install --nodocs --setopt=install_weak_deps=0 hostname \
    && microdnf clean all && rm -rf /var/cache/yum /var/cache/dnf \
    && echo "temporal:x:1000:0:temporal:/home/temporal:/sbin/nologin" >> /etc/passwd \
    && mkdir -p /home/temporal /etc/temporal \
    && chgrp -R 0 /home/temporal && chmod -R g=u /home/temporal

# Red Hat certification (HasLicense): Temporal's own MIT license, from the source tag.
COPY --from=build /src/LICENSE /licenses/LICENSE
COPY --chmod=755 --from=build /out/temporal-server /usr/local/bin/temporal-server
COPY --chmod=755 entrypoint.sh /etc/temporal/entrypoint.sh

ENV HOME=/home/temporal
WORKDIR /etc/temporal
USER 1000
CMD ["/etc/temporal/entrypoint.sh"]

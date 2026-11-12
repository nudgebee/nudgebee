# Patched mirror of temporalio/admin-tools. See temporal-server/Dockerfile for why.
# All findings are in the Go binaries, so rebuild the same tags on a patched Go and
# rebuild the image around them. The server tools come from the temporal repo.
# The temporal CLI comes from temporalio/cli, at the version upstream's image labels
# as com.temporal.cli.version. Drop this mirror with the temporal-server one.
#
# The final stage is Red Hat UBI 9 minimal (Red Hat container certification), not the
# upstream Alpine image. Same layout: tools in /usr/local/bin, SQL/Cassandra schemas in
# /etc/temporal/schema, sh and nc available, uid 1000, default CMD a standing shell.

# Stage 1: rebuild the server-side tools.
FROM ghcr.io/nudgebee/golang:1.26.10-alpine AS tools
ENV GOTOOLCHAIN=local CGO_ENABLED=0
ARG TEMPORAL_VERSION=v1.31.4
RUN apk add --no-cache git \
    && git clone --depth 1 --branch "${TEMPORAL_VERSION}" \
        https://github.com/temporalio/temporal /src
WORKDIR /src
RUN go get golang.org/x/net@v0.60.0 github.com/go-jose/go-jose/v4@v4.1.6 \
    && go mod tidy
RUN for t in sql:temporal-sql-tool cassandra:temporal-cassandra-tool \
        elasticsearch:temporal-elasticsearch-tool tdbg:tdbg; do \
        go build -trimpath -tags disable_grpc_modules -ldflags "-s -w" \
            -o "/out/${t#*:}" "./cmd/tools/${t%%:*}" || exit 1; \
    done

# Stage 2: rebuild the temporal CLI.
FROM ghcr.io/nudgebee/golang:1.26.10-alpine AS cli
ENV GOTOOLCHAIN=local CGO_ENABLED=0
ARG CLI_VERSION=1.9.1
RUN apk add --no-cache git \
    && git clone --depth 1 --branch "v${CLI_VERSION}" \
        https://github.com/temporalio/cli /src
WORKDIR /src
RUN go get golang.org/x/net@v0.60.0 github.com/go-jose/go-jose/v4@v4.1.6 \
        github.com/labstack/echo/v4@v4.16.1 \
        github.com/gomarkdown/markdown@v0.0.0-20260724235538-61af6e1e6971 \
    && go mod tidy
RUN go build -trimpath \
    -ldflags "-s -w -X github.com/temporalio/cli/internal/temporalcli.Version=${CLI_VERSION}" \
    -o /out/temporal ./cmd/temporal

# Stage 3: UBI 9 minimal runtime. nmap-ncat provides nc, which upstream's image has
# (busybox) for connectivity checks. Bump OS_PKG_EPOCH to force a fresh package layer.
FROM registry.access.redhat.com/ubi9/ubi-minimal:9.9
ARG TEMPORAL_VERSION=1.31.4
ARG CLI_VERSION=1.9.1
ARG IMAGE_RELEASE=1
ARG OS_PKG_EPOCH=2026-W46
LABEL name="nudgebee-temporal-admin-tools" \
      maintainer="nudgebee" \
      vendor="nudgebee" \
      version="${TEMPORAL_VERSION}" \
      release="${IMAGE_RELEASE}" \
      summary="Temporal admin tools" \
      description="Temporal ${TEMPORAL_VERSION} schema tools, tdbg and temporal CLI ${CLI_VERSION}, rebuilt on a patched Go toolchain for the Nudgebee platform." \
      com.temporal.server.version="${TEMPORAL_VERSION}" \
      com.temporal.cli.version="${CLI_VERSION}" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.url="https://github.com/temporalio/temporal"

RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && microdnf -y upgrade --refresh \
    && microdnf -y install --nodocs --setopt=install_weak_deps=0 nmap-ncat \
    && microdnf clean all && rm -rf /var/cache/yum /var/cache/dnf \
    && echo "temporal:x:1000:0:temporal:/home/temporal:/sbin/nologin" >> /etc/passwd \
    && mkdir -p /home/temporal \
    && chgrp -R 0 /home/temporal && chmod -R g=u /home/temporal

# Red Hat certification (HasLicense): Temporal's own MIT license, from the source tag.
COPY --from=tools /src/LICENSE /licenses/LICENSE
COPY --from=tools /src/schema /etc/temporal/schema
COPY --chmod=755 --from=tools /out/ /usr/local/bin/
COPY --chmod=755 --from=cli /out/temporal /usr/local/bin/temporal

ENV HOME=/home/temporal
WORKDIR /home/temporal
USER 1000
CMD ["sh", "-c", "trap exit INT HUP TERM; sleep infinity"]

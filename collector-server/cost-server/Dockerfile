# syntax=docker/dockerfile:1.7
# Upstream github.com/opencost/opencost is public, so no GOPRIVATE/token is needed.
FROM ghcr.io/nudgebee/golang:1.26.10-alpine AS build-stage

# Use the builder image's bundled Go toolchain, never an auto-downloaded one.
# Pins the compile to the base's Go and gives the build layer a distinct cache
# key so a stale toolchain can't slip back in via edge.yaml's registry cache.
ENV GOTOOLCHAIN=local

WORKDIR /app

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /app/cost-server ./cmd
RUN chmod +x /app/cost-server


FROM ghcr.io/nudgebee/alpine:3.22 AS release-stage
ARG IMAGE_VERSION=0.0.0
ARG IMAGE_RELEASE=1
LABEL name="nudgebee-cost-server" \
      maintainer="nudgebee" \
      vendor="nudgebee" \
      version="${IMAGE_VERSION}" \
      release="${IMAGE_RELEASE}" \
      summary="Nudgebee cost server" \
      description="Computes and serves Kubernetes and cloud cost data for the Nudgebee platform."
ARG OS_PKG_EPOCH=2026-W44
RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apk upgrade --no-cache \
    && apk add --no-cache ca-certificates \
    && adduser -D -g '' appuser
WORKDIR /app
COPY --from=build-stage /app/cost-server /app/cost-server
# Binary stays root-owned, world-readable/executable — appuser can run but not
# overwrite it (container immutability; avoids a chown layer-copy of the binary).
RUN chmod +x /app/cost-server
USER appuser
EXPOSE 9003
CMD ["./cost-server"]

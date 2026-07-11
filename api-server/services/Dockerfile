# syntax=docker/dockerfile:1.7
FROM golang:1.26.5-alpine AS build-stage

# Use the builder image's bundled Go toolchain, never an auto-downloaded one.
# Pins the compile to the base's Go and gives the build layer a distinct cache
# key so a stale toolchain can't slip back in via edge.yaml's registry cache.
ENV GOTOOLCHAIN=local

# Set destination for COPY
WORKDIR /app

# Download Go modules
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY ./ ./

# Build
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build  -o /app/services ./cmd
RUN chmod +x /app/services
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go install github.com/google/pprof@latest
EXPOSE 8000
CMD ["/app/services"]

FROM alpine:3.22 AS release-stage
# OS-package cache-bust: bumping OS_PKG_EPOCH (ISO week) changes this layer's
# cache key so BuildKit re-runs the apk install and pulls current Alpine security
# patches — its registry cache (mode=max) otherwise serves a stale package layer
# forever. Committed here (not a build-arg) so each refresh is a reviewable,
# revertable diff; bump it when the Trivy scan flags a stale package (e.g. c-ares
# CVE-2026-33630).
ARG OS_PKG_EPOCH=2026-W28
RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apk upgrade --no-cache \
    && apk add --no-cache git graphviz ca-certificates \
    && adduser -D -g '' appuser
WORKDIR /app
COPY --from=build-stage /app/services /app/services
COPY --from=build-stage /go/bin/pprof /usr/local/bin/pprof
COPY --from=build-stage /app/knowledge_graph/core/default_relationships.json /app/knowledge_graph/core/default_relationships.json
# Files stay root-owned, world-readable/executable — appuser runs the binary and
# reads the JSON but cannot overwrite them (container immutability; also avoids a
# chown -R layer-copy of the whole /app tree).
RUN chmod +x /app/services
USER appuser
EXPOSE 8000
CMD ["/app/services"]

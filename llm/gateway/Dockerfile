# NB AI Gateway image. Multi-stage: static Go binary → minimal alpine runtime.
# Depends on the published github.com/maximhq/bifrost/core module — a normal
# `go build`, no fork or replace.

FROM ghcr.io/nudgebee/golang:1.26.10 AS build
WORKDIR /app

COPY ./ ./

ARG TARGETOS
ARG TARGETARCH
ENV CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64}
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -o /app/llm-gateway ./cmd/gateway

# --- runtime ---
FROM ghcr.io/nudgebee/alpine:3.22
# Bumping OS_PKG_EPOCH changes this layer's cache key so the registry cache
# can't serve a stale package layer (same convention as api-server/services).
ARG OS_PKG_EPOCH=2026-W44
RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apk upgrade --no-cache \
    && apk add --no-cache ca-certificates \
    && adduser -D -u 10001 gateway
WORKDIR /app
COPY --from=build /app/llm-gateway /app/llm-gateway
USER gateway
EXPOSE 8000
ENTRYPOINT ["/app/llm-gateway"]

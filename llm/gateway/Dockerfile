# syntax=docker/dockerfile:1.7
# NB AI Gateway image. Multi-stage: static Go binary → minimal alpine runtime.
# Depends on the published github.com/maximhq/bifrost/core module — a normal
# `go build`, no fork or replace.

FROM golang:1.26 AS build
WORKDIR /app

COPY ./ ./

ARG TARGETOS
ARG TARGETARCH
ENV CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64}
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -o /app/llm-gateway ./cmd/gateway

# --- runtime ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 gateway
WORKDIR /app
COPY --from=build /app/llm-gateway /app/llm-gateway
USER gateway
EXPOSE 8000
ENTRYPOINT ["/app/llm-gateway"]

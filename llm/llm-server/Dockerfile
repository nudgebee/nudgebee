# syntax=docker/dockerfile:1.7
# Dockerfile (for your Go application)
# Build this after building your base image: docker build -t your-app-image .

# Runtime base for the final stage. MUST be declared before the first FROM:
# an ARG declared after a FROM is scoped to that stage and is invisible to
# later FROM lines, so a global ARG is the only way to parameterize the base.
# OSS installs use the public GHCR mirror of our internal llm-server base
# image (Ubuntu noble + Playwright preinstalled). EE deploys override
# RUNTIME_BASE to their internal registry via --build-arg.
ARG RUNTIME_BASE=ghcr.io/nudgebee/nudgebee-llm-server-base:ubuntu-noble-20260430

# --- Build Stage ---
FROM golang:1.26 AS build-stage

ARG TARGETPLATFORM
ARG TARGETOS
ARG TARGETARCH

ENV GOOS=${TARGETOS:-linux}
ENV GOARCH=${TARGETARCH:-amd64}
ENV CGO_ENABLED=0

WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download -x

COPY ./ ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -o /app/services ./cmd
RUN chmod +x /app/services

# --- Final Stage ---
FROM ${RUNTIME_BASE}

WORKDIR /app

# Copy the compiled Go application from the build stage
COPY --from=build-stage /app/services /app/services

EXPOSE 8000
ENTRYPOINT ["/app/services"]

# syntax=docker/dockerfile:1.7

# Runtime base. OSS installs use the public GHCR mirror of our internal
# cloud-collector base image (Alpine + pre-baked tooling). EE deploys
# override RUNTIME_BASE to their internal registry via --build-arg.
ARG RUNTIME_BASE=ghcr.io/nudgebee/nudgebee-cloud-collector-base:20260112-0715

FROM golang:1.26-alpine AS build-stage

WORKDIR /app

# Install git (needed for go mod)
RUN apk add --no-cache git

# Copy only go mod files first (better caching)
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# Copy application source
COPY . .

# Build binary
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 \
    GOOS=linux \
    go build -o /app/cloud-collector-server ./cmd

FROM ${RUNTIME_BASE}

WORKDIR /app

# Copy only the compiled binary
COPY --from=build-stage /app/cloud-collector-server /app/cloud-collector-server

RUN chmod +x /app/cloud-collector-server

EXPOSE 8000

CMD ["/app/cloud-collector-server"]

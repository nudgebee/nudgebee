# syntax=docker/dockerfile:1.7
# Dockerfile (for your Go application)
# Build this after building your base image: docker build -t your-app-image .

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
    go build -o /app/services cmd/*.go
RUN chmod +x /app/services

# --- Final Stage ---
# Use the pre-built Playwright base image
FROM registry.nudgebee.com/nudgebee-llm-server-base:ubuntu-noble-20260430

WORKDIR /app

# Copy the compiled Go application from the build stage
COPY --from=build-stage /app/services /app/services

EXPOSE 8000
ENTRYPOINT ["/app/services"]

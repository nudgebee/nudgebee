# syntax=docker/dockerfile:1.7
FROM golang:1.26-alpine as build-stage

# Set destination for COPY
WORKDIR /app

# Ensure static Go binary builds
ENV CGO_ENABLED=0

# Download Go modules
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY ./ ./

# Build
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=linux go build  -o /app/services cmd/*.go
RUN chmod +x /app/services
EXPOSE 8000
CMD ["/app/services"]



FROM alpine:3.19 AS release-stage
RUN apk add aws-cli jq
WORKDIR /app
COPY --from=build-stage /app/services /app/services
RUN chmod +x /app/services
EXPOSE 8000
CMD ["/app/services"]

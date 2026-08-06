# syntax=docker/dockerfile:1.7
FROM golang:1.26.5-alpine as build-stage

# Use the builder image's bundled Go toolchain, never an auto-downloaded one.
ENV GOTOOLCHAIN=local

WORKDIR /app

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY ./ ./

# CGO stays off: the SQLite driver under grype is pure Go (modernc.org/sqlite).
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -o /app/vuln-matcher-server .
RUN chmod +x /app/vuln-matcher-server
RUN rm -rf /go

# The vulnerability database lives on an ephemeral volume mounted here; it is
# refreshed from our own mirror, never baked into the image.
ENV VULN_DB_ROOT=/var/lib/vuln-matcher/db

EXPOSE 8080
CMD ["/app/vuln-matcher-server"]

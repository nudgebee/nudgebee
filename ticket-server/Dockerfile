# syntax=docker/dockerfile:1.7
FROM golang:1.26.4-alpine as build-stage


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
    CGO_ENABLED=0 GOOS=linux go build  -o /app/ticket-server .
RUN chmod +x /app/ticket-server
RUN rm -rf /go

EXPOSE 8000
CMD ["/app/ticket-server"]


FROM alpine:3.22 AS release-stage
RUN apk add --no-cache ca-certificates \
    && adduser -D -g '' appuser
WORKDIR /app
COPY --from=build-stage /app/ticket-server /app/ticket-server
RUN chmod +x /app/ticket-server \
    && chown appuser:appuser /app/ticket-server
USER appuser
EXPOSE 8000
CMD ["/app/ticket-server"]

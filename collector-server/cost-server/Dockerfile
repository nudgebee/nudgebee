# syntax=docker/dockerfile:1.7
# Upstream github.com/opencost/opencost is public, so no GOPRIVATE/token is needed.
FROM golang:1.26.4-alpine AS build-stage

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


FROM alpine:3.22 AS release-stage
RUN apk add --no-cache ca-certificates \
    && adduser -D -g '' appuser
WORKDIR /app
COPY --from=build-stage /app/cost-server /app/cost-server
# Binary stays root-owned, world-readable/executable — appuser can run but not
# overwrite it (container immutability; avoids a chown layer-copy of the binary).
RUN chmod +x /app/cost-server
USER appuser
EXPOSE 9003
CMD ["./cost-server"]

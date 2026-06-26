# syntax=docker/dockerfile:1.7
# Upstream github.com/opencost/opencost is public, so no GOPRIVATE/token is needed.
FROM golang:1.26-alpine AS build-stage

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


FROM alpine:3.19 AS release-stage
WORKDIR /app
COPY --from=build-stage /app/cost-server /app/cost-server
RUN chmod +x /app/cost-server
EXPOSE 9003
CMD ["./cost-server"]

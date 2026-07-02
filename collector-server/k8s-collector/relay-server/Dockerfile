# syntax=docker/dockerfile:1.7
FROM golang:1.26.4-alpine AS build-stage

WORKDIR /app

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -o /app/nudgebee-relay-server ./cmd
ENV GIN_MODE=release
RUN chmod +x /app/nudgebee-relay-server
EXPOSE 8080
CMD [ "./nudgebee-relay-server" ]


FROM alpine:3.22 AS release-stage
RUN apk add --no-cache ca-certificates \
    && adduser -D -g '' appuser
WORKDIR /app
COPY --from=build-stage /app/nudgebee-relay-server /app/nudgebee-relay-server
# Binary stays root-owned, world-readable/executable — appuser can run but not
# overwrite it (container immutability; avoids a chown layer-copy of the binary).
RUN chmod +x /app/nudgebee-relay-server
USER appuser
EXPOSE 8080
CMD ["./nudgebee-relay-server"]

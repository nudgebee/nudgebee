# syntax=docker/dockerfile:1.7
FROM golang:1.26-alpine AS build-stage

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


FROM alpine:3.19 AS release-stage
WORKDIR /app
COPY --from=build-stage /app/nudgebee-relay-server /app/nudgebee-relay-server
RUN chmod +x /app/nudgebee-relay-server
EXPOSE 8080
CMD ["./nudgebee-relay-server"]

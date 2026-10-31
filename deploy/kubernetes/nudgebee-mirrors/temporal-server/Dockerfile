# Patched mirror of temporalio/server. Every Critical and High finding in the
# upstream 1.31.4 image is inside the temporal-server binary, built on Go 1.26.8
# with x/net v0.58.0 and go-jose v4.1.4. No newer 1.31.x exists, so rebuild the same
# tag on a patched Go, with upstream's build tag, and swap only that binary into
# the upstream image. Drop this mirror once an upstream release carries the fixes.
#
# Keep TEMPORAL_VERSION, the FROM below and temporal-admin-tools/Dockerfile on the
# same release. Admin-tools runs the schema jobs and must match the server.

# Stage 1: rebuild temporal-server on a patched Go toolchain.
FROM ghcr.io/nudgebee/golang:1.26.10-alpine AS build
ENV GOTOOLCHAIN=local CGO_ENABLED=0
ARG TEMPORAL_VERSION=v1.31.4
RUN apk add --no-cache git \
    && git clone --depth 1 --branch "${TEMPORAL_VERSION}" \
        https://github.com/temporalio/temporal /src
WORKDIR /src
RUN go get golang.org/x/net@v0.60.0 github.com/go-jose/go-jose/v4@v4.1.6 \
    && go mod tidy
RUN go build -trimpath -tags disable_grpc_modules -ldflags "-s -w" \
    -o /out/temporal-server ./cmd/server

FROM ghcr.io/nudgebee/temporal-server:1.31.4
COPY --chmod=755 --from=build /out/temporal-server /usr/local/bin/temporal-server

# Patched mirror of temporalio/admin-tools. See temporal-server/Dockerfile for why.
# All findings are in the Go binaries, so rebuild the same tags on a patched Go and
# swap them into the upstream image. The server tools come from the temporal repo.
# The temporal CLI comes from temporalio/cli, at the version upstream's image labels
# as com.temporal.cli.version. Drop this mirror with the temporal-server one.

# Stage 1: rebuild the server-side tools.
FROM ghcr.io/nudgebee/golang:1.26.10-alpine AS tools
ENV GOTOOLCHAIN=local CGO_ENABLED=0
ARG TEMPORAL_VERSION=v1.31.4
RUN apk add --no-cache git \
    && git clone --depth 1 --branch "${TEMPORAL_VERSION}" \
        https://github.com/temporalio/temporal /src
WORKDIR /src
RUN go get golang.org/x/net@v0.60.0 github.com/go-jose/go-jose/v4@v4.1.6 \
    && go mod tidy
RUN for t in sql:temporal-sql-tool cassandra:temporal-cassandra-tool \
        elasticsearch:temporal-elasticsearch-tool tdbg:tdbg; do \
        go build -trimpath -tags disable_grpc_modules -ldflags "-s -w" \
            -o "/out/${t#*:}" "./cmd/tools/${t%%:*}" || exit 1; \
    done

# Stage 2: rebuild the temporal CLI.
FROM ghcr.io/nudgebee/golang:1.26.10-alpine AS cli
ENV GOTOOLCHAIN=local CGO_ENABLED=0
ARG CLI_VERSION=1.9.1
RUN apk add --no-cache git \
    && git clone --depth 1 --branch "v${CLI_VERSION}" \
        https://github.com/temporalio/cli /src
WORKDIR /src
RUN go get golang.org/x/net@v0.60.0 github.com/go-jose/go-jose/v4@v4.1.6 \
        github.com/labstack/echo/v4@v4.16.1 \
        github.com/gomarkdown/markdown@v0.0.0-20260724235538-61af6e1e6971 \
    && go mod tidy
RUN go build -trimpath \
    -ldflags "-s -w -X github.com/temporalio/cli/internal/temporalcli.Version=${CLI_VERSION}" \
    -o /out/temporal ./cmd/temporal

FROM ghcr.io/nudgebee/temporal-admin-tools:1.31.4
COPY --chmod=755 --from=tools /out/ /usr/local/bin/
COPY --chmod=755 --from=cli /out/temporal /usr/local/bin/temporal

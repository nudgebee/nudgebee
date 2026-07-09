# ---- migrate builder ----
# The published golang-migrate release binaries (.deb / .tar.gz) ship ~53 CVEs:
# Go stdlib plus vendored x/crypto, x/net, jackc/pgx (CRITICAL), grpc (CRITICAL),
# otel, go-jose. trivy's image scan happened to miss them on the old ubuntu
# base, but they were always present. Building from source with a current Go
# toolchain and freshened module deps produces a clean, static binary.
FROM golang:1.26.4-alpine AS migrate-build
RUN apk add --no-cache git
WORKDIR /build
ARG GOLANG_MIGRATE_VERSION=v4.19.1
RUN git clone --depth 1 --branch "${GOLANG_MIGRATE_VERSION}" https://github.com/golang-migrate/migrate .
# Freshen the CVE-carrying transitive deps (each @latest is major-bounded by its
# module path, so no surprise major jumps) and rebuild the module graph, then
# compile a static binary with only the drivers run-migrations.sh uses
# (postgres + clickhouse).
RUN go get \
        golang.org/x/crypto@latest \
        golang.org/x/net@latest \
        github.com/jackc/pgx/v5@latest \
        google.golang.org/grpc@latest \
        go.opentelemetry.io/otel@latest \
        go.opentelemetry.io/otel/sdk@latest \
        github.com/go-jose/go-jose/v4@latest \
        go.mongodb.org/mongo-driver@latest \
    && go mod tidy
RUN CGO_ENABLED=0 go build -tags 'postgres clickhouse' \
        -ldflags "-X main.Version=${GOLANG_MIGRATE_VERSION}" \
        -o /usr/local/bin/migrate ./cmd/migrate

# ---- runtime ----
# Alpine base: its OS packages are continuously patched, unlike ubuntu:24.04
# whose noble snapshot accrues fixable-but-unpatched CVEs. This image only needs
# psql + the (source-built) migrate binary + bash + curl, all clean on Alpine.
FROM alpine:3.22

ENV TZ=Etc/UTC

# postgresql-client provides psql (run-migrations.sh pre-creates the nudgebee
# schema before golang-migrate writes its tracker there — golang-migrate does
# NOT auto-create the schema its tracker lives in). bash runs the script; curl
# does the services-server / RabbitMQ health probes; ca-certificates/tzdata
# cover TLS + timezone. apk upgrade keeps the base fresh on every rebuild.
RUN apk upgrade --no-cache && \
    apk add --no-cache bash postgresql-client curl ca-certificates tzdata

COPY --from=migrate-build /usr/local/bin/migrate /usr/local/bin/migrate

RUN mkdir -p /hasura-migrations

COPY ./ /hasura-migrations

WORKDIR /hasura-migrations

# Run the Job as a non-root user. run-migrations.sh only reads the world-readable
# migration files under /hasura-migrations and talks to Postgres/ClickHouse/
# RabbitMQ over the network — it writes nothing to disk — so no root is needed.
# Alpine has no built-in unprivileged user, so create one at uid 1000 (matching
# the uid of the `ubuntu` user the previous ubuntu:24.04 base provided).
RUN adduser -D -u 1000 appuser
USER appuser

CMD ["/bin/bash", "./run-migrations.sh"]

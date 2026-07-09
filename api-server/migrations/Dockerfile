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
# Freshen the CVE-carrying transitive deps and rebuild the module graph, then
# compile a static binary with only the drivers run-migrations.sh uses
# (postgres + clickhouse). Versions are pinned (not @latest) for reproducible
# builds — these exact versions were verified to scan 0 CVEs on a current Trivy
# DB (bump them when a scan later flags one; don't float to @latest).
RUN go get \
        golang.org/x/crypto@v0.54.0 \
        golang.org/x/net@v0.57.0 \
        github.com/jackc/pgx/v5@v5.10.0 \
        google.golang.org/grpc@v1.82.0 \
        go.opentelemetry.io/otel@v1.44.0 \
        go.opentelemetry.io/otel/sdk@v1.44.0 \
        github.com/go-jose/go-jose/v4@v4.1.4 \
        go.mongodb.org/mongo-driver@v1.17.9 \
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

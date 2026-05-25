FROM ubuntu:24.04

ARG TARGETARCH
ENV DEBIAN_FRONTEND=noninteractive

# postgresql-client provides psql, used by run-migrations.sh to ensure the
# nudgebee schema exists before golang-migrate writes its tracker table
# there. golang-migrate does NOT auto-create the schema referenced by its
# tracker table.
RUN apt-get update && \
    apt-get upgrade -y && \
    TZ=Etc/UTC apt-get install -y --no-install-recommends \
        curl \
        ca-certificates \
        tzdata \
        postgresql-client && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*


RUN mkdir -p /hasura-migrations

COPY ./ /hasura-migrations

WORKDIR /hasura-migrations

ARG GOLANG_MIGRATE_VERSION=v4.17.0
# Download, install golang-migrate, and clean up the .deb file
RUN DEB_FILENAME="migrate.linux-${TARGETARCH}.deb" \
    && curl -fsSL \
        --connect-timeout 30 \
        --max-time 300 \
        --retry 5 \
        --retry-delay 10 \
        --retry-all-errors \
        "https://github.com/golang-migrate/migrate/releases/download/${GOLANG_MIGRATE_VERSION}/${DEB_FILENAME}" \
        -o "${DEB_FILENAME}" \
    && dpkg -i "${DEB_FILENAME}" \
    && rm "${DEB_FILENAME}"
CMD ["/bin/bash", "./run-migrations.sh"]
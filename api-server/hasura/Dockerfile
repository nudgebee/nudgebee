FROM ubuntu:24.04

ARG TARGETARCH
ENV DEBIAN_FRONTEND=noninteractive

# Update, upgrade, install packages, and clean up in a single layer
RUN apt-get update && \
    apt-get upgrade -y && \
    TZ=Etc/UTC apt-get install -y --no-install-recommends \
        curl \
        ca-certificates \
        tzdata && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*


# Install Hasura CLI by downloading binary directly from releases (more reliable than get.sh)
ARG HASURA_CLI_VERSION=v2.33.0
RUN ARCH=$([ "${TARGETARCH}" = "arm64" ] && echo "arm64" || echo "amd64") \
    && curl -fsSL \
        --connect-timeout 30 \
        --max-time 300 \
        --retry 5 \
        --retry-delay 10 \
        --retry-all-errors \
        "https://github.com/hasura/graphql-engine/releases/download/${HASURA_CLI_VERSION}/cli-hasura-linux-${ARCH}" \
        -o /usr/local/bin/hasura \
    && chmod +x /usr/local/bin/hasura

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
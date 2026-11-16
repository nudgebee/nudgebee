# Patched mirror of aquasec/trivy, pulled by the on-demand scanner jobs
# (trivy_cis_scan runs it directly; image_scanner copies /usr/local/bin/trivy
# out of it into the scanned workload). Pinned in
# api-server/services/scan_orchestrator/images.go (TRIVY_IMAGE).
#
# Re-mirror + an apk upgrade layer: the upstream tag snapshot lags Alpine
# security releases (0.76.1 ships brotli-libs 1.2.0-r1, CVE-2026-111497).
# Bump OS_PKG_EPOCH (ISO week) to force a re-pull; the weekly workflow run
# passes the current week.
#
# 0.76.1 is the newest release and pins go-jose 4.1.5, so build the same source
# with go-jose 4.1.6. CGO stays off because image_scanner runs the binary inside
# arbitrary workload images.
#
# The image also carries an empty Java DB at /opt/nudgebee/java-db (see
# java-db.sql). image_scanner copies it into trivy's cache dir so JAR/WAR/EAR
# files are scanned without downloading the ~1 GB upstream Java DB.

# Stage 1: rebuild trivy.
FROM ghcr.io/nudgebee/golang:1.27.3-alpine AS trivy
ENV GOTOOLCHAIN=local CGO_ENABLED=0
ARG TRIVY_VERSION=0.76.1
RUN apk add --no-cache git \
    && git clone --depth 1 --branch "v${TRIVY_VERSION}" \
        https://github.com/aquasecurity/trivy /src
WORKDIR /src
RUN go get github.com/go-jose/go-jose/v4@v4.1.6 && go mod tidy
RUN go build -trimpath \
    -ldflags "-s -w -X github.com/aquasecurity/trivy/pkg/version/app.ver=${TRIVY_VERSION}" \
    -o /trivy ./cmd/trivy

# Stage 2: the empty Java DB, and two JARs for the smoke test below: one that
# carries a pom.properties, and one without, which trivy looks up in the DB.
FROM ghcr.io/nudgebee/alpine:3.23 AS javadb
RUN apk add --no-cache sqlite zip
COPY java-db.sql /java-db.sql
RUN mkdir -p /java-db /smoke/META-INF/maven/org.apache.logging.log4j/log4j-core \
    && sqlite3 /java-db/trivy-java.db < /java-db.sql \
    && printf '%s\n' '{"Version":1,"NextUpdate":"2099-01-01T00:00:00Z","UpdatedAt":"2026-01-01T00:00:00Z","DownloadedAt":"2026-01-01T00:00:00Z"}' \
        > /java-db/metadata.json \
    && printf 'groupId=org.apache.logging.log4j\nartifactId=log4j-core\nversion=2.14.1\n' \
        > /smoke/META-INF/maven/org.apache.logging.log4j/log4j-core/pom.properties \
    && mkdir /jar && cd /smoke && zip -qr /jar/log4j-core-2.14.1.jar META-INF \
    && echo smoke > /readme.txt && cd / && zip -q /jar/no-pom-1.0.jar readme.txt

ARG OS_PKG_EPOCH=2026-W44
FROM docker.io/aquasec/trivy:0.76.1

ARG OS_PKG_EPOCH

RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apk upgrade --no-cache
COPY --from=trivy /trivy /usr/local/bin/trivy
COPY --from=javadb /java-db /opt/nudgebee/java-db

# Fails the build when this trivy no longer works with the empty Java DB (a Java
# DB layout change upstream). Two ways that shows: trivy refuses the DB, and every
# scan of an image with a JAR fails; or it reads it, a query fails, and trivy
# logs a "Walk error" at debug level and silently drops that archive. The second
# JAR has no pom.properties, so trivy queries the DB for it.
# No network: SBOM output needs no vulnerability DB, and --skip-java-db-update
# keeps trivy on the copied DB. The log strings matched below are trivy
# 0.76.1's debug wording: re-check them when bumping trivy (a reworded log fails
# the build, it cannot pass it).
RUN --mount=type=bind,from=javadb,source=/jar,target=/smoke \
    mkdir -p /tmp/smoke-cache && cp -r /opt/nudgebee/java-db /tmp/smoke-cache/ \
    && trivy rootfs --debug --cache-dir /tmp/smoke-cache --skip-java-db-update \
        --format cyclonedx --output /tmp/smoke.json /smoke 2> /tmp/smoke.log \
    && grep -q 'pkg:maven/org.apache.logging.log4j/log4j-core@2.14.1' /tmp/smoke.json \
    && grep -q 'file_path="no-pom-1.0.jar"' /tmp/smoke.log \
    && if grep -E 'Walk error|SQL logic error|Java DB' /tmp/smoke.log | grep -v 'Java DB update was skipped'; then exit 1; fi \
    && rm -rf /tmp/smoke-cache /tmp/smoke.json /tmp/smoke.log

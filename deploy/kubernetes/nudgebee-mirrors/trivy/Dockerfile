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

ARG OS_PKG_EPOCH=2026-W44
FROM docker.io/aquasec/trivy:0.76.1

ARG OS_PKG_EPOCH

RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apk upgrade --no-cache
COPY --from=trivy /trivy /usr/local/bin/trivy

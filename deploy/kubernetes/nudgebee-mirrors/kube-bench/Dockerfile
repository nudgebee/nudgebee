# Patched mirror of aquasec/kube-bench, pulled by the on-demand kube_bench_scan
# job. Pinned in api-server/services/scan_orchestrator/images.go
# (KUBE_BENCH_IMAGE).
#
# Re-mirror + an apk upgrade layer so OS packages track Alpine security
# releases between upstream tags. Bump OS_PKG_EPOCH (ISO week) to force a
# re-pull; the weekly workflow run passes the current week.
#
# v0.16.0 is the newest release, and both Go binaries in it lag the fixes, so
# build the same sources on a patched toolchain with patched x/net and x/text.
# kube-bench only runs `kubectl version`, so the apk kubectl is replaced by a
# rebuild from the newest 1.36 patch release, the line it already shipped.

# Stage 1: rebuild kube-bench.
FROM ghcr.io/nudgebee/golang:1.26.10-alpine AS kube-bench
ENV GOTOOLCHAIN=local CGO_ENABLED=0
ARG KUBE_BENCH_VERSION=v0.16.0
RUN apk add --no-cache git \
    && git clone --depth 1 --branch "${KUBE_BENCH_VERSION}" \
        https://github.com/aquasecurity/kube-bench /src
WORKDIR /src
RUN go get golang.org/x/net@v0.60.0 golang.org/x/text@v0.42.0 && go mod tidy
RUN go build -trimpath \
    -ldflags "-X github.com/aquasecurity/kube-bench/cmd.KubeBenchVersion=${KUBE_BENCH_VERSION}" \
    -o /kube-bench .

# Stage 2: rebuild kubectl.
FROM ghcr.io/nudgebee/golang:1.26.10-alpine AS kubectl
ENV GOTOOLCHAIN=local CGO_ENABLED=0
ARG KUBECTL_VERSION=v1.36.6
RUN apk add --no-cache git \
    && git clone --depth 1 --branch "${KUBECTL_VERSION}" \
        https://github.com/kubernetes/kubernetes /src
WORKDIR /src
RUN rm -rf vendor \
    && go work edit \
        -replace golang.org/x/net=golang.org/x/net@v0.60.0 \
        -replace golang.org/x/text=golang.org/x/text@v0.42.0 \
    && go work sync
RUN go build -trimpath \
    -ldflags "-s -w -X k8s.io/client-go/pkg/version.gitVersion=${KUBECTL_VERSION} -X k8s.io/component-base/version.gitVersion=${KUBECTL_VERSION}" \
    -o /kubectl ./cmd/kubectl

ARG OS_PKG_EPOCH=2026-W44
FROM docker.io/aquasec/kube-bench:v0.16.0

ARG OS_PKG_EPOCH

RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apk upgrade --no-cache \
    && apk del --no-cache kubectl
COPY --from=kube-bench /kube-bench /usr/local/bin/kube-bench
COPY --from=kubectl /kubectl /usr/bin/kubectl

# Minimal kubectl image for the rag-server StatefulSet migration hook
# (rag-server/templates/migration-sts-hooks.yaml, .Values.migrationHookImage).
#
# Replaces the clone of docker.io/alpine/k8s, a general toolbox whose bundled
# helm, kustomize, cloud CLIs, curl and python carried hundreds of CVEs the hook
# never touches: it needs only /bin/sh and kubectl.
#
# Same kubectl source tag as the toolbox (v1.32.13), so the client/server skew
# is unchanged. Keep the 1.32 line unless the supported cluster range moves. The
# upstream release binary is built on an old Go and no 1.32.x release is newer,
# so build the same tag on a patched toolchain, the technique the postgres gosu
# and redis wait-for-port mirrors use. x/net, x/text and spdystream are replaced
# with their patched releases; kubectl vendors old copies of all three.

# Stage 1: rebuild kubectl on a patched Go toolchain.
FROM ghcr.io/nudgebee/golang:1.26.10-alpine AS kubectl
ENV GOTOOLCHAIN=local CGO_ENABLED=0
ARG KUBECTL_VERSION=v1.32.13
RUN apk add --no-cache git \
    && git clone --depth 1 --branch "${KUBECTL_VERSION}" \
        https://github.com/kubernetes/kubernetes /src
WORKDIR /src
RUN rm -rf vendor \
    && go work edit -go=1.26.0 \
        -replace golang.org/x/net=golang.org/x/net@v0.60.0 \
        -replace golang.org/x/text=golang.org/x/text@v0.39.0 \
        -replace github.com/moby/spdystream=github.com/moby/spdystream@v0.5.1 \
    && go work sync
RUN go build -trimpath \
    -ldflags "-s -w -X k8s.io/client-go/pkg/version.gitVersion=${KUBECTL_VERSION} -X k8s.io/component-base/version.gitVersion=${KUBECTL_VERSION}" \
    -o /kubectl ./cmd/kubectl

FROM ghcr.io/nudgebee/busybox:1.37.0
COPY --from=kubectl /kubectl /usr/local/bin/kubectl
# kubectl keeps its discovery cache under $HOME. A write failure there is harmless
# but noisy, and the numeric user below has no home directory.
ENV HOME=/tmp
USER 65532:65532

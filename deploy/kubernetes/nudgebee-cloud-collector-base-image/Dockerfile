FROM alpine:3.22

# Refresh OS packages to the latest patch level (alpine:3.19 was EOL — no more
# security updates; 3.22 is current). apk upgrade keeps subsequent rebuilds fresh.
# OS-package cache-bust: bumping OS_PKG_EPOCH (ISO week) changes this layer's
# cache key so BuildKit re-runs apk upgrade and pulls current Alpine security
# patches — its registry cache (mode=max) otherwise serves a stale package layer
# forever. Committed (not a build-arg) so each refresh is a reviewable, revertable
# diff; bump when the Trivy scan flags a stale package (e.g. c-ares CVE-2026-33630);
# currently W29 pulls curl 8.14.1-r3 (CVE-2026-5545).
ARG OS_PKG_EPOCH=2026-W29
RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" && apk upgrade --no-cache

# Install system deps + AWS CLI
RUN apk add --no-cache \
    coreutils \
    aws-cli \
    jq \
    curl \
    bash \
    python3 \
    py3-pip \
    gcc \
    musl-dev \
    python3-dev \
    libffi-dev \
    openssl-dev \
    cargo \
    make

# Upgrade the system-Python cryptography that Alpine's aws-cli (and the gcloud
# SDK, which runs on the system interpreter) pull in. Alpine's py3-cryptography
# lags at 44/46 — a HIGH that cloud-collector-server inherits from
# /usr/lib/pythonX/site-packages, distinct from the azure venv patched below.
# --break-system-packages is required on Alpine's PEP-668 externally-managed
# env; the gcc/cargo/openssl-dev build deps above let the 48 wheel compile.
RUN pip install --no-cache-dir --break-system-packages --upgrade "cryptography>=48.0.1"

# Azure CLI. Install azure-cli and cryptography>=48.0.1 in one resolver pass so
# pip satisfies both constraints simultaneously (azure-cli-core declares a bare
# `cryptography` requirement with no ceiling, so >=48 resolves cleanly). This
# clears the cryptography HIGH that cloud-collector-server inherits from this
# venv. Single-pass is deliberate: if a future azure-cli ever pins
# cryptography<48, this fails loudly at build instead of silently shipping an
# unsupported combo.
RUN python3 -m venv /opt/azure-cli-venv && \
    /opt/azure-cli-venv/bin/pip install --no-cache-dir --upgrade pip && \
    /opt/azure-cli-venv/bin/pip install --no-cache-dir azure-cli "cryptography>=48.0.1" && \
    ln -s /opt/azure-cli-venv/bin/az /usr/local/bin/az && \
    az extension add --name costmanagement

# Google Cloud SDK. Run gcloud on the system interpreter (cryptography>=48,
# upgraded above) instead of the SDK's bundled python: the bundle ships a frozen
# cryptography (46.x) + pip that Google patches on its own cadence, so it accrues
# HIGH/MEDIUM CVEs we cannot apk-upgrade — deleting it removes them. gcloud-crc32c
# is a Go binary that only accelerates `gcloud storage` CLI transfers (unused here
# — GCS access goes through the Go SDK), so it is dead weight carrying stdlib CVEs
# from Google's build toolchain. Validated: gcloud resolves /usr/bin/python3 and
# the service-account auth crypto path works with the bundle removed.
RUN apk add --no-cache --virtual .gcloud-build-deps git && \
    curl -o install.sh https://sdk.cloud.google.com && \
    bash install.sh --disable-prompts --install-dir=/usr/local && \
    rm install.sh && \
    rm -rf /usr/local/google-cloud-sdk/platform/bundledpythonunix /usr/local/google-cloud-sdk/bin/gcloud-crc32c && \
    apk del .gcloud-build-deps
ENV CLOUDSDK_PYTHON=/usr/bin/python3
ENV PATH="/usr/local/google-cloud-sdk/bin:$PATH"

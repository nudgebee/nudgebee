FROM ghcr.io/nudgebee/alpine:3.23

# Refresh OS packages to the latest patch level (alpine:3.19 was EOL — no more
# security updates; 3.23 is current). apk upgrade keeps subsequent rebuilds fresh.
# OS-package cache-bust: bumping OS_PKG_EPOCH (ISO week) changes this layer's
# cache key so BuildKit re-runs apk upgrade and pulls current Alpine security
# patches — its registry cache (mode=max) otherwise serves a stale package layer
# forever. Committed (not a build-arg) so each refresh is a reviewable, revertable
# diff; bump when the Trivy scan flags a stale package (e.g. c-ares CVE-2026-33630);
# currently W44 pulls ca-certificates 20261020, which drops the distrusted
# emSign ECC Root CA - G3.
ARG OS_PKG_EPOCH=2026-W44
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
# certifi, urllib3 and setuptools are on the same system interpreter and lag the
# same way: certifi GHSA-6q48-6c7w-9gw2, urllib3 GHSA-mf9v-mfxr-j63j, and
# setuptools' vendored jaraco.context and wheel (84.2.1 vendors 6.1.0 / 0.46.3;
# GHSA-58pv-8j8x-9vj2, GHSA-8rrh-rw8j-w5fx). aws-cli 2.32 on Alpine 3.23 runs on
# urllib3 2.x, so the <3 ceiling is the only bound it needs.
RUN pip install --no-cache-dir --break-system-packages --upgrade \
    "cryptography>=48.0.1" "certifi>=2026.10.18" "urllib3>=2.7.0,<3" "setuptools>=84.2.1"

# urllib3 2.8.0 warns on the `strict` argument Alpine's botocore still passes, once
# per aws call. Silence that one warning so it stays out of CLI results.
ENV PYTHONWARNINGS=ignore::FutureWarning:urllib3.poolmanager

# Azure CLI. Install azure-cli and the patched floors in one resolver pass so
# pip satisfies all constraints simultaneously (azure-cli-core declares a bare
# `cryptography` requirement with no ceiling, so >=50 resolves cleanly). This
# clears the cryptography, PyJWT, urllib3, oauthlib and certifi HIGHs that
# cloud-collector-server inherits from this venv. Single-pass is deliberate: if
# a future azure-cli ever pins below a floor, this fails loudly at build
# instead of silently shipping an unsupported combo.
RUN python3 -m venv /opt/azure-cli-venv && \
    /opt/azure-cli-venv/bin/pip install --no-cache-dir --upgrade pip && \
    /opt/azure-cli-venv/bin/pip install --no-cache-dir azure-cli "cryptography>=50.0.0" \
      "PyJWT>=2.14.0" "urllib3>=2.8.0" "oauthlib>=4.0.0" "certifi>=2026.10.18" && \
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

# The compilers above only exist to build wheels. Drop them so they don't ship in
# the runtime image, where binutils alone carries scanner findings.
RUN apk del gcc musl-dev python3-dev libffi-dev openssl-dev cargo make

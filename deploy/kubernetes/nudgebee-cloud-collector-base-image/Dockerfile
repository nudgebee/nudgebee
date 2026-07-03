FROM alpine:3.22

# Refresh OS packages to the latest patch level (alpine:3.19 was EOL — no more
# security updates; 3.22 is current). apk upgrade keeps subsequent rebuilds fresh.
RUN apk upgrade --no-cache

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

# Google Cloud SDK
RUN apk add --no-cache --virtual .gcloud-build-deps git && \
    curl -o install.sh https://sdk.cloud.google.com && \
    bash install.sh --disable-prompts --install-dir=/usr/local && \
    rm install.sh && \
    apk del .gcloud-build-deps
ENV PATH="/usr/local/google-cloud-sdk/bin:$PATH"

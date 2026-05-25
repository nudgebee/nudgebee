FROM alpine:3.19

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

# Azure CLI
RUN python3 -m venv /opt/azure-cli-venv && \
    /opt/azure-cli-venv/bin/pip install --no-cache-dir azure-cli && \
    ln -s /opt/azure-cli-venv/bin/az /usr/local/bin/az && \
    az extension add --name costmanagement

# Google Cloud SDK
RUN apk add --no-cache --virtual .gcloud-build-deps git && \
    curl -o install.sh https://sdk.cloud.google.com && \
    bash install.sh --disable-prompts --install-dir=/usr/local && \
    rm install.sh && \
    apk del .gcloud-build-deps
ENV PATH="/usr/local/google-cloud-sdk/bin:$PATH"

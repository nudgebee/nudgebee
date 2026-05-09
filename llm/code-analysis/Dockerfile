FROM golang:1.26-alpine AS builder

WORKDIR /app

# Install git and other dependencies needed for build and runtime
RUN apk add --no-cache git make ca-certificates openssh-client

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o code-analysis-agent ./cmd/main.go
# Build the shim
RUN CGO_ENABLED=0 GOOS=linux go build -o shim ./cmd/shim/main.go

# Install golangci-lint in builder where we have the right Go version
RUN CGO_ENABLED=0 GOOS=linux GOBIN=/usr/local/bin \
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.9.0

# Final stage
FROM alpine:3.19    

WORKDIR /app

# Install runtime dependencies
# git, curl, openssh-client, bind-tools (dig), iputils (ping), net-tools (netstat), nmap-ncat (nc)
# jq, grep, findutils, coreutils, sed, awk, tar, unzip, procps
# ripgrep (rg), the_silver_searcher (ag)
RUN apk add --no-cache \
    git \
    curl \
    openssh-client \
    bind-tools \
    iputils \
    net-tools \
    nmap-ncat \
    jq \
    grep \
    findutils \
    coreutils \
    sed \
    gawk \
    tar \
    unzip \
    procps \
    ripgrep \
    the_silver_searcher \
    ca-certificates \
    github-cli \
    aws-cli \
    python3 \
    py3-pip \
    bash

# Install Google Cloud SDK
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then \
        curl -O https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-linux-x86_64.tar.gz && \
        tar -xf google-cloud-cli-linux-x86_64.tar.gz && \
        rm google-cloud-cli-linux-x86_64.tar.gz; \
    elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then \
        curl -O https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-linux-arm.tar.gz && \
        tar -xf google-cloud-cli-linux-arm.tar.gz && \
        rm google-cloud-cli-linux-arm.tar.gz; \
    fi && \
    ./google-cloud-sdk/install.sh --quiet --usage-reporting false --path-update false --command-completion false
ENV PATH=/home/appuser/.local/bin:/home/appuser/bin:/app/google-cloud-sdk/bin:$PATH

# Install Azure CLI in a venv to isolate its dependencies (e.g. urllib3 2.x)
# from system Python packages used by apk-installed aws-cli
RUN apk add --no-cache --virtual .build-deps \
    gcc \
    musl-dev \
    python3-dev \
    libffi-dev \
    openssl-dev \
    cargo \
    make \
    linux-headers && \
    python3 -m venv /opt/azure-cli-venv && \
    /opt/azure-cli-venv/bin/pip install --no-cache-dir azure-cli && \
    ln -s /opt/azure-cli-venv/bin/az /usr/local/bin/az && \
    apk del .build-deps

# Pre-install Azure CLI extensions to a fixed directory.
# AZURE_EXTENSION_DIR decouples extension storage from AZURE_CONFIG_DIR,
# ensuring extensions remain available when AZURE_CONFIG_DIR is overridden
# per-session for auth isolation. Without this, dynamic extension installs
# (e.g. log-analytics) trigger a CLI re-execution that loses login state.
ENV AZURE_EXTENSION_DIR=/opt/azure-cli-extensions
RUN mkdir -p /opt/azure-cli-extensions && \
    az extension add --name log-analytics && \
    az extension add --name costmanagement

# Install build & development tools for code verification
# These enable the agent to run build/lint/test commands on cloned repositories.
# Go is copied from the builder stage instead of `apk add go` because Alpine
# 3.19 ships Go 1.21, which fails on target repos whose go.mod requires a
# newer toolchain (e.g. nudgebee/api-server/services requires >= 1.26.1).
RUN apk add --no-cache \
    make \
    nodejs \
    npm

# Install common linters and package managers
# Python tools are installed in a venv to avoid polluting system site-packages
# (e.g. poetry pulls in urllib3 2.x which breaks apk-installed aws-cli)
RUN npm install -g yarn pnpm && \
    python3 -m venv /opt/python-tools && \
    /opt/python-tools/bin/pip install --no-cache-dir poetry black flake8 mypy && \
    ln -s /opt/python-tools/bin/poetry /usr/local/bin/poetry && \
    ln -s /opt/python-tools/bin/black /usr/local/bin/black && \
    ln -s /opt/python-tools/bin/flake8 /usr/local/bin/flake8 && \
    ln -s /opt/python-tools/bin/mypy /usr/local/bin/mypy

# Copy Go toolchain and golangci-lint from builder.
# Builder uses golang:1.26-alpine; matching that here keeps build/vet runnable
# against modules that pin go >= 1.26.
COPY --from=builder /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:$PATH
COPY --from=builder /usr/local/bin/golangci-lint /usr/local/bin/golangci-lint

# Create a non-root user
RUN adduser -D -g '' appuser

# Create workspace directory and set ownership
RUN mkdir -p /tmp/code-analysis && \
    mkdir -p /app/workspaces && \
    chown -R appuser:appuser /tmp/code-analysis /app /opt/azure-cli-extensions

# Copy binary from builder
COPY --from=builder /app/code-analysis-agent .
COPY --from=builder /app/shim /usr/local/bin/shim
RUN chown appuser:appuser ./code-analysis-agent /usr/local/bin/shim

# Create symlinks for shims
RUN ln -s /usr/local/bin/shim /usr/local/bin/kubectl && \
    ln -s /usr/local/bin/shim /usr/local/bin/helm && \
    ln -s /usr/local/bin/shim /usr/local/bin/psql && \
    ln -s /usr/local/bin/shim /usr/local/bin/mysql && \
    ln -s /usr/local/bin/shim /usr/local/bin/redis-cli && \
    ln -s /usr/local/bin/shim /usr/local/bin/argocd && \
    ln -s /usr/local/bin/shim /usr/local/bin/clickhouse-client && \
    ln -s /usr/local/bin/shim /usr/local/bin/rabbitmqadmin && \
    ln -s /usr/local/bin/shim /usr/local/bin/sqlcmd && \
    ln -s /usr/local/bin/shim /usr/local/bin/sqlplus && \
    ln -s /usr/local/bin/shim /usr/local/bin/ssh

# Switch to non-root user
USER appuser

# Expose port
EXPOSE 8080

# Run the application
CMD ["/app/code-analysis-agent"]

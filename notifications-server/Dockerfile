# syntax=docker/dockerfile:1.7
# Base image. OSS installs use the public GHCR mirror of our internal
# Python base (Alpine + uv + native build deps preinstalled). EE deploys
# override PYTHON_BASE to their internal registry via --build-arg.
# Rolling :3.12 tag (rebuilt weekly on patched Alpine + latest uv/pip), matching
# the k8s-collector base pin. The old dated tag :3.12-20250919-140344 shipped an
# unpatched Alpine + uv 0.8.18 / pip 25.0.1 (5 fixable pip/uv CVEs); :3.12 scans
# clean. EE deploys still override PYTHON_BASE via --build-arg.
ARG PYTHON_BASE=ghcr.io/nudgebee/nudgebee-python:3.12
FROM ${PYTHON_BASE} AS python-base
RUN apk update && \
    apk upgrade --no-cache && \
    rm -rf /var/cache/apk/*
# Set the working directory
WORKDIR /usr/src/app

# Copy only dependency files first for caching
COPY poetry.lock pyproject.toml ./

# Install dependencies (excluding dev dependencies)
RUN --mount=type=cache,target=/root/.cache/uv \
    --mount=type=cache,target=/root/.cache/pip \
    uv pip install --system --requirements pyproject.toml

# Copy application source code
COPY notifications_server notifications_server

# Set service name for observability
ENV OTEL_SERVICE_NAME=notifications_server

# Command to run the application
CMD ["uvicorn", "notifications_server.server:app", "--host", "0.0.0.0", "--port", "8080", "--proxy-headers"]

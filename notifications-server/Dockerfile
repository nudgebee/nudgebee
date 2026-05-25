# syntax=docker/dockerfile:1.7
# Base image. OSS installs use the public GHCR mirror of our internal
# Python base (Alpine + uv + native build deps preinstalled). EE deploys
# override PYTHON_BASE to their internal registry via --build-arg.
ARG PYTHON_BASE=ghcr.io/nudgebee/nudgebee-python:3.12-20250919-140344
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
COPY notifications_server/*.py notifications_server/
COPY notifications_server/branding notifications_server/branding
COPY notifications_server/clients notifications_server/clients
COPY notifications_server/configs notifications_server/configs
COPY notifications_server/emailer notifications_server/emailer
COPY notifications_server/exceptions notifications_server/exceptions
COPY notifications_server/message_templates notifications_server/message_templates
COPY notifications_server/models notifications_server/models
COPY notifications_server/processors notifications_server/processors
COPY notifications_server/rabbitmq notifications_server/rabbitmq
COPY notifications_server/repositories notifications_server/repositories
COPY notifications_server/routers notifications_server/routers
COPY notifications_server/schemas notifications_server/schemas
COPY notifications_server/services notifications_server/services
COPY notifications_server/utils notifications_server/utils

# Set service name for observability
ENV OTEL_SERVICE_NAME=notifications_server

# Command to run the application
CMD ["uvicorn", "notifications_server.server:app", "--host", "0.0.0.0", "--port", "8080", "--proxy-headers"]

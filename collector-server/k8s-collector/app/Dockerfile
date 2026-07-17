# syntax=docker/dockerfile:1.7
# Base image. OSS installs use the public GHCR mirror of our internal
# Python base (Alpine + uv + native build deps preinstalled). EE deploys
# override PYTHON_BASE to their internal registry via --build-arg.
ARG PYTHON_BASE=ghcr.io/nudgebee/nudgebee-python:3.12
FROM ${PYTHON_BASE} AS python-base

# Stage 1: builder-base to install dependencies
FROM python-base AS builder-base

# Add only additional packages if needed (optional)
RUN apk add --no-cache libsodium-dev cython libexpat pgbadger

# Set up build directory
WORKDIR /opt/pysetup
COPY poetry.lock pyproject.toml ./

# Install dependencies in a virtual environment.
# Create the venv with --without-pip: uv (not pip) resolves and installs our
# deps and does not need pip in the target venv, so seeding pip/setuptools/wheel
# only adds vulnerable binaries that trivy flags (pip 25.0.1 CVEs) plus image
# weight. Omitting them eliminates the pip CVEs at the root.
RUN --mount=type=cache,target=/root/.cache/uv \
    python -m venv --without-pip /opt/pysetup/.venv && \
    uv pip install --requirements pyproject.toml

# Stage 2: production
FROM python-base AS production

# The pinned nudgebee-python base is rebuilt infrequently, so its Alpine OS
# packages drift behind security patches (openssl/libcrypto3/libssl3, musl,
# zlib, nghttp2 CVEs). Refresh them at build time. The proper root-cause fix is
# a base-image rebuild + tag bump (publish workflow lives in nudgebee-infra).
RUN apk upgrade --no-cache

WORKDIR /app
ENV PYTHONPATH=/app

# Copy virtual environment from builder
COPY --from=builder-base /opt/pysetup/.venv /opt/pysetup/.venv

# Copy application code
COPY . .

EXPOSE 5000

# Launch app using gunicorn
CMD ["gunicorn", "--workers=2", "--bind=0.0.0.0:5000", "--timeout=300", "app:app"]

FROM python:3.12-alpine AS python-base
LABEL maintainer="nudgebee"
LABEL version="3.12-alpine"

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_NO_CACHE_DIR=off \
    PIP_DISABLE_PIP_VERSION_CHECK=on \
    PIP_DEFAULT_TIMEOUT=100 \
    PYSETUP_PATH="/opt/pysetup" \
    VENV_PATH="/opt/pysetup/.venv" \
    PYTHONPATH=/usr/src/app

ENV PATH="$VENV_PATH/bin:$PATH"

FROM python-base AS builder-base

# Upgrade sqlite-libs and install existing dependencies.
# OS-package cache-bust: bumping OS_PKG_EPOCH (ISO week) changes this layer's
# cache key so BuildKit re-runs the apk install and pulls current Alpine security
# patches — its registry cache (mode=max) otherwise serves a stale package layer
# forever. Committed (not a build-arg) so each refresh is a reviewable, revertable
# diff; bump when the Trivy scan flags a stale package (e.g. c-ares CVE-2026-33630).
ARG OS_PKG_EPOCH=2026-W28
RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apk update && apk upgrade --no-cache && apk add --no-cache openssl && apk add --no-cache sqlite-libs \
    bash \
    curl \
    musl-dev \
    gcc \
    libc-dev \
    build-base \
    libffi-dev \
    ca-certificates \
    libstdc++

# Upgrade pip alongside installing uv — the python:3.12-alpine tag lags pip
# security releases (e.g. pip 25.0.1 carries fixable MEDIUM CVEs that surface in
# every consumer image). --upgrade keeps subsequent rebuilds on the latest pip.
RUN pip install --no-cache-dir --upgrade pip uv

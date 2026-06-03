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

# Upgrade sqlite-libs and install existing dependencies
RUN apk update && apk upgrade --no-cache && apk add --no-cache openssl && apk add --no-cache sqlite-libs \
    bash \
    curl \
    musl-dev \
    gcc \
    libc-dev \
    build-base \
    libffi-dev \
    ca-certificates \
    libstdc++

RUN pip install uv

# syntax=docker/dockerfile:1.7
# Use the custom base image that has all base dependencies preinstalled
FROM registry.nudgebee.com/nudgebee-python:3.12-20250919-140344 AS python-base

# Stage 1: builder-base to install dependencies
FROM python-base AS builder-base

# Add only additional packages if needed (optional)
RUN apk add --no-cache libsodium-dev cython libexpat pgbadger

# Set up build directory
WORKDIR /opt/pysetup
COPY poetry.lock pyproject.toml ./

# Install dependencies in a virtual environment
RUN --mount=type=cache,target=/root/.cache/uv \
    --mount=type=cache,target=/root/.cache/pip \
    python -m venv /opt/pysetup/.venv && \
    uv pip install --requirements pyproject.toml

# Stage 2: production
FROM python-base AS production

WORKDIR /app
ENV PYTHONPATH=/app

# Copy virtual environment from builder
COPY --from=builder-base /opt/pysetup/.venv /opt/pysetup/.venv

# Copy application code
COPY . .

EXPOSE 5000

# Launch app using gunicorn
CMD ["gunicorn", "--workers=2", "--bind=0.0.0.0:5000", "--timeout=300", "app:app"]

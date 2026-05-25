# syntax=docker/dockerfile:1.7
FROM debian:trixie-slim AS python-base
LABEL org.nudgebee.image.authors="dev@nudgebee.com"

ARG TARGETARCH
ENV MINICONDA_VER=latest
ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV PATH=/opt/conda/bin:$PATH

# Install necessary packages
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl && \
    rm -rf /var/lib/apt/lists/* && \
    if [ "$TARGETARCH" = "arm64" ]; then \
        curl -fsSL https://github.com/conda-forge/miniforge/releases/download/24.3.0-0/Miniforge3-Linux-aarch64.sh -o miniforge.sh; \
    else \
        curl -fsSL https://github.com/conda-forge/miniforge/releases/download/24.3.0-0/Miniforge3-Linux-x86_64.sh -o miniforge.sh; \
    fi && \
    bash miniforge.sh -b -p /opt/conda && \
    rm miniforge.sh && \
    conda config --set channel_priority strict && \
    conda clean --yes --all

# Install kubectl
RUN curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/${TARGETARCH}/kubectl" && \
    chmod +x kubectl && \
    mv kubectl /usr/local/bin/

# ----- Stage: uv-base (installs python3 + uv) -----
FROM python-base AS uv-base

# Create a Conda environment
ENV PATH=/opt/conda/bin:$PATH
RUN conda update -n base -c defaults conda && \
    conda create -n myenv python=3.13 -y && \
    conda install -n myenv -c conda-forge pulp -y && \
    conda clean --yes --all

ENV PATH=/opt/conda/envs/myenv/bin:$PATH
WORKDIR /app

# Install uv (single fast binary)
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install uv

COPY uv.lock pyproject.toml ./

# Install dependencies
RUN --mount=type=cache,target=/root/.cache/uv \
    --mount=type=cache,target=/root/.cache/pip \
    uv pip install -e . --system


# `production` image used for runtime
FROM uv-base AS production

# Copy conda environment from builder
COPY --from=uv-base /opt/conda /opt/conda
ENV PATH=/opt/conda/envs/myenv/bin:$PATH

WORKDIR /app
ENV PYTHONPATH /app
COPY benchmark_server /app/benchmark_server
COPY llm /app/llm
COPY pyproject.toml /app/
COPY app.py /app/

# Set environment variables
ENV LOGGING_CONFIG_FILE=/app/benchmark_server/config/logging.json
ENV BENCHMARK_PORT=9999
# UVICORN_WORKERS=1 is intentional — the in-process magic-link token store
# in benchmark_server/controllers/auth_controller.py (_magic_tokens dict) is
# per-process. With >1 worker, the POST /auth/email-login that issues a
# token and the GET /api/auth/callback/email that consumes it can land on
# different workers, making ~50% of magic-link clicks fail with no useful
# diagnostic. Raise to >1 only AFTER moving tokens to the DB (mirroring
# NextAuth's user_auths magic-link rows).
ENV UVICORN_WORKERS=1
# --proxy-headers + --forwarded-allow-ips so request.client.host reflects
# the real client (used by future per-IP rate-limiting / audit logging),
# and request.url.scheme reflects the X-Forwarded-Proto from the
# TLS-terminating ingress.
ENV UVICORN_PROXY_FLAGS="--proxy-headers --forwarded-allow-ips=*"
EXPOSE ${BENCHMARK_PORT}

# Dashboard is served by FastAPI at / (no separate Streamlit process needed)
CMD ["sh", "-c", "\
    conda run --no-capture-output -n myenv uvicorn --workers ${UVICORN_WORKERS} \
        ${UVICORN_PROXY_FLAGS} \
        --host 0.0.0.0 --port ${BENCHMARK_PORT} app:app --log-config ${LOGGING_CONFIG_FILE}"]

FROM debian:trixie-slim AS python-base
LABEL org.nudgebee.image.authors="hemasundar.nallapaneni@nudgebee.com"

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
RUN pip install --no-cache-dir uv

COPY uv.lock pyproject.toml ./

# Install dependencies
RUN uv pip install -e . --system


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
ENV UVICORN_WORKERS=2
EXPOSE ${BENCHMARK_PORT}

# Dashboard is served by FastAPI at / (no separate Streamlit process needed)
CMD ["sh", "-c", "\
    conda run --no-capture-output -n myenv uvicorn --workers ${UVICORN_WORKERS} \
        --host 0.0.0.0 --port ${BENCHMARK_PORT} app:app --log-config ${LOGGING_CONFIG_FILE}"]

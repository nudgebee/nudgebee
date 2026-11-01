# syntax=docker/dockerfile:1.7
# Base image. OSS installs use the public GHCR mirror of our internal
# ML base (conda + conda `myenv` Python 3.11 + ML deps preinstalled). EE deploys
# override PYTHON_BASE to their internal registry via --build-arg.
# Pinned to the :wolfi tag (the Wolfi-based, continuously-patched, build-
# essential-free conda base that ml-k8s-server also uses). The Wolfi migration
# cleared the Debian-essential perl-base CRITICALs + ~121 NOFIX Debian OS CVEs
# the old debian:trixie-slim base carried. rag-server's requires-python
# (>=3.10,<=3.14) is satisfied by the base's Python 3.11.8, and its heavy deps
# (sentence-transformers -> torch) install from prebuilt wheels, so the
# toolchain-free base is sufficient.
ARG PYTHON_BASE=ghcr.io/nudgebee/nudgebee-ml-base:wolfi
FROM ${PYTHON_BASE} AS uv-base
LABEL org.nudgebee.image.authors="dev@nudgebee.com"

ARG TARGETARCH
ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1

WORKDIR /app

COPY uv.lock pyproject.toml ./

# Install dependencies from uv.lock into the conda env via `uv sync`.
# Cache mounts persist /root/.cache/{uv,pip} across builds via
# BuildKit; the previous explicit `rm -rf` of those dirs is removed
# because the cache mount itself keeps them out of the image layer.
# UV_PROJECT_ENVIRONMENT points uv at the existing conda env instead of
# creating its own .venv; --frozen trusts the committed uv.lock as-is
# (no re-resolve); --no-install-project skips building/installing the
# local rag-server package itself, matching the deps-only install this
# replaced. --inexact keeps uv from removing the base image's preinstalled
# conda/ML packages that aren't tracked in uv.lock.
# Drop pip from the conda envs. Dependencies are installed with uv and
# nothing calls pip at runtime, but pip vendors its own msgpack, urllib3 and
# setuptools copies that Trivy flags (still unpatched in pip 26.2.2). Its
# extracted copies in the conda package cache carry the same manifest.
RUN --mount=type=cache,target=/root/.cache/uv \
    --mount=type=cache,target=/root/.cache/pip \
    set -e; \
    (conda run -n myenv pip uninstall -y importlib-metadata || true); \
    export UV_PROJECT_ENVIRONMENT=/opt/conda/envs/myenv; \
    success=0; \
    arch=$(uname -m); \
    for i in 1 2 3; do \
        echo "Attempt $i [arch=$arch]: Installing dependencies..."; \
        if conda run -n myenv uv sync --frozen --inexact --no-install-project; then \
            success=1; break; \
        else \
            echo "Attempt $i [arch=$arch] failed, retrying in $((i*5)) seconds..."; \
            sleep $((i*5)); \
        fi; \
    done; \
    [ "$success" -eq 1 ] || exit 1; \
    for env in myenv base; do \
        if conda list -n "$env" --json | grep -q '"name": "pip"'; then conda remove -y --force -n "$env" pip || exit 1; fi; \
    done && \
    rm -rf /opt/conda/pkgs/pip-* && \
    conda clean --yes --all

# `production` image used for runtime
FROM uv-base AS production

# Copy conda environment from builder
COPY --from=uv-base /opt/conda /opt/conda
ENV PATH=/opt/conda/envs/myenv/bin:$PATH

WORKDIR /app
ENV PYTHONPATH=/app
COPY . /app

# Uvicorn configuration (single worker for Qdrant persistent mode)
ENV UVICORN_WORKERS=1
ENV RAGSERVER_PORT=9999
EXPOSE 9999

# Copy the entrypoint script and make it executable
COPY entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

# Set the entrypoint script to be executed when the container starts
ENTRYPOINT ["/app/entrypoint.sh"]

# The default command is to start the web server
CMD ["web"]


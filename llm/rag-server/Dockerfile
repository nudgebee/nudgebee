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

# Install dependencies using uv (with poetry.lock compatibility).
# Cache mounts persist /root/.cache/{uv,pip} across builds via
# BuildKit; the previous explicit `rm -rf` of those dirs is removed
# because the cache mount itself keeps them out of the image layer.
RUN --mount=type=cache,target=/root/.cache/uv \
    --mount=type=cache,target=/root/.cache/pip \
    set -e; \
    (conda run -n myenv pip uninstall -y importlib-metadata || true); \
    success=0; \
    arch=$(uname -m); \
    for i in 1 2 3; do \
        echo "Attempt $i [arch=$arch]: Installing dependencies..."; \
        if conda run -n myenv uv pip install --system --requirements uv.lock; then \
            success=1; break; \
        else \
            echo "Attempt $i [arch=$arch] failed, retrying in $((i*5)) seconds..."; \
            sleep $((i*5)); \
        fi; \
    done; \
    [ "$success" -eq 1 ] || exit 1; \
    conda clean --yes --all

# Replace the CUDA build of torch with the CPU one.
#
# The ML base image ships torch built against CUDA, which drags in ~3.3GB of
# nvidia/* and triton wheels. rag-server does inference on the CPU and is never
# scheduled onto a GPU node, so none of that code can execute -- it is dead
# weight in every layer, every pull and every pod's RSS.
#
# Measured on the dev image (linux/amd64): site-packages 5.5GB -> 1.8GB, pod
# resident memory 896MB -> 527MB, with inference speed unchanged (0.448 -> 0.483
# CPU-seconds per search, inside run-to-run variance). The pinned version is read
# back from the installed build so this keeps whatever version the base image
# settled on.
#
# arm64 is a no-op: there is no CUDA build of torch for linux/aarch64, so the
# base image already ships a CPU one and there is nothing to strip. The case
# below checks the actual build rather than the platform, so it stays correct if
# the base image changes.
RUN --mount=type=cache,target=/root/.cache/pip \
    set -e; \
    TORCH_BUILD=$(conda run -n myenv python -c "import torch; print(torch.__version__)"); \
    case "$TORCH_BUILD" in \
      *+cu*) \
        TORCH_VERSION=${TORCH_BUILD%%+*}; \
        echo "Replacing torch ${TORCH_BUILD} with the CPU build"; \
        conda run -n myenv python -m pip uninstall -y triton >/dev/null 2>&1 || true; \
        conda run -n myenv python -m pip freeze | sed -n 's/^\(nvidia-[^=]*\)==.*/\1/p' \
            | xargs -r conda run -n myenv python -m pip uninstall -y >/dev/null 2>&1 || true; \
        conda run -n myenv python -m pip install --force-reinstall --no-cache-dir \
            "torch==${TORCH_VERSION}" --index-url https://download.pytorch.org/whl/cpu; \
        ;; \
      *) \
        echo "torch ${TORCH_BUILD} is already a CPU build (the arm64 case) - nothing to strip"; \
        ;; \
    esac; \
    conda run -n myenv python -c "import torch; assert not torch.cuda.is_available(), 'expected a CPU-only torch build'; print('torch', torch.__version__)"; \
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


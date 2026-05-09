FROM registry.nudgebee.com/nudgebee-ml-base:3.12-20250926-063300 AS uv-base
LABEL org.nudgebee.image.authors="hemasundar.nallapaneni@nudgebee.com"

ARG TARGETARCH
ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1

WORKDIR /app

COPY uv.lock pyproject.toml ./

# Install dependencies using uv (with poetry.lock compatibility)
RUN set -e; \
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
    conda clean --yes --all; \
    rm -rf /root/.cache/pip /root/.cache/uv


# `production` image used for runtime
FROM uv-base AS production

# Copy conda environment from builder
COPY --from=uv-base /opt/conda /opt/conda
ENV PATH=/opt/conda/envs/myenv/bin:$PATH

WORKDIR /app
ENV PYTHONPATH /app
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


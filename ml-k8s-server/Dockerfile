FROM registry.nudgebee.com/nudgebee-ml-base:20250627-141845 AS uv-base
LABEL org.nudgebee.image.authors="dev@nudgebee.com"

ARG TARGETARCH
ENV MINICONDA_VER=latest
ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1

WORKDIR /app

COPY poetry.lock pyproject.toml ./

# Install dependencies
RUN conda run -n myenv pip uninstall -y importlib-metadata || true && \
    conda run -n myenv uv pip install --system --requirements pyproject.toml && \
    conda clean --yes --all && \
    rm -rf /root/.cache/pip /root/.cache/uv

FROM uv-base AS production

# Copy conda environment from builder
COPY --from=uv-base /opt/conda /opt/conda
ENV PATH=/opt/conda/envs/myenv/bin:$PATH

WORKDIR /app

# Copy only the application code
COPY gunicorn.conf.py ./
COPY server ./server

# Runtime configuration
ENV GUNICORN_WORKERS=2
ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1

EXPOSE 9999
EXPOSE 8081

# Use sh -c to allow environment variable expansion in exec form
CMD ["sh", "-c", "conda run --no-capture-output -n myenv gunicorn --config gunicorn.conf.py --workers ${GUNICORN_WORKERS} --timeout=900 --bind=0.0.0.0:9999 server.app:app & conda run --no-capture-output -n myenv gunicorn --workers 1 --timeout=60 --bind=0.0.0.0:8081 server.app:health_app & wait"]

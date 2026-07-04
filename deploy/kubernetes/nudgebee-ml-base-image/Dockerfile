FROM debian:trixie-slim AS python-base
LABEL org.nudgebee.image.authors="dev@nudgebee.com"
LABEL maintainer="nudgebee"

ARG TARGETARCH
ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV CONDA_DIR="/opt/conda"
ENV PATH="${CONDA_DIR}/bin:${CONDA_DIR}/envs/myenv/bin:$PATH"

# Install system dependencies, Miniforge, and setup environment.
# `apt-get upgrade` patches the base Debian packages to the latest trixie
# security releases — without it the image ships whatever debian:trixie-slim
# had when the tag was cut (which had drifted far behind: openssl, libgnutls,
# libpcre2, ... all with fixes available).
# DEBIAN_FRONTEND=noninteractive keeps apt from opening debconf prompts (which
# `upgrade` can trigger for reconfigured packages) and hanging the non-TTY CI
# build. Scoped to this RUN via `export` so it does not persist into the image.
# Drop build-essential: ml-k8s-server (the only consumer) installs prebuilt
# manylinux wheels (tensorflow, numpy, pandas, scikit-learn, psycopg2-binary,
# pydantic, ...) — nothing compiles from source, so a C/C++ toolchain is dead
# weight at build time and pure attack surface at runtime. It pulled in perl,
# gcc, make and dev headers, which accounted for ~7 CRITICAL + ~50 HIGH + ~270
# MEDIUM (mostly NOFIX Debian) on ml-k8s-server. Replace it with just the two
# runtime shared libs the ML wheels dlopen: libgomp1 (OpenMP, used by
# numpy/tensorflow) and libstdc++6. Verified: ml-k8s-server still builds and
# `import tensorflow, numpy, pandas, sklearn` all succeed.
RUN export DEBIAN_FRONTEND=noninteractive && \
    apt-get update && apt-get upgrade -y && apt-get install -y --no-install-recommends \
    bash libgomp1 libstdc++6 bzip2 ca-certificates curl && \
    rm -rf /var/lib/apt/lists/* && \
    # Install Miniforge
    if [ "$TARGETARCH" = "arm64" ]; then \
        curl -fsSL "https://github.com/conda-forge/miniforge/releases/latest/download/Miniforge3-Linux-aarch64.sh" -o miniforge.sh; \
    else \
        curl -fsSL "https://github.com/conda-forge/miniforge/releases/latest/download/Miniforge3-Linux-x86_64.sh" -o miniforge.sh; \
    fi && \
    bash miniforge.sh -b -p "${CONDA_DIR}" && \
    rm miniforge.sh && \
    # Setup environment and install packages
    "${CONDA_DIR}/bin/conda" update -n base -c conda-forge conda -y && \
    "${CONDA_DIR}/bin/conda" create -n myenv python=3.11.8 -c conda-forge -y && \
    "${CONDA_DIR}/bin/conda" install -n myenv -c conda-forge pulp -y && \
    "${CONDA_DIR}/bin/conda" run -n myenv pip install uv && \
    "${CONDA_DIR}/bin/conda" run -n myenv pip uninstall -y importlib-metadata || true && \
    # Patch the conda base env's msgpack (tooling dep, python 3.13) off the
    # vulnerable 1.1.2 that trivy flags HIGH. The app runs from `myenv`, but the
    # base env still ships in the image, so bump it to the fixed release.
    "${CONDA_DIR}/bin/conda" install -n base -c conda-forge "msgpack-python>=1.2.1" -y && \
    "${CONDA_DIR}/bin/conda" clean --yes --all && \
    rm -rf /root/.cache

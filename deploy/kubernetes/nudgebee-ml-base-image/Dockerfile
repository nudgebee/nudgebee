# Wolfi base: glibc-based (so conda + manylinux ML wheels work) and continuously
# patched, unlike debian:trixie-slim whose snapshot accrues NOFIX OS CVEs. The
# Debian base shipped perl-base as an essential package (dpkg depends on it) —
# an unavoidable CRITICAL that only a base migration clears. Verified in a spike:
# on Wolfi, conda + `import tensorflow, numpy, pandas, sklearn` (ml-k8s-server)
# and `import torch, sentence_transformers` (rag-server) all succeed, and the
# base scans clean (0 CVEs after the msgpack patch below).
#
# Deliberately tracks :latest (Wolfi's free tier has no version-rolling/dated
# tags). This base's whole purpose is to be fresh: ml-base-image.yaml rebuilds
# it weekly + on push, so :latest keeps it continuously patched — pinning a
# digest would refreeze it into the staleness this migration is fixing.
# Reproducibility for consumers comes from the immutable OUTPUT tags this
# workflow publishes (:<YYYYMMDD>-<sha7>), which they can pin; the CI Trivy gate
# catches any regression a bad upstream push might introduce.
FROM cgr.dev/chainguard/wolfi-base AS python-base
LABEL org.nudgebee.image.authors="dev@nudgebee.com"
LABEL maintainer="nudgebee"

# Buildx sets TARGETARCH per platform (amd64 / arm64). Both consumers
# (ml-k8s-server, rag-server) and their ML wheels (tensorflow, torch,
# onnxruntime) publish aarch64 builds, and Wolfi supports arm64, so this base
# builds multi-arch.
ARG TARGETARCH

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV CONDA_DIR="/opt/conda"
ENV PATH="${CONDA_DIR}/bin:${CONDA_DIR}/envs/myenv/bin:$PATH"

# Runtime shared libs the prebuilt ML wheels dlopen (libgomp = OpenMP for
# numpy/tensorflow/torch, libstdc++), plus the tools miniforge needs. No
# build toolchain: every consumer installs prebuilt manylinux wheels, nothing
# compiles from source (dropping build-essential/perl/gcc is what took the old
# Debian base's ~7 CRITICAL + ~50 HIGH + ~270 MEDIUM off ml-k8s-server).
# posix-libc-utils provides `ldd` — Wolfi ships glibc but not the ldd wrapper,
# which the miniforge installer greps for its GLIBC-version pre-check (arch-
# independent, so no per-arch dynamic-linker shim needed).
# libmagic: rag-server's `unstructured` uses python-magic for file-type
# detection, which ctypes-dlopens libmagic.so by name at runtime (invisible to
# ldd). Wolfi doesn't ship it by default; the package includes the magic.mgc
# database too. (debian:trixie-slim didn't ship it either, so unstructured was
# on a weaker fallback there — this is strictly better.)
RUN apk add --no-cache bash libgomp libstdc++ bzip2 ca-certificates curl posix-libc-utils libmagic

RUN if [ "$TARGETARCH" = "arm64" ]; then MF_ARCH=aarch64; else MF_ARCH=x86_64; fi && \
    curl -fsSL "https://github.com/conda-forge/miniforge/releases/latest/download/Miniforge3-Linux-${MF_ARCH}.sh" -o miniforge.sh && \
    bash miniforge.sh -b -p "${CONDA_DIR}" && \
    rm miniforge.sh && \
    # Setup environment and install packages
    "${CONDA_DIR}/bin/conda" update -n base -c conda-forge conda -y && \
    "${CONDA_DIR}/bin/conda" create -n myenv python=3.11.8 -c conda-forge -y && \
    "${CONDA_DIR}/bin/conda" install -n myenv -c conda-forge pulp -y && \
    "${CONDA_DIR}/bin/conda" run -n myenv pip install uv && \
    "${CONDA_DIR}/bin/conda" run -n myenv pip uninstall -y importlib-metadata || true && \
    # Patch the conda base env's msgpack (tooling dep) off the vulnerable 1.1.2
    # that trivy flags HIGH. The app runs from `myenv`, but the base env still
    # ships in the image, so bump it to the fixed release.
    "${CONDA_DIR}/bin/conda" install -n base -c conda-forge "msgpack-python>=1.2.1" -y && \
    # conda-rattler-solver is an experimental, optional conda solver plugin
    # (conda's default remains libmamba, also installed). It vendors py-rattler,
    # whose rattler.abi3.so statically links pyo3 0.25.1 — trivy flags HIGH
    # (GHSA-36hh-v3qg-5jq4) + MEDIUM (GHSA-chgr-c6px-7xpp). It is base-env package
    # tooling never invoked at runtime (the app runs from myenv), so remove it
    # outright instead of chasing an upstream rattler release. Removal downgrades
    # conda three patches (26.5.3 -> 26.5.0, still libmamba-solvered) — a
    # deliberate trade, and it clears the pyo3 finding on both consumers
    # (ml-k8s-server, rag-server). Guarded per package: miniforge tracks
    # releases/latest, so upstream may drop one or both packages from the bundle;
    # `conda remove` PackagesNotFoundErrors on any absent name, which would break
    # every rebuild. Skip each absent package (the desired end-state) but still
    # hard-fail if one is present and its removal errors. Removing
    # conda-rattler-solver first also removes py-rattler (its dependency) — the
    # second iteration then skips it cleanly.
    for pkg in conda-rattler-solver py-rattler; do \
      if "${CONDA_DIR}/bin/conda" list -n base | grep -qE "^${pkg}[[:space:]]"; then \
        "${CONDA_DIR}/bin/conda" remove -n base "$pkg" -y; \
      else echo "$pkg not bundled; skipping removal"; fi; \
    done && \
    "${CONDA_DIR}/bin/conda" clean --yes --all && \
    rm -rf /root/.cache

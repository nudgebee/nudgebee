# Patched mirror of the official Qdrant v1.20.0 image.
#
# v1.19.2 still ships tinypool 1.1.1 (CVE-2026-104848, CVE-2026-104849, both
# critical); v1.20.0 scans with 0 critical. Same recipe as ../qdrant: an apt
# upgrade layer on the official image so the Debian base picks up current patches.
# The v1.19.2 mirror stays published for charts that still pin it.

ARG OS_PKG_EPOCH=2026-W44
FROM docker.io/qdrant/qdrant:v1.20.0

ARG OS_PKG_EPOCH

RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get upgrade -y --no-install-recommends \
    && rm -rf /var/lib/apt/lists/*

# Patched mirror of the official Qdrant image, the chart's vector store.
#
# v1.19.2's Debian 13 base ships libpcre2-8-0 10.46-1~deb13u2, and its HIGH
# (CVE-2026-103111) is the image's only fixable finding. Take current Debian
# patches on the same Qdrant version, so the storage format and rollback stay as
# they are; v1.20.0 also clears it, but as a Qdrant upgrade.

ARG OS_PKG_EPOCH=2026-W44
FROM docker.io/qdrant/qdrant:v1.19.2

ARG OS_PKG_EPOCH

RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get upgrade -y --no-install-recommends \
    && rm -rf /var/lib/apt/lists/*

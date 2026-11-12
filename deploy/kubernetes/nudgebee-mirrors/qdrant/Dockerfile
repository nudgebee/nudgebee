# Mirror of the official Qdrant image, the chart's vector store, on Red Hat UBI 9
# (Red Hat container certification). The Qdrant binary, config and web UI are taken
# unchanged from the upstream Debian image, so the Qdrant version, storage format and
# rollback stay as they are; only the OS layer underneath changes.
#
# The upstream binary needs glibc <= 2.34 (UBI 9 ships 2.34), libgcc_s, liblzma and
# libunwind. libgcc and xz-libs come from UBI packages; libunwind is not in the UBI
# repos (EPEL only), so its three small libs are carried over from the upstream image.

ARG QDRANT_VERSION=v1.19.2

FROM docker.io/qdrant/qdrant:${QDRANT_VERSION} AS upstream
# -L: copy the real files under their soname links; the multiarch dir differs per arch.
RUN mkdir /unwind && cp -L /lib/*-linux-gnu/libunwind*.so.* /unwind/

# --- rootfs: the only OS packages the runtime needs ---
# ubi-micro has no package manager, so the packages are installed into /rootfs here and
# copied over. Bumping OS_PKG_EPOCH changes this layer's cache key so a registry cache
# can't serve a stale package layer (same convention as the other UBI images).
# bash: upstream entrypoint.sh; libgcc/xz-libs: the binary's shared libs;
# ca-certificates/tzdata: TLS to S3 snapshot targets and time zones.
FROM registry.access.redhat.com/ubi9 AS rootfs-builder
ARG OS_PKG_EPOCH=2026-W44
RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
 && dnf install -y --installroot /rootfs --releasever 9 --setopt=install_weak_deps=false --nodocs \
      bash libgcc xz-libs ca-certificates tzdata \
 && dnf --installroot /rootfs clean all && rm -rf /rootfs/var/cache/*
# Data dirs: group 0 and group-writable, so an arbitrary OpenShift uid (always gid 0)
# can write to them; also what a fresh emptyDir/PVC is initialised from.
RUN mkdir -p /rootfs/qdrant/storage /rootfs/qdrant/snapshots \
 && chown -R 1001:0 /rootfs/qdrant && chmod -R g=u /rootfs/qdrant

FROM registry.access.redhat.com/ubi9/ubi-micro AS release-stage
ARG QDRANT_VERSION=v1.19.2
ARG IMAGE_RELEASE=1
LABEL name="nudgebee-qdrant" \
      maintainer="nudgebee" \
      vendor="nudgebee" \
      version="${QDRANT_VERSION}" \
      release="${IMAGE_RELEASE}" \
      summary="Qdrant vector database" \
      description="Qdrant vector database, the Nudgebee chart's vector store, rebuilt on Red Hat UBI 9."
COPY --from=rootfs-builder /rootfs/ /
COPY --from=upstream /unwind/ /usr/lib64/

# Red Hat certification (HasLicense): license texts under /licenses. Qdrant is
# Apache-2.0 (copy of its LICENSE at the mirrored tag); the repo LICENSE comes in through
# the `repo` named context:  docker buildx build --build-context repo=<repo root> .
COPY LICENSE.qdrant /licenses/LICENSE.qdrant
COPY --from=repo LICENSE /licenses/LICENSE

WORKDIR /qdrant
# Root-owned and world-readable/executable: the runtime uid can run but not overwrite
# the binary, config or UI (container immutability).
COPY --from=upstream --chmod=0755 /qdrant/qdrant /qdrant/entrypoint.sh /qdrant/
COPY --from=upstream /qdrant/config /qdrant/config
COPY --from=upstream /qdrant/static /qdrant/static

ENV TZ=Etc/UTC RUN_MODE=production
# Numeric uid so runAsNonRoot can be verified without an /etc/passwd entry.
USER 1001
EXPOSE 6333 6334
CMD ["./entrypoint.sh"]

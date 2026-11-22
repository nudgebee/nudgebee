FROM ghcr.io/nudgebee/alpine:3.23
ARG IMAGE_VERSION=0.0.0
ARG IMAGE_RELEASE=1
LABEL name="nudgebee-services-sidecar" \
      maintainer="nudgebee" \
      vendor="nudgebee" \
      version="${IMAGE_VERSION}" \
      release="${IMAGE_RELEASE}" \
      summary="Nudgebee services sidecar" \
      description="Sidecar container that runs alongside the Nudgebee services API server."

# Install curl for making HTTP requests and bc for floating point math.
# OS-package cache-bust: bumping OS_PKG_EPOCH (ISO week) changes this layer's
# cache key so BuildKit re-runs the apk install and pulls current Alpine security
# patches instead of a stale cached layer. Committed (not a build-arg) so each
# refresh is a reviewable, revertable diff; bump when the Trivy scan flags a stale
# package (e.g. c-ares CVE-2026-33630).
# Currently W44 pulls ca-certificates 20261020, which drops the distrusted
# emSign ECC Root CA - G3.
ARG OS_PKG_EPOCH=2026-W44
RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apk upgrade --no-cache \
    && apk add --no-cache curl bc \
    && adduser -D -g '' appuser

# Copy the monitoring script into the container
COPY monitor.sh /monitor.sh
RUN chmod +x /monitor.sh

# Run as non-root
USER appuser

# The command to run when the container starts
CMD ["/monitor.sh"]

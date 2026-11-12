# Patched mirror of Red Hat's ubi9/nginx-126, the UI sidecar's default image (Red Hat
# chart certification, non-root and OpenShift-friendly). Published to
# ghcr.io/<owner>/nginx next to the Alpine tag, so installs that mirror or override only
# the registry keep resolving `<registry>/nginx`.
#
# Same nginx version and base as the Red Hat image, plus a security-only dnf update so the
# pinned tag picks up UBI fixes between Red Hat rebuilds. Bump OS_PKG_EPOCH (ISO week) to
# force a weekly re-pull. The chart supplies its own nginx.conf and command, so nothing
# of the image's s2i defaults is relied on. Runs as uid 1001, as upstream.
ARG NGINX_TAG=1-1793639981
FROM registry.access.redhat.com/ubi9/nginx-126:${NGINX_TAG}

# Re-declare in the build-stage scope (an ARG before FROM is only visible to FROM lines).
ARG OS_PKG_EPOCH=2026-W44

USER 0
RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && dnf -y --setopt=install_weak_deps=false --nodocs upgrade --security \
    && dnf clean all && rm -rf /var/cache/dnf /var/cache/yum
USER 1001

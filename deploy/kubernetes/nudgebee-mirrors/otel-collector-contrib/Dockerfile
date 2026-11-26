# The upstream OpenTelemetry Collector Contrib binary on Red Hat UBI 9 micro, for
# the nudgebee-agent chart (Red Hat chart certification; the upstream image is not
# UBI-based). Same version, binary, default config, user and entrypoint.
#
# ubi-micro has no package manager, so freshness comes from bumping the base tag.
# OS_PKG_EPOCH is fed by the shared infra-mirrors workflow and refreshes the CA
# bundle only.
FROM ghcr.io/open-telemetry/opentelemetry-collector-releases/opentelemetry-collector-contrib:0.164.0@sha256:340c9ca6a36617084c6661bdc4f0fe92b7cda5a805d61673ec8db375fddc522b AS upstream

# Only the CA bundle is copied out: installing packages over ubi-micro rewrites
# files the base owns, which fails Red Hat's HasModifiedFiles check.
FROM registry.access.redhat.com/ubi9 AS certs
ARG OS_PKG_EPOCH=2026-W48
RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" && dnf upgrade -y ca-certificates

FROM registry.access.redhat.com/ubi9/ubi-micro:9.9-1794478307
ARG IMAGE_RELEASE=1
LABEL name="nudgebee-opentelemetry-collector-contrib" \
      maintainer="nudgebee" \
      vendor="nudgebee" \
      version="0.164.0" \
      release="${IMAGE_RELEASE}" \
      summary="OpenTelemetry Collector Contrib" \
      description="Upstream OpenTelemetry Collector Contrib, used by the Nudgebee agent, on Red Hat UBI 9 micro."
COPY --from=certs /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem /etc/pki/tls/certs/ca-bundle.crt
COPY --from=upstream --chmod=0755 /otelcol-contrib /otelcol-contrib
COPY --from=upstream /etc/otelcol-contrib/config.yaml /etc/otelcol-contrib/config.yaml
# Red Hat certification (HasLicense).
COPY LICENSE.otelcol /licenses/LICENSE.opentelemetry-collector
COPY --from=repo LICENSE /licenses/LICENSE
USER 10001:10001
EXPOSE 4317 4318 55679
ENTRYPOINT ["/otelcol-contrib"]
CMD ["--config", "/etc/otelcol-contrib/config.yaml"]

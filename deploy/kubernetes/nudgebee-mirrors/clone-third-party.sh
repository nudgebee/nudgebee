#!/usr/bin/env bash
# Clone actively-maintained third-party images to ghcr.io/nudgebee so the cluster
# and docker-compose pull from ghcr instead of docker.io (avoiding Docker Hub rate
# limits). These upstreams are already patched, so we copy the exact manifest —
# no rebuild (unlike the frozen bitnamilegacy bases, which need the apt-upgrade
# Dockerfiles in this dir + the infra-mirrors-image.yaml workflow).
#
# Registry-to-registry copy, multi-arch preserved. Run locally after `docker login
# ghcr.io` (needs write access to the target packages). Re-run to bump a version.
#
# Requires: crane (https://github.com/google/go-containerregistry) — `brew install crane`.
set -euo pipefail

NS="${GHCR_NAMESPACE:-ghcr.io/nudgebee}"

# src -> dst:tag
IMAGES=(
  "docker.io/qdrant/qdrant:v1.18.3 qdrant:v1.18.3"
  # Consumed by the nudgebee-agent chart (k8s-agent repo), not the chart in this
  # repo — listed here so the mirror has one documented, reproducible source
  # instead of an ad-hoc push. 0.157.0 is built on Go 1.26.5 with grpc v1.82.1
  # (verified against the release binary), clearing the 1H/1M stdlib pair and the
  # grpc HIGH that 0.156.0 carried. Bump the chart's tag in the same change.
  "ghcr.io/open-telemetry/opentelemetry-collector-releases/opentelemetry-collector-contrib:0.157.0 opentelemetry-collector-contrib:0.157.0"
)

for entry in "${IMAGES[@]}"; do
  src="${entry%% *}"; dst="${entry##* }"
  echo ">>> crane copy $src -> ${NS}/${dst}"
  crane copy "$src" "${NS}/${dst}"
done
echo "done"

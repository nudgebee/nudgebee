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
)

for entry in "${IMAGES[@]}"; do
  src="${entry%% *}"; dst="${entry##* }"
  echo ">>> crane copy $src -> ${NS}/${dst}"
  crane copy "$src" "${NS}/${dst}"
done
echo "done"

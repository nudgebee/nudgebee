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
  "docker.io/qdrant/qdrant:v1.19.0 qdrant:v1.19.0"
  # Temporal. The upstream chart defaults these to docker.io/temporalio/*; the
  # chart's temporal.{server,admintools,web}.image.repository values point at
  # these clones. web/ui is disabled in values.yaml but cloned anyway so
  # re-enabling it does not reintroduce a Docker Hub pull. Bump all three with
  # the chart version — admin-tools runs the schema jobs and must match server.
  "docker.io/temporalio/server:1.31.2 temporal-server:1.31.2"
  "docker.io/temporalio/admin-tools:1.31.2 temporal-admin-tools:1.31.2"
  "docker.io/temporalio/ui:2.52.0 temporal-ui:2.52.0"
  # kubectl toolbox for the rag-server StatefulSet migration hook
  # (rag-server/templates/migration-sts-hooks.yaml, .Values.migrationHookImage).
  "docker.io/alpine/k8s:1.32.13 alpine-k8s:1.32.13"
  # `helm test` connection pods only (templates/tests/test-connection.yaml in the
  # service charts). Pinned rather than the previous untagged `busybox`, which
  # resolved to :latest.
  "docker.io/library/busybox:1.37.0 busybox:1.37.0"
  # Init container for the service pods (global.initImages.curl).
  "docker.io/curlimages/curl:8.10.1 curl:8.10.1"
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

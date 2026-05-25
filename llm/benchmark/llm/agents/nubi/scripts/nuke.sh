#!/usr/bin/env bash
# Tear down all k8s resources created by nubi benchmark fixtures.
# Selects exclusively on the label benchmark=true, which every fixture stamps
# on its namespaces (and on any cluster-scoped resources it creates).
#
# Invoked by benchmark_server's POST /agent-benchmark/infra/nuke endpoint.
set -euo pipefail

LABEL="benchmark=true"

echo "==> Deleting namespaces with ${LABEL}"
kubectl delete ns -l "${LABEL}" --wait=false --ignore-not-found

echo "==> Deleting cluster-scoped PVs with ${LABEL}"
kubectl delete pv -l "${LABEL}" --ignore-not-found

echo "==> Deleting cluster-scoped PVCs with ${LABEL} (just in case)"
kubectl delete pvc -A -l "${LABEL}" --ignore-not-found

echo "==> Nuke complete. Remaining labeled namespaces:"
kubectl get ns -l "${LABEL}" 2>&1 || true

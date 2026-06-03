#!/bin/bash
set -e

# Clean up the restricted access resources
kubectl delete clusterrole test-clusterrole-28 --ignore-not-found
kubectl delete secret restricted-Nubi-sa-token -n 28-test --ignore-not-found
kubectl delete clusterrolebinding restricted-Nubi-binding-28 --ignore-not-found
kubectl delete clusterrole restricted-Nubi-role-28 --ignore-not-found
kubectl delete serviceaccount restricted-Nubi-sa -n 28-test --ignore-not-found

# Delete the test namespace
kubectl delete namespace 28-test --ignore-not-found

# Clean up temporary directory and kubeconfig (cross-platform)
TEMP_BASE="${TMPDIR:-/tmp}"
TEMP_DIR="$TEMP_BASE/Nubi-test-28-permissions"
rm -rf "$TEMP_DIR" 2>/dev/null || true

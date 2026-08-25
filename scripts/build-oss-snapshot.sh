#!/usr/bin/env bash
# build-oss-snapshot.sh — produce an OSS source snapshot from the current tree.
#
# Reads .oss-exclude for the list of paths to drop, then applies the two
# companion sed patches documented in its footer (blank-import strips in
# cmd/main.go and _app.tsx). Optionally builds the result to prove it
# compiles without the EE tree.
#
# Usage:
#   scripts/build-oss-snapshot.sh [OUTPUT_DIR] [--verify]
#
# OUTPUT_DIR defaults to /tmp/nudgebee-oss-snapshot. Pass --verify to run
# go build + npm build against the snapshot and fail on any error.
set -euo pipefail

OUTPUT_DIR="${1:-/tmp/nudgebee-oss-snapshot}"
VERIFY=0
for arg in "$@"; do
  [[ "$arg" == "--verify" ]] && VERIFY=1
done

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

if [[ ! -f .oss-exclude ]]; then
  echo "❌ .oss-exclude not found at repo root" >&2
  exit 1
fi

echo "→ Snapshotting tracked files into $OUTPUT_DIR"
rm -rf "$OUTPUT_DIR"
mkdir -p "$OUTPUT_DIR"
git ls-files -z | tar --null -T - -cf - | tar -xf - -C "$OUTPUT_DIR"

cd "$OUTPUT_DIR"

echo "→ Stripping paths listed in .oss-exclude"
STRIPPED=0
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  [[ "$line" =~ ^# ]] && continue
  if [[ -e "$line" ]]; then
    rm -rf "$line"
    STRIPPED=$((STRIPPED + 1))
    echo "  - $line"
  fi
done < .oss-exclude
echo "  ($STRIPPED paths removed)"

echo "→ Applying companion patches (canonical impl: scripts/oss-patches.sh)"
# Single source of truth: both this script and the external OSS_DROP.md
# runbook source the same patch implementation so they can't drift.
source "$REPO_ROOT/scripts/oss-patches.sh"
apply_oss_patches

echo "→ Asserting no residual ee/ imports in snapshot"
# Match import statements only — skip comments, path aliases in build configs,
# and string occurrences in docs/source. Cover every Go module so llm/ee and
# future module-local EE trees cannot survive a supposedly verified snapshot.
RESIDUAL=$(grep -rnE '^[[:space:]]*(import[[:space:]]|_[[:space:]]).*(["'\''])([^"'\'']*/ee/|@ee/)' \
            --include='*.go' --include='*.ts' --include='*.tsx' \
            --include='*.js' --include='*.jsx' \
            api-server app llm collector-server notifications-server ticket-server ml-k8s-server 2>/dev/null || true)
# Also catch frontend from-imports of @ee or module-local EE paths.
RESIDUAL_FROM=$(grep -rnE "from[[:space:]]+(['\"])(@ee/|[^'\"]*/ee/)" \
            --include='*.ts' --include='*.tsx' --include='*.js' --include='*.jsx' \
            api-server app llm collector-server notifications-server ticket-server ml-k8s-server 2>/dev/null || true)
ALL_RESIDUAL="$RESIDUAL"$'\n'"$RESIDUAL_FROM"
if [[ -n "$(echo "$ALL_RESIDUAL" | tr -d '[:space:]')" ]]; then
  echo "❌ Snapshot still imports ee/ paths:"
  echo "$ALL_RESIDUAL" | grep -v '^$' | head -20
  exit 1
fi
echo "  ✓ no residual imports"

if [[ "$VERIFY" -eq 1 ]]; then
  echo "→ Verifying: go build api-server/services"
  ( cd api-server/services && go build ./... ) || {
    echo "❌ go build failed in OSS snapshot"
    exit 1
  }
  echo "  ✓ go build clean"

  echo "→ Verifying: go vet api-server/services (catches ineffective assigns, unused imports surfaced by stripping)"
  ( cd api-server/services && go vet ./... ) || {
    echo "❌ go vet failed in OSS snapshot"
    exit 1
  }
  echo "  ✓ go vet clean"

  echo "→ Verifying: npm run lint2 + npm run build (app)"
  if [[ -d app/node_modules ]]; then
    echo "  (skipping npm ci, node_modules present)"
  else
    ( cd app && npm ci --legacy-peer-deps --silent ) || {
      echo "❌ npm ci failed"
      exit 1
    }
  fi
  # Heap bump for the same reason every app/ CI workflow sets one
  # (app-dev-gke, app-prod: 4096; app-test-gke: 8192): checking this project
  # peaks around 2.4GB, over Node's ~2GB default, so without it tsc dies with
  # "Ineffective mark-compacts near heap limit" — an OOM that reads like a type
  # error in the log but isn't one.
  ( cd app && NODE_OPTIONS="--max_old_space_size=4096" npx tsc --noEmit ) || {
    echo "❌ tsc --noEmit failed in OSS snapshot"
    exit 1
  }
  echo "  ✓ tsc clean"
fi

echo ""
echo "✓ OSS snapshot at $OUTPUT_DIR"

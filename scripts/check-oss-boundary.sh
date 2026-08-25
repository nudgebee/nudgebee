#!/usr/bin/env bash
#
# scripts/check-oss-boundary.sh — fail if any OSS file imports an EE path.
#
# Reads .oss-exclude at repo root, builds a list of forbidden import
# targets (the directories under api-server/services/ee/ and app/src/ee/),
# then greps every non-EE Go and TS/TSX/JS/JSX file for those targets.
# Any match is a boundary violation and means the OSS snapshot would not
# compile/build cleanly with the EE tree removed.
#
# Exit code: 0 on clean, 1 on violation.
# Run manually:  ./scripts/check-oss-boundary.sh
# Run in CI:     see .github/workflows/oss-boundary.yaml

set -euo pipefail

cd "$(dirname "$0")/.."

# Load .oss-exclude paths so the checker treats listed files/dirs as EE-side
# (they're going to be stripped during the OSS squash anyway, so their
# imports of EE paths are fine).
EE_PATHS=()
while IFS= read -r line; do
  line="${line%%#*}"            # strip comment
  line="$(echo "$line" | xargs)" # trim whitespace
  [[ -z "$line" ]] && continue
  EE_PATHS+=("$line")
done < .oss-exclude

is_ee_path() {
  # Normalise the candidate path so the `./foo` form produced by `find .`
  # matches the `foo` form used in .oss-exclude. Without this strip, an
  # exclude entry like `llm/llm-server/cmd/imports_enterprise.go` would
  # never match `./llm/llm-server/cmd/imports_enterprise.go` and the file
  # would fall through to the EE-import scan.
  local f="${1#./}"
  # Defensive: bash 3.2 (macOS default) treats "${arr[@]}" as unbound
  # when arr is empty under `set -u`. Skip the loop in that case.
  (( ${#EE_PATHS[@]} == 0 )) && return 1
  for ee in "${EE_PATHS[@]}"; do
    [[ "$f" == "$ee"* ]] && return 0
  done
  return 1
}

VIOLATIONS=0

# ─── Backend: every Go module, files outside any ee/ subtree ─────────
# Forbidden import substring: any string literal containing "/ee/" inside
# a Go import path (catches both api-server/services/ee/* and per-module
# ee subtrees in other services like llm-server/api/ee/* if/when they land).
# Today only api-server/services has EE code; this scan is forward-looking
# for the other Go modules in the monorepo.
#
# Exception: cmd/main.go is allowed to blank-import any nudgebee/services/ee/*
# package — the OSS squash strips those lines (see .oss-exclude footer).
# Other files must route through a registry/hook.
FORBIDDEN_GO_IMPORT='"[^"]*/ee/[^"]*"'
while IFS= read -r -d '' file; do
  if is_ee_path "$file"; then continue; fi
  if grep -qE "$FORBIDDEN_GO_IMPORT" "$file"; then
    if [[ "$file" == *"cmd/main.go" ]]; then
      offending=$(grep -E "$FORBIDDEN_GO_IMPORT" "$file" \
                  | grep -vE '^[[:space:]]*_ "[^"]*/ee/' || true)
      if [[ -n "$offending" ]]; then
        echo "❌ $file references EE paths beyond allowed blank imports:"
        echo "$offending" | sed 's/^/    /'
        VIOLATIONS=$((VIOLATIONS + 1))
      fi
    else
      echo "❌ $file imports an /ee/* path (OSS code cannot depend on EE):"
      grep -nE "$FORBIDDEN_GO_IMPORT" "$file" | sed 's/^/    /'
      VIOLATIONS=$((VIOLATIONS + 1))
    fi
  fi
done < <(find . \
            -type f -name '*.go' \
            -not -path '*/node_modules/*' \
            -not -path '*/.git/*' \
            -not -path '*/ee/*' \
            -print0)

# ─── Python modules: every *.py outside an ee/ subtree ───────────────
# Forbidden: `from ee.` / `from .ee.` / `import ee` (absolute + relative).
# Today no Python module has EE code; this is forward-looking for
# ml-k8s-server, llm/rag-server, auto-pilot, notifications-server, etc.
FORBIDDEN_PY_IMPORT='^[[:space:]]*(from[[:space:]]+([._a-zA-Z0-9]*\.)?ee([._a-zA-Z0-9]*)[[:space:]]+import|from[[:space:]]+\.ee[[:space:]]+import|import[[:space:]]+([._a-zA-Z0-9]*\.)?ee([[:space:]]|$))'
while IFS= read -r -d '' file; do
  if is_ee_path "$file"; then continue; fi
  bad=$(grep -nE "$FORBIDDEN_PY_IMPORT" "$file" || true)
  if [[ -n "$bad" ]]; then
    echo "❌ $file imports an ee module (OSS Python code cannot depend on EE):"
    echo "$bad" | sed 's/^/    /'
    VIOLATIONS=$((VIOLATIONS + 1))
  fi
done < <(find . \
            -type f -name '*.py' \
            -not -path '*/node_modules/*' \
            -not -path '*/.git/*' \
            -not -path '*/.venv/*' \
            -not -path '*/venv/*' \
            -not -path '*/__pycache__/*' \
            -not -path '*/ee/*' \
            -print0)

# ─── Frontend: TS/TSX/JS/JSX files outside app/src/ee/ ───────────────
# Forbidden: `from '@ee/...'` and side-effect `import '@ee/...'`.
# Exceptions — same blank-import pattern as cmd/main.go on the backend:
#   - app/src/pages/_app.tsx          → `@ee/init`        (client + page SSR)
#   - app/src/instrumentation.ts      → `@ee/init-server` (Node-only init for API routes;
#                                                          no React, so distinct from @ee/init)
# The OSS squash strips both lines (see .oss-exclude footer + oss-patches.sh).
while IFS= read -r -d '' file; do
  if is_ee_path "$file"; then continue; fi
  bad=$(grep -nE "from ['\"]@ee/|^[[:space:]]*import ['\"]@ee/" "$file" || true)
  if [[ "$file" == *"app/src/pages/_app.tsx" ]]; then
    bad=$(echo "$bad" | grep -vE "^[0-9]+:[[:space:]]*import ['\"]@ee/init['\"]" || true)
  elif [[ "$file" == *"app/src/instrumentation.ts" ]]; then
    bad=$(echo "$bad" | grep -vE "^[0-9]+:[[:space:]]*import ['\"]@ee/init-server['\"]" || true)
  fi
  if [[ -n "$bad" ]]; then
    echo "❌ $file imports @ee/* (OSS frontend code cannot depend on EE):"
    echo "$bad" | sed 's/^/    /'
    VIOLATIONS=$((VIOLATIONS + 1))
  fi
done < <(find app/src \
            -type f \( -name '*.ts' -o -name '*.tsx' -o -name '*.js' -o -name '*.jsx' \) \
            -not -path 'app/src/ee/*' \
            -print0)

if [[ "$VIOLATIONS" -gt 0 ]]; then
  echo
  echo "Boundary check failed: $VIOLATIONS file(s) violate OSS/EE separation."
  echo "Fix by routing the dependency through a registry/hook in OSS code,"
  echo "or by relocating the importing file into ee/."
  exit 1
fi

echo "✓ OSS/EE boundary check passed."

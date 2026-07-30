#!/usr/bin/env bash
#
# Scaffold a new Postgres migration in migrations/app/ with the correct
# version-number + unix-ms-timestamp + flat filename convention, and refresh
# atlas.sum so the new file is hash-tracked.
#
# Usage:
#   ./new-migration.sh <snake_case_name>   # scaffold new migration files
#   ./new-migration.sh --regen-sum         # re-hash atlas.sum after editing
#
# Example:
#   ./new-migration.sh add_widget_color
#   # → creates 1736953412345_V734_add_widget_color.up.sql
#   #          1736953412345_V734_add_widget_color.down.sql
#
# Requires atlas to be installed locally (brew install atlas) — atlas.sum
# staleness fails the migration Job at deploy time, so we refuse to scaffold
# without it.
#
# Why --regen-sum exists: scaffolding creates EMPTY .sql files, hashed as-is.
# When you fill them in with actual DDL, atlas.sum goes stale and CI's
# Migrations-Validate job fails ("atlas.sum is stale"). The re-hash command
# is fiddly (needs the right --dir flag AND ?format=golang-migrate), and CI
# uses a specific invocation that must be matched exactly. --regen-sum
# encodes that one invocation so users don't have to remember it. Callers
# who invoke `atlas migrate hash` by hand with the wrong flags produce a
# sum in Atlas's default format (2 lines per migration) instead of the
# golang-migrate format (1 line per pair), which CI silently rejects.

set -euo pipefail

MIG_DIR=$(cd "$(dirname "$0")/migrations/app" && pwd)
MIG_ROOT=$(cd "$(dirname "$0")" && pwd)

# Common atlas hashing invocation — MUST match .github/workflows/migrations-validate.yaml
# ('file://migrations/app?format=golang-migrate') so scaffolded / re-hashed
# sums are accepted by CI without drift.
regen_atlas_sum() {
  if ! command -v atlas >/dev/null 2>&1; then
    cat <<MSG >&2
ERROR: atlas binary not on PATH; cannot refresh atlas.sum.
       Install with:  brew install atlas
       (atlas is required — atlas.sum staleness fails the migration Job at deploy.)
MSG
    return 1
  fi
  (cd "$MIG_ROOT" && atlas migrate hash \
     --dir "file://migrations/app?format=golang-migrate")
}

# --regen-sum: no scaffolding, just re-hash. For use after editing an
# already-scaffolded migration file (the initial hash was against empty
# files; content changes invalidate it).
if [ $# -eq 1 ] && [ "$1" = "--regen-sum" ]; then
  if regen_atlas_sum; then
    echo "refreshed: ${MIG_DIR}/atlas.sum"
    exit 0
  fi
  exit 1
fi

if [ $# -ne 1 ] || [[ ! "$1" =~ ^[a-z0-9_]+$ ]]; then
  echo "usage: $0 <snake_case_name>       # scaffold new migration" >&2
  echo "       $0 --regen-sum             # re-hash atlas.sum after editing" >&2
  echo "  name must be lowercase letters, digits, underscores only" >&2
  exit 1
fi

NAME=$1

# 1. Next version: highest V<N> + 1.
NEXT_V=$(ls "$MIG_DIR" \
  | grep -oE 'V[0-9]+' \
  | sed 's/^V//' \
  | sort -n \
  | tail -1)
NEXT_V=$((NEXT_V + 1))

# 2. Unix-ms timestamp (Hasura convention, kept so lexicographic sort = time order).
TS=$(python3 -c "import time; print(int(time.time() * 1000))")

# 3. Create both files.
UP="${MIG_DIR}/${TS}_V${NEXT_V}_${NAME}.up.sql"
DOWN="${MIG_DIR}/${TS}_V${NEXT_V}_${NAME}.down.sql"

if [ -e "$UP" ] || [ -e "$DOWN" ]; then
  echo "error: target file already exists; pick a different name" >&2
  exit 1
fi

touch "$UP" "$DOWN"

# 4. Refresh atlas.sum so atlas accepts the new files. The hash file is a
# checksum line per migration plus a top-level integrity hash; atlas refuses
# to apply if the manifest is stale, which is exactly the behavior we want
# (anyone touching migration files re-hashes). If atlas is missing or
# `atlas migrate hash` fails, delete the just-scaffolded files and exit
# loud — atlas.sum drift is otherwise detected only at deploy time, which is
# too late.
if ! regen_atlas_sum; then
  rm -f "$UP" "$DOWN"
  echo "ERROR: atlas migrate hash failed; reverted new files." >&2
  exit 1
fi

echo "created:"
echo "  $UP"
echo "  $DOWN"
echo "  ${MIG_DIR}/atlas.sum (refreshed)"
echo ""
echo "Next: fill in the .up.sql / .down.sql files, then re-hash with:"
echo "  $(basename "$0") --regen-sum"
echo "(Skipping this step, or running \`atlas migrate hash\` by hand with"
echo " different flags, produces a sum CI will reject.)"

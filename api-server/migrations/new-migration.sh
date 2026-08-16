#!/usr/bin/env bash
#
# Scaffold a new Postgres migration in migrations/app/ with the correct
# version-number + unix-ms-timestamp + flat filename convention, and refresh
# atlas.sum so the new file is hash-tracked.
#
# Usage:
#   ./new-migration.sh <snake_case_name>
#
# Example:
#   ./new-migration.sh add_widget_color
#   # → creates 1736953412345_V734_add_widget_color.up.sql
#   #          1736953412345_V734_add_widget_color.down.sql
#
# Requires atlas to be installed locally (brew install atlas) — atlas.sum
# staleness fails the migration Job at deploy time, so we refuse to scaffold
# without it.

set -euo pipefail

if [ $# -ne 1 ] || [[ ! "$1" =~ ^[a-z0-9_]+$ ]]; then
  echo "usage: $0 <snake_case_name>" >&2
  echo "  name must be lowercase letters, digits, underscores only" >&2
  exit 1
fi

NAME=$1
MIG_DIR=$(cd "$(dirname "$0")/migrations/app" && pwd)

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
if ! command -v atlas >/dev/null 2>&1; then
  rm -f "$UP" "$DOWN"
  cat <<MSG >&2
ERROR: atlas binary not on PATH; cannot refresh atlas.sum.
       Install with:  brew install atlas
       (atlas is required — atlas.sum staleness fails the migration Job at deploy.)
MSG
  exit 1
fi

MIG_ROOT=$(cd "$(dirname "$0")" && pwd)
if ! (cd "$MIG_ROOT" && atlas migrate hash \
        --dir "file://migrations/app?format=golang-migrate"); then
  rm -f "$UP" "$DOWN"
  echo "ERROR: atlas migrate hash failed; reverted new files." >&2
  exit 1
fi

echo "created:"
echo "  $UP"
echo "  $DOWN"
echo "  ${MIG_DIR}/atlas.sum (refreshed)"

#!/usr/bin/env bash
#
# Scaffold a new golang-migrate migration in migrations/app/ with the
# correct version-number + unix-ms-timestamp + flat filename convention.
#
# Usage:
#   ./new-migration.sh <snake_case_name>
#
# Example:
#   ./new-migration.sh add_widget_color
#   # → creates 1736953412345_V734_add_widget_color.up.sql
#   #          1736953412345_V734_add_widget_color.down.sql

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

echo "created:"
echo "  $UP"
echo "  $DOWN"

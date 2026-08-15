#!/usr/bin/env bash
#
# OPTIONAL standalone bootstrap tool. Cuts a Postgres DB over from the legacy
# golang-migrate engine to Atlas without applying any migrations.
#
# When to run this manually:
#   * You want to verify Atlas would seed the right baseline before flipping
#     the deployment to the atlas-only image (dry-run-ish — call this, inspect
#     `atlas migrate status`, then deploy the new image).
#   * You're rebuilding an env's revisions table after a manual reset.
#
# In normal operation the atlas-only run-migrations.sh auto-bootstraps inline
# on first run (idempotent). This script is the explicit / pre-deploy
# equivalent that an operator can run from a debug pod.
#
# What it does
# ------------
#   1. Acquires a Postgres advisory lock so two concurrent bootstraps cannot
#      double-seed (the Job pod auto-bootstrap also takes this lock).
#   2. Reads the legacy nudgebee.schema_migrations.version + dirty flag.
#   3. Refuses if dirty=true (operator must resolve a wedged migration via
#      psql before the cutover can proceed).
#   4. Refuses if the legacy version is a phantom (no backing file in
#      ./migrations/app/). Same incident class that wedged rackspace earlier.
#   5. Runs `atlas migrate set <version>` which writes one row per file <=
#      version into nudgebee.atlas_schema_revisions. Hashes come from
#      migrations/app/atlas.sum.
#   6. Runs `atlas migrate status` for sight-verification.
#
# What it does NOT do
# -------------------
#   * Does not apply any migrations. That's run-migrations.sh's job.
#   * Does not touch nudgebee.schema_migrations. The legacy tracker stays
#     in place as a forensic artifact and as a rollback target (redeploying
#     the previous image, which still has golang-migrate, falls back to it).
#   * Does not reconcile sub-baseline gaps. `atlas migrate set` will mark
#     every file <= version as applied — including any silently-skipped files
#     left behind by the V756 / V758 / V760 incident class. Reconcile those
#     via psql FIRST. The 11-file backfills done on test / prod / rackspace
#     earlier this sprint are the model.
#
# Usage
# -----
#   ./atlas-bootstrap.sh
#
# Required env vars (inherited from secret/nudgebee in K8s):
#   APP_DATABASE_URL    Postgres connection string for the env.
#
# Idempotent: if nudgebee.atlas_schema_revisions already has rows, exits 0
# with a notice rather than re-seeding.

set -euo pipefail

if [ -z "${APP_DATABASE_URL:-}" ]; then
  echo "ERROR: APP_DATABASE_URL is not set." >&2
  echo "       Run inside the migration-job pod (envFrom secret/nudgebee) or" >&2
  echo "       export it manually before running." >&2
  exit 1
fi

MIG_ROOT=$(cd "$(dirname "$0")" && pwd)

command -v atlas >/dev/null || { echo "ERROR: atlas binary not in PATH" >&2; exit 1; }
command -v psql  >/dev/null || { echo "ERROR: psql binary not in PATH"  >&2; exit 1; }

# Schema must exist (also created by run-migrations.sh; harmless duplicate).
psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -q -c "CREATE SCHEMA IF NOT EXISTS nudgebee;"

# Acquire a Postgres advisory lock so two concurrent bootstrap runs cannot
# race (helm upgrade + manual kubectl exec, etc.). Lock key is the hash of
# the literal string "nudgebee.atlas-bootstrap" reduced to a signed 32-bit
# integer; arbitrary but stable. pg_advisory_lock blocks until acquired and
# is auto-released on session end (psql exit).
LOCK_KEY=-46688224  # crc32("nudgebee.atlas-bootstrap") - 2^31, precomputed (no python3 in the alpine image)
psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -q -c "SELECT pg_advisory_lock(${LOCK_KEY})" >/dev/null

# trap to release lock on early exit; psql session closes automatically but
# this keeps things tidy if anyone reads logs and wonders.
release_lock() {
  psql "$APP_DATABASE_URL" -q -c "SELECT pg_advisory_unlock(${LOCK_KEY})" >/dev/null 2>&1 || true
}
trap release_lock EXIT

# Stepwise existence probe for the revisions table. A single CASE/COALESCE
# expression with both arms referencing the table fails at parse time on a
# DB where the table is absent — Postgres plans every arm regardless of
# evaluation order. to_regclass() is the safe NULL-on-missing probe.
has_revs_table=$(psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -tAq -c "
  SELECT to_regclass('nudgebee.atlas_schema_revisions') IS NOT NULL;
" | tr -d '[:space:]')

if [ "$has_revs_table" = "t" ]; then
  existing_revs=$(psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -tAq -c "
    SELECT count(*)::text FROM nudgebee.atlas_schema_revisions;
  " | tr -d '[:space:]')
  if [ "${existing_revs:-0}" != "0" ]; then
    echo "Atlas revisions table already populated ($existing_revs rows)."
    echo "Bootstrap is a no-op. Run 'atlas migrate status' to inspect current state."
    exit 0
  fi
fi

# Stepwise probe for the legacy tracker.
has_legacy_tracker=$(psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -tAq -c "
  SELECT to_regclass('nudgebee.schema_migrations') IS NOT NULL;
" | tr -d '[:space:]')

if [ "$has_legacy_tracker" != "t" ]; then
  cat <<MSG >&2

ERROR: no legacy nudgebee.schema_migrations tracker found.
       This env has nothing to bootstrap Atlas from.

If this is a fresh DB, deploy the atlas-only image directly — run-migrations.sh
will apply from scratch and create the revisions table.

If this env was managed by Hasura CLI (pre-cutover), the env was never on
golang-migrate either; the same fresh-DB path applies.
MSG
  exit 1
fi

# Read tracker version + dirty flag. ORDER BY version DESC LIMIT 1 defends
# against a pathological multi-row state (operator manually inserted a recovery
# row); the table is normally single-row.
# NB: Postgres `bool::text` formats as 'true' / 'false', not 't' / 'f' (that
# short form is psql's display formatting via -t, not the text representation).
# We collapse to 't' / 'f' inside SQL so the shell compare stays simple.
legacy_state=$(psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -tAq -c "
  SELECT version::text || ':' || CASE WHEN dirty THEN 't' ELSE 'f' END
  FROM nudgebee.schema_migrations
  ORDER BY version DESC LIMIT 1;
" | tr -d '[:space:]')

if [ -z "$legacy_state" ]; then
  cat <<MSG >&2

ERROR: legacy nudgebee.schema_migrations exists but is empty (no rows).
       Nothing to bootstrap from. If this is a fresh DB, deploy the atlas-only
       image directly — run-migrations.sh handles the empty-tracker case
       (fresh DB path).
MSG
  exit 1
fi

current_version="${legacy_state%:*}"
current_dirty="${legacy_state#*:}"

echo "Legacy tracker: version=$current_version dirty=$current_dirty"

if [ "$current_dirty" = "t" ]; then
  cat <<MSG >&2

ERROR: legacy nudgebee.schema_migrations has dirty=true at version $current_version.

Atlas cutover refuses to seed past a wedged golang-migrate state — the file
at version $current_version is partially applied and seeding revisions would
mark it fully applied incorrectly.

Resolution:
  1. Inspect ./migrations/app/${current_version}_*.up.sql and verify against
     the DB whether the migration was fully, partially, or not applied.
  2. If fully applied:  UPDATE nudgebee.schema_migrations SET dirty=false;
  3. If not applied:    fix or revert the migration content, then clear dirty.
  4. Re-run this bootstrap.
MSG
  exit 1
fi

# Phantom-version guard. Same logic as the steady-state guard in
# run-migrations.sh. Refuse to seed Atlas with a version that does not back
# to a real file — the same class of bug that wedged the rackspace migration
# Job earlier this sprint.
if [ -z "$(find "$MIG_ROOT/migrations/app" -maxdepth 1 -name "${current_version}_*.up.sql" -print -quit 2>/dev/null)" ]; then
  cat <<MSG >&2

ERROR: legacy tracker $current_version has no backing file in
       $MIG_ROOT/migrations/app/${current_version}_*.up.sql.

Bootstrap refuses to seed Atlas with a phantom version. Candidate real
versions (highest 5 in the image):
MSG
  ls "$MIG_ROOT/migrations/app/" | grep -E '_V[0-9]+_.*\.up\.sql$' | sort -r | head -5 | sed 's|^|  |' >&2
  cat <<MSG >&2

Resolution: identify the highest real applied version (inspect schema or
hdb_catalog.hdb_version.cli_state) and reset the legacy tracker:

  UPDATE nudgebee.schema_migrations SET version=<real_version>, dirty=false;

Re-run this bootstrap, which will re-validate the new value.
MSG
  exit 1
fi

# Atlas set marks every file up to and including <version> as applied. Reads
# the per-file SHA256 from migrations/app/atlas.sum. The cd here is into the
# directory that contains atlas.hcl (api-server/migrations/), not the inner
# migrations/app/ directory.
echo "Seeding nudgebee.atlas_schema_revisions to version $current_version..."
(
  cd "$MIG_ROOT"
  atlas migrate set "$current_version" \
    -c "file://atlas.hcl" \
    --env default \
    --url "$APP_DATABASE_URL"
)

echo
echo "Bootstrap complete. Verifying status..."
(
  cd "$MIG_ROOT"
  atlas migrate status \
    -c "file://atlas.hcl" \
    --env default \
    --url "$APP_DATABASE_URL"
)

cat <<MSG

Next steps:
  1. Verify the status above shows the expected pending count (files added
     since this env's legacy tracker advanced).
  2. Deploy the atlas-only migration image. Its run-migrations.sh will
     short-circuit the bootstrap (revisions table already populated) and
     just apply the pending files.
  3. nudgebee.schema_migrations stays in place as a forensic artifact and a
     rollback target — do not drop it for at least 30 days post-cutover.
MSG

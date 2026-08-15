#!/bin/bash

set -e
# pipefail is required so `psql ... | tr` failures propagate. Without it, an
# erroring psql probe silently produces an empty result, the if-arm misclassifies,
# and the wrong branch runs against a real DB.
set -o pipefail

# Atlas is the engine. Per-file revisions live in nudgebee.atlas_schema_revisions;
# --exec-order non-linear (atlas.hcl) makes out-of-order arrivals apply normally
# instead of silently skipping. golang-migrate is no longer shipped in the image —
# the migration-engine swap removed the silent-skip / phantom-version /
# CONCURRENTLY-in-tx incident classes that drove the rewrite (Fixes #33007).
#
# Cutover semantics on first-run-after-deploy:
#   * Atlas revisions table present       → just apply pending (steady state)
#   * Legacy nudgebee.schema_migrations   → auto-seed revisions from the legacy
#     tracker, then apply pending (one-time cutover; idempotent on re-run because
#     the revisions table is now present)
#   * Neither table present (fresh DB)    → apply from scratch; atlas creates
#     the revisions table on first apply
#
# Rollback: redeploy the previous image (which still has golang-migrate).
# nudgebee.schema_migrations is left in place as a forensic artifact and a
# rollback target; nothing in this script ever writes to it.

echo "Running Postgres migrations (atlas)..."

# Atlas requires the schema for its revisions table to already exist; it does
# not auto-create schemas it does not own. IF NOT EXISTS keeps this idempotent.
psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -q -c "CREATE SCHEMA IF NOT EXISTS nudgebee;"

# Stepwise table-presence probes. A single CASE expression with both branches
# referencing both tracker tables would fail at parse time on any DB where one
# is missing (Postgres plans every WHEN/THEN arm before evaluation, and
# `SELECT FROM nudgebee.schema_migrations` cannot be parsed against a DB that
# does not yet have the table). to_regclass() is the safe existence probe.
has_atlas_revs=$(psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -tAq -c "
  SELECT to_regclass('nudgebee.atlas_schema_revisions') IS NOT NULL;
" | tr -d '[:space:]')

has_legacy_tracker=$(psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -tAq -c "
  SELECT to_regclass('nudgebee.schema_migrations') IS NOT NULL;
" | tr -d '[:space:]')

if [ "$has_atlas_revs" = "t" ]; then
    echo "Atlas revisions table present; applying pending migrations."
elif [ "$has_legacy_tracker" = "t" ]; then
    # First-run cutover from golang-migrate. Read the legacy tracker (ORDER BY
    # version DESC LIMIT 1 defends against the pathological multi-row state if
    # operator manually inserted a recovery row) and seed atlas at that version.
    # NB: Postgres `bool::text` formats as 'true' / 'false', not 't' / 'f' (that
    # short form is psql's display formatting via -t, not the text representation).
    # We collapse to 't' / 'f' inside SQL so the shell compare stays simple.
    legacy_state=$(psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -tAq -c "
      SELECT version::text || ':' || CASE WHEN dirty THEN 't' ELSE 'f' END
      FROM nudgebee.schema_migrations
      ORDER BY version DESC LIMIT 1;
    " | tr -d '[:space:]')

    if [ -z "$legacy_state" ]; then
        # Legacy tracker table exists but is empty — treat as fresh DB.
        echo "Legacy nudgebee.schema_migrations is empty; applying from scratch."
    else
        legacy_ver="${legacy_state%:*}"
        legacy_dirty="${legacy_state#*:}"

        if [ "$legacy_dirty" = "t" ]; then
            cat <<MSG >&2

ERROR: legacy nudgebee.schema_migrations has dirty=true at version $legacy_ver.
       Atlas cutover refuses to seed past a wedged golang-migrate state.

Resolution:
  1. Inspect the wedged migration (./migrations/app/${legacy_ver}_*.up.sql) and
     confirm via DB inspection whether it was fully, partially, or not applied.
  2. If fully applied:  UPDATE nudgebee.schema_migrations SET dirty=false;
  3. If not applied:    fix or revert the migration content, then clear dirty.
  4. Re-run this Job; cutover will proceed.
MSG
            exit 1
        fi

        # Validate the legacy version has a backing file in this image. A
        # phantom version (left by 'migrate force <bogus_ts>' historically)
        # would cause atlas to seed revisions for files that do not exist.
        if [ -z "$(find ./migrations/app -maxdepth 1 -name "${legacy_ver}_*.up.sql" -print -quit 2>/dev/null)" ]; then
            cat <<MSG >&2

ERROR: legacy nudgebee.schema_migrations points at version $legacy_ver,
       which has no backing file in ./migrations/app/${legacy_ver}_*.up.sql.

This is a phantom tracker (someone ran 'migrate force <ts>' to a non-existent
timestamp on the previous engine). Atlas cutover refuses to seed past it.

Resolution: identify the highest real applied version (inspect schema or
hdb_catalog.hdb_version.cli_state) and reset the legacy tracker:

  UPDATE nudgebee.schema_migrations SET version=<real_version>, dirty=false;

Then re-run this Job.
MSG
            exit 1
        fi

        # Loud cutover notice. `atlas migrate set <baseline>` marks EVERY file
        # with version <= baseline as applied WITHOUT running it. golang-migrate
        # kept only a single high-water version, never a per-file record, so a
        # sub-baseline file it silently skipped would be marked applied here and
        # NEVER run (the out-of-order cherry-pick / hotfix class this engine swap
        # fixes going forward).
        seeded_count=$(find ./migrations/app -maxdepth 1 -name '*.up.sql' \
            | sed -E 's#.*/([0-9]+)_.*#\1#' \
            | awk -v b="$legacy_ver" '$1+0 <= b+0' | wc -l | tr -d ' ')
        pending_count=$(find ./migrations/app -maxdepth 1 -name '*.up.sql' \
            | sed -E 's#.*/([0-9]+)_.*#\1#' \
            | awk -v b="$legacy_ver" '$1+0 > b+0' | wc -l | tr -d ' ')
        cat <<MSG >&2
======================== ATLAS CUTOVER NOTICE ========================
Seeding from legacy golang-migrate baseline version=$legacy_ver.
  * $seeded_count file(s) with version <= $legacy_ver will be marked APPLIED
    WITHOUT running (atlas migrate set).
  * $pending_count file(s) above the baseline will actually be applied next.
======================================================================
MSG

        # Pre-cutover gap-detection is intentionally omitted in OSS.
        # The 'atlas migrate set' baseline below marks all sub-baseline files applied without running them.
        echo "First-run cutover: seeding atlas_schema_revisions from legacy tracker (version=$legacy_ver)..."
        atlas migrate set "$legacy_ver" \
            -c file://atlas.hcl --env default \
            --url "$APP_DATABASE_URL"
    fi
else
    echo "Fresh DB (no atlas revisions, no legacy tracker); applying from scratch."
fi

echo "Applying migrations via atlas..."
atlas migrate apply \
    -c file://atlas.hcl --env default \
    --url "$APP_DATABASE_URL" \
    --tx-mode file
echo "Postgres migrations OK."

# MIGRATE_SKIP_PLAYBOOK=1 lets local infra-only flows (compose --profile migrate)
# run DB migrations without a live services-server to receive this curl. The
# playbook cron registers itself on first services-server boot, so skipping
# here is safe for dev. Prod migration Jobs leave the env var unset.
if [ "${MIGRATE_SKIP_PLAYBOOK:-0}" = "1" ]; then
    echo "Skipping Agent Playbook load (MIGRATE_SKIP_PLAYBOOK=1)"
else
    echo "Loading Agent Playbook..."
    max_retries=12
    retry_delay=5
    attempt=1
    while [ $attempt -le $max_retries ]; do
        # -f is what makes the retry cover HTTP errors too: without it curl exits
        # 0 on a 5xx and the loop breaks on the first attempt. The handler answers
        # 200 as soon as it accepts the job (the load itself runs in a goroutine),
        # so there is no success-shaped non-2xx here for -f to misread.
        if curl -f -X POST "${SERVICE_API_SERVER_URL}/rpc-cron" -d '{
                "comment": "Load Agent Playbook",
                "name": "Load Agent Playbook",
                "payload": {}
            }' -v -H "X-ACTION-TOKEN: $ACTION_API_SERVER_TOKEN"; then
            break
        fi
        echo "Attempt $attempt of $max_retries failed. Retrying in $retry_delay seconds..."
        sleep $retry_delay
        attempt=$((attempt + 1))
    done
    if [ $attempt -gt $max_retries ]; then
        # Non-fatal on purpose. Two deploy shapes reach this line and neither
        # should lose the DB migration over a best-effort cron registration:
        #   * on-prem (deploy/kubernetes/nudgebee) — post-install/post-upgrade
        #     hook, backoffLimit 0. services-server is rolling out alongside the
        #     Job, so a connection-refused is usually transient and the retries
        #     above clear it; if they do not, exiting here fails the Helm hook
        #     over a cron that would have self-registered anyway.
        #   * SaaS (deploy/kubernetes/migrations) — pre-install/pre-upgrade hook,
        #     so services-server cannot be Ready until this Job succeeds. On a
        #     fresh install the retries can never connect (deadlock, not a blip).
        # Either way the playbook cron self-registers on first services-server
        # boot, so warn loudly and continue to the migrations below.
        echo "WARN: Agent Playbook not loaded after $max_retries attempts —" \
             "services-server did not accept the request at $SERVICE_API_SERVER_URL" \
             "(unreachable, or answering with an HTTP error)." \
             "Continuing; the playbook cron self-registers on boot." >&2
    fi
fi

# MIGRATE_SKIP_RABBITMQ=1 lets the migration Job run in isolation (no RabbitMQ
# broker reachable). Used by the local podman test flow and any other infra-
# free smoke test. Prod migration Jobs leave the env var unset.
if [ "${MIGRATE_SKIP_RABBITMQ:-0}" = "1" ]; then
    echo "Skipping RabbitMQ migrations (MIGRATE_SKIP_RABBITMQ=1)"
else
    echo "Running RabbitMQ migrations..."
    until curl -sf -u "$RABBIT_MQ_USERNAME:$RABBIT_MQ_PASSWORD" "http://$RABBIT_MQ_HOST:15672/api/overview" > /dev/null; do
      echo "Waiting for RabbitMQ management API..."
      sleep 3
    done
    for script in ./migrations/rabbitmq/*.sh; do
      echo "running: $script"
      sh "$script"
    done
fi

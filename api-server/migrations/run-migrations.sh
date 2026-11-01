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

# Existing application connections must still be able to resolve tables after
# reconciliation or newly applied Atlas migrations place them in public.
public_in_path=$(psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -tAq -c \
    "SELECT 'public' = ANY(current_schemas(false))")
if [ "$public_in_path" != "t" ]; then
    echo "ERROR: public is absent from the database connection's search_path; application connections would not resolve migrated tables." >&2
    exit 1
fi

# A historical role-named installation created a known set of application
# tables in nudgebee before this job started putting public first. Relocate
# only those tables, only for that role, before gap backfills or Atlas run.
# The SQL moves them atomically and gives up quickly if live app traffic holds
# a lock; retrying the hook after the workload quiets is safe.
database_role=$(psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -tAq -c 'SELECT current_user')
if [ "$database_role" = "nudgebee" ]; then
    echo "Checking known role-schema tables for reconciliation..."
    if ! psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -q -f ./reconcile-role-schema.sql; then
        echo "ERROR: role-schema reconciliation failed without moving any tables; review conflicts or retry when database traffic is quiet." >&2
        exit 1
    fi
fi

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

# Unqualified CREATE TABLE statements must land in public: some migrations
# immediately refer to the new table as public.<name>. The default PostgreSQL
# search_path is "$user", public, so the nudgebee role resolves the nudgebee
# revisions schema first. Do not change the role or APP_DATABASE_URL used by
# other services; set the path on Atlas's connection only.
atlas_database_url="$APP_DATABASE_URL"
current_schema=$(psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -tAq -c 'SELECT current_schema()')
if [ "$current_schema" != "public" ]; then
    if [ -z "$current_schema" ]; then
        echo "ERROR: migration connection has no current schema." >&2
        exit 1
    fi
    can_create_public=$(psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -tAq -c \
        "SELECT has_schema_privilege(current_user, 'public', 'CREATE')")
    if [ "$can_create_public" != "t" ]; then
        echo "ERROR: migration user cannot create tables in public." >&2
        exit 1
    fi

    # Reordering the path must not change existing relation, function, or
    # type resolution. Fail closed if a name exists in both public and a later
    # schema; even an unqualified function call could otherwise change meaning.
    conflicting_objects=$(psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -tAq -c "
      WITH names AS (
        SELECT 'relation ' || c.relname AS name, n.nspname
        FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
        UNION ALL
        SELECT 'function ' || p.proname, n.nspname
        FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
        UNION ALL
        SELECT 'type ' || t.typname, n.nspname
        FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
      )
      SELECT string_agg(DISTINCT p.name, ', ' ORDER BY p.name)
      FROM names p JOIN names other ON other.name = p.name
      WHERE p.nspname = 'public'
        AND other.nspname <> 'public'
        AND other.nspname = ANY (current_schemas(false));
    ")
    if [ -n "$conflicting_objects" ]; then
        echo "ERROR: cannot put public first; object names exist in both public and the current search path: $conflicting_objects" >&2
        exit 1
    fi

    # Any unqualified CREATE TABLE checks its creation schema only. A pending
    # migration would create a second public table if its name already exists
    # only in a later search-path schema. Atlas status limits this check to
    # files the database has not applied yet.
    pending_files=$(atlas migrate status \
        -c file://atlas.hcl --env default --url "$APP_DATABASE_URL" \
        --format '{{ range .Pending }}{{ .Name }}{{ "\n" }}{{ end }}')
    existing_nonpublic_relations=$(psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -tAq -c "
      SELECT DISTINCT c.relname
      FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
      WHERE n.nspname <> 'public'
        AND n.nspname = ANY (current_schemas(false));
    ")
    pending_collisions=$(ATLAS_PENDING_FILES="$pending_files" \
        EXISTING_NONPUBLIC_RELATIONS="$existing_nonpublic_relations" python3 - <<'PY'
import os
from pathlib import Path
from validate_migrations import created_tables

existing = set(os.environ["EXISTING_NONPUBLIC_RELATIONS"].splitlines())
collisions = set()
for name in os.environ["ATLAS_PENDING_FILES"].splitlines():
    if Path(name).name != name or not name.endswith(".up.sql"):
        raise SystemExit(f"ERROR: unexpected pending migration name: {name}")
    sql = (Path("migrations/app") / name).read_text(encoding="utf-8-sig")
    for schema, table, _ in created_tables(sql):
        if schema is None and table in existing:
            collisions.add(table)
print(", ".join(sorted(collisions)))
PY
    )
    if [ -n "$pending_collisions" ]; then
        echo "ERROR: pending CREATE TABLE would duplicate existing non-public relations: $pending_collisions" >&2
        exit 1
    fi

    atlas_search_path=$(psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -tAq -c "
      SELECT 'public' || COALESCE(',' || string_agg(quote_ident(schema_name), ',' ORDER BY position), '')
      FROM unnest(current_schemas(false)) WITH ORDINALITY AS schemas(schema_name, position)
      WHERE schema_name <> 'public';
    ")
    atlas_database_url=$(ATLAS_SEARCH_PATH="$atlas_search_path" python3 - <<'PY'
import os
from urllib.parse import parse_qsl, quote, urlencode, urlsplit, urlunsplit

url = urlsplit(os.environ["APP_DATABASE_URL"])
params = parse_qsl(url.query, keep_blank_values=True)
if any(key == "search_path" for key, _ in params):
    raise SystemExit("ERROR: cannot override an explicit search_path in APP_DATABASE_URL")
options = next((value for key, value in params if key == "options"), "")
params = [(key, value) for key, value in params if key != "options"]
params.append(("options", (options + " -csearch_path=" + os.environ["ATLAS_SEARCH_PATH"]).strip()))
# PostgreSQL URI parameters decode %20 as a space, not form-style '+'.
print(urlunsplit(url._replace(query=urlencode(params, quote_via=quote))))
PY
    )
    atlas_current_schema=$(psql "$atlas_database_url" -v ON_ERROR_STOP=1 -tAq -c 'SELECT current_schema()')
    if [ "$atlas_current_schema" != "public" ]; then
        echo "ERROR: Atlas connection would not use public as its current schema." >&2
        exit 1
    fi
    echo "Atlas migration search_path: $atlas_search_path"
fi

echo "Applying migrations via atlas..."
atlas migrate apply \
    -c file://atlas.hcl --env default \
    --url "$atlas_database_url" \
    --tx-mode file
echo "Postgres migrations OK."

# MIGRATE_SKIP_PLAYBOOK=1 lets local infra-only flows (compose --profile migrate)
# run DB migrations without a live services-server to receive this curl. The
# playbook cron registers itself on first services-server boot, so skipping
# here is safe for dev. Prod migration Jobs leave the env var unset.
if [ "${MIGRATE_SKIP_PLAYBOOK:-0}" = "1" ]; then
    echo "Skipping Agent Playbook load (MIGRATE_SKIP_PLAYBOOK=1)"
else
    # This Job runs as a post-install/post-upgrade Helm hook, so it can start
    # before the services-server pods are Ready. Without a wait, the POST below
    # hits a TCP connect timeout and (under `set -e`) fails the whole Job,
    # skipping the ClickHouse + RabbitMQ steps that follow. Poll /health for a
    # bounded window first. If services-server never becomes ready we log and
    # continue rather than fail: the playbook cron self-registers on first
    # services-server boot, so this trigger is best-effort.
    if [ -z "${SERVICE_API_SERVER_URL:-}" ]; then
        echo "WARN: SERVICE_API_SERVER_URL is not set; skipping Agent Playbook trigger (cron self-registers on services-server boot)."
    else
        max_attempts="${MIGRATE_PLAYBOOK_WAIT_ATTEMPTS:-60}"
        interval="${MIGRATE_PLAYBOOK_WAIT_INTERVAL:-5}"
        attempt=0
        playbook_ready=0
        while [ "$attempt" -lt "$max_attempts" ]; do
            if curl -sf -m 5 "$SERVICE_API_SERVER_URL/health" > /dev/null 2>&1; then
                playbook_ready=1
                break
            fi
            attempt=$((attempt + 1))
            echo "Waiting for services-server health ($SERVICE_API_SERVER_URL/health), attempt $attempt/$max_attempts..."
            sleep "$interval"
        done

        if [ "$playbook_ready" = "1" ]; then
            echo "Loading Agent Playbook..."
            curl -f -X POST "$SERVICE_API_SERVER_URL/rpc-cron" -d '{
                    "comment": "Load Agent Playbook",
                    "name": "Load Agent Playbook",
                    "payload": {}
                }' -H "X-ACTION-TOKEN: $ACTION_API_SERVER_TOKEN" \
                || echo "WARN: Agent Playbook trigger failed; cron self-registers on services-server boot, continuing."
        else
            echo "WARN: services-server not healthy after $((max_attempts * interval))s; skipping Agent Playbook trigger (cron self-registers on services-server boot)."
        fi
    fi
fi

# MIGRATE_SKIP_RABBITMQ=1 lets the migration Job run in isolation (no RabbitMQ
# broker reachable). Used by the local podman test flow and any other infra-
# free smoke test. Prod migration Jobs leave the env var unset.
if [ "${MIGRATE_SKIP_RABBITMQ:-0}" = "1" ]; then
    echo "Skipping RabbitMQ migrations (MIGRATE_SKIP_RABBITMQ=1)"
else
    echo "Running RabbitMQ migrations..."
    # Bounded, and never fatal. These scripts only configure the broker and are
    # idempotent, so a run skipped here is applied by the next deploy. The wait
    # used to be unbounded: a broker that never comes back (down, moved, wrong
    # credentials) held the Job open forever, and on-prem that Job is a
    # post-install,post-upgrade Helm hook with backoffLimit: 0 -- the upgrade
    # hangs instead of finishing. A failing script was fatal for the same
    # reason, since the runner is under `set -e`.
    rabbit_ready=0
    rabbit_deadline=$((SECONDS + 180))
    while [ "$SECONDS" -lt "$rabbit_deadline" ]; do
      if curl -sf --max-time 5 -u "$RABBIT_MQ_USERNAME:$RABBIT_MQ_PASSWORD" "http://$RABBIT_MQ_HOST:15672/api/overview" > /dev/null; then
        rabbit_ready=1
        break
      fi
      echo "Waiting for RabbitMQ management API..."
      sleep 3
    done
    if [ "$rabbit_ready" = "1" ]; then
      for script in ./migrations/rabbitmq/*.sh; do
        echo "running: $script"
        sh "$script" || echo "WARN: $script failed; continuing (it re-runs on the next deploy)" >&2
      done
    else
      echo "WARN: RabbitMQ management API at $RABBIT_MQ_HOST:15672 unreachable for 3m;" \
           "skipping RabbitMQ migrations (they re-run on the next deploy)." >&2
    fi
fi

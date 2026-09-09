# Migrations

This directory contains all database migrations for the Nudgebee platform.

**Postgres migrations are applied by [Atlas Community](https://atlasgo.io/) edition.** The previous engine, golang-migrate, was removed in PR #33008 (Fixes #33007) — its single-row tracker kept producing silent-skip, phantom-version, and CONCURRENTLY-in-tx incidents that Atlas's per-file revision model makes structurally impossible.

> **Cutover from golang-migrate is automatic.** The atlas-only migration Job's `run-migrations.sh` detects a legacy `nudgebee.schema_migrations` tracker on first run and seeds `nudgebee.atlas_schema_revisions` from it before applying any pending files. No operator action required beyond deploying the new image. See ["Per-env cutover"](#per-env-cutover) below.

## Directory structure

```
api-server/migrations/
├── Dockerfile                # Migration job image (atlas + psql)
├── run-migrations.sh         # Entrypoint: applies migrations on deploy
├── new-migration.sh          # Scaffold — creates .up/.down + regenerates atlas.sum
├── atlas.hcl                 # Atlas project config (non-linear exec, nudgebee schema)
├── atlas-bootstrap.sh        # OPTIONAL standalone bootstrap tool (run-migrations.sh
│                             #   does the same inline on first run)
├── validate_migrations.py    # Pre-merge hygiene gate (duplicate ts, V-label reuse)
└── migrations/
    ├── app/
    │   ├── *.up.sql / *.down.sql   # Postgres migrations (flat layout)
    │   └── atlas.sum               # Per-file SHA256 manifest (auto-regenerated)
    ├── clickhouse/           # Kept in tree; NOT auto-applied — apply manually if clickhouse.enabled=true
    └── rabbitmq/             # Shell scripts run after RabbitMQ is healthy
```

## Migration types

### Postgres (`migrations/app/`)

**Flat layout** — one file per migration, no enclosing directory:

```
{timestamp_ms}_V{N}_{description}.up.sql
{timestamp_ms}_V{N}_{description}.down.sql   # optional but recommended
```

Examples:
```
1665080411172_V0.up.sql
1774614951697_V655_fix_event_duplicates_fk_cascade.up.sql
1782796952980_V773_llm_watch_tasks.up.sql
```

**Filename rules:**
- `{timestamp_ms}` — current Unix timestamp in **milliseconds**. Used as the version key. Lexicographic sort = time order.
- `V{N}` — sequential version counter for human readability. Increments by 1. Reuse fails the PR-time validator.
- `{description}` — `snake_case`, optional but encouraged.
- **Atlas applies out-of-order arrivals natively.** A file with ts < current tracker (cherry-pick / HF backmerge) is applied via `--exec-order non-linear` — the V756/V758/V760 silent-skip class is structurally impossible.
- **`CREATE INDEX CONCURRENTLY` / `DROP INDEX CONCURRENTLY` are rejected by CI** (`.github/workflows/migrations-lint.yaml`). Atlas itself supports a per-file `-- atlas:txmode none` directive that would let a `CONCURRENTLY` statement run outside the `--tx-mode file` transaction, but OSS keeps the outright-reject lint for now. If you need a large-table index without the brief `ACCESS EXCLUSIVE` lock a plain statement takes, apply the `CONCURRENTLY` form manually via `psql` against the live DB *before* opening the PR, then write the migration with plain `CREATE INDEX IF NOT EXISTS` / `DROP INDEX IF EXISTS`.
- Use `IF NOT EXISTS` / `IF EXISTS` where possible for idempotency.

### Clickhouse (`migrations/clickhouse/`)

**Not applied by Atlas in OSS.** The numbered `.sql` files (`NN_*.up.sql`) are kept in the tree, but the ClickHouse apply path was removed from `run-migrations.sh` in the atlas cutover, and Atlas Community does not support ClickHouse (Pro tier only). **If you deploy with `clickhouse.enabled=true`, apply these migrations manually** against your own ClickHouse instance — nothing in the migration Job will do it for you.

### RabbitMQ (`migrations/rabbitmq/`)

Shell scripts run sequentially after RabbitMQ is healthy:

```
001_remove_autopilot_queues.sh
```

Skipped when `MIGRATE_SKIP_RABBITMQ=1` (used by local smoke-test flows).

## How migrations run on deploy

The migration job is a Helm `pre-install,pre-upgrade` hook. On every deploy, the chart creates a Kubernetes Job whose container runs [`run-migrations.sh`](./run-migrations.sh):

1. `psql ... -c "CREATE SCHEMA IF NOT EXISTS nudgebee;"` — Atlas does not auto-create the schema its revisions table lives in.
2. **Cutover detection** (stepwise `to_regclass` probes — never a single CASE expression):
   - Atlas revisions table present → just apply pending (steady state).
   - Legacy `nudgebee.schema_migrations` present (first run after cutover) → read its version + dirty flag, refuse if `dirty=true` or if version is a phantom, then `atlas migrate set <version>` to seed revisions.
   - Neither present (fresh DB) → apply from scratch.
3. `atlas migrate apply -c file://atlas.hcl --env default --url ... --tx-mode file` — applies pending migrations. Out-of-order arrivals are applied via `--exec-order non-linear` (atlas.hcl).
4. Calls the API server to reload the agent playbook (skipped if `MIGRATE_SKIP_PLAYBOOK=1`).
5. Waits for RabbitMQ, runs each `migrations/rabbitmq/*.sh` (skipped if `MIGRATE_SKIP_RABBITMQ=1`).

CI in `nudgebee-infra` builds + pushes the migration image and runs `helm upgrade ... --wait --wait-for-jobs`, which blocks until the Job exits 0.

**Tools in the image:**
- atlas community `v1.3.0` (SHA256-pinned in Dockerfile)
- `postgresql-client` (for `psql` probes in run-migrations.sh)

`ATLAS_NO_UPDATE_NOTIFIER=1` is set in the Dockerfile — no outbound HTTP from the Job container on every run. Relevant for rackspace and other restricted-egress environments.

## Migration version tracking

Atlas tracks state in `nudgebee.atlas_schema_revisions`:

```sql
CREATE TABLE nudgebee.atlas_schema_revisions (
  version          varchar PRIMARY KEY,
  description      varchar NOT NULL,
  type             bigint  NOT NULL DEFAULT 2,
  applied          bigint  NOT NULL DEFAULT 0,
  total            bigint  NOT NULL DEFAULT 0,
  executed_at      timestamptz NOT NULL,
  execution_time   bigint NOT NULL,
  error            text,
  error_stmt       text,
  hash             varchar NOT NULL,
  partial_hashes   jsonb,
  operator_version varchar NOT NULL
);
```

How it works:
- **One row per applied file** (keyed by `version`, which is the timestamp from the filename).
- `hash` is the per-file SHA256 from `migrations/app/atlas.sum`. Edits to applied files cause subsequent applies to refuse with `checksum mismatch` (desired — silent edits to applied SQL are a footgun).
- `applied` / `total` show the statement-level progress within a file (catches mid-file crashes).
- "Is migration X applied?" = "is there a row with `version = X`?"

The legacy `nudgebee.schema_migrations` table is left in place after cutover as a forensic artifact and a rollback target. Nothing writes to it in the atlas-only path. **Do not drop it for at least 30 days post-cutover.**

## `atlas.sum` — integrity manifest

`migrations/app/atlas.sum` is a committed, auto-generated file. Each line is `{filename} h1:{base64_sha256}=` for one migration file, plus a top line that is a hash-of-hashes covering the whole directory (a small Merkle tree). Atlas reads it on every `migrate apply` and refuses to run if any file's content disagrees with the manifest.

### Why the file is required

Atlas rejects `migrate apply` without a valid manifest:

```
Error: sql/migrate: checksum file not found
```

There is no `--skip-sum` flag. `--allow-dirty` is a different thing (it's about the target database, not the migration directory). The manifest is a load-bearing part of Atlas's directory format.

### What it protects against

**Silent edits to migration files that have already been applied on some environment.** Under golang-migrate, editing an applied `.up.sql` shipped without any noise — the tracker only records the version integer, not any hash of the content. Different environments could quietly diverge on the same version number.

Under Atlas, the moment any tracked file's SHA256 changes, `atlas migrate apply` on every downstream environment refuses with `checksum mismatch`. That includes:

- accidental edits to an old migration on a developer's branch
- a bad rebase that clobbers an applied migration
- a hand-fix in production that drifted from the committed source

The manifest turns those into a loud, blocking failure at the next deploy — which is the whole reason we tolerate the operational cost below.

### Operational costs

- **~1000-line file** grows by one line per migration.
- **Merge conflicts on every parallel migration PR.** The top-line `h1:` header covers all files, so two PRs that each add a migration both rewrite that line. The losing PR runs `atlas migrate hash` after the merge to regenerate. `new-migration.sh` does the regeneration automatically; conflict resolution is `cd api-server/migrations && atlas migrate hash --dir 'file://migrations/app?format=golang-migrate'` and commit.
- **Developers need `atlas` installed locally** (`brew install atlas`) — `new-migration.sh` refuses without it because a stale manifest fails the migration Job at deploy time.
- **CI enforces freshness.** `.github/workflows/migrations-validate.yaml` installs the pinned atlas binary and runs `atlas migrate hash` + `git diff --exit-code`, blocking any PR with drift.

### Alternatives considered and rejected

**(A) Don't commit `atlas.sum`; regenerate inside the migration Job.** Adds `atlas migrate hash` at the top of `run-migrations.sh` before the apply. Result: nobody sees hash changes in PR review, drift between developer intent and shipped content is invisible, and the "silent edit" protection is effectively removed. Rejected — it deletes the safety property that justifies the file's existence in the first place.

**(B) `--allow-dirty` or similar bypass.** Not real — that flag is for starting the engine on a non-clean target database, not for skipping manifest validation. Atlas has no supported "skip sum" mode. Rejected.

**(C) Ignore the merge-conflict cost and revisit later.** Chosen. If it becomes a real workflow pain (>1/week), option A becomes viable at the cost of losing edit-detection.

### If you see `checksum mismatch`

```bash
cd api-server/migrations
atlas migrate hash --dir 'file://migrations/app?format=golang-migrate'
git diff api-server/migrations/migrations/app/atlas.sum
```

If the diff shows only additions for files you actually added (or hash changes for files you actually edited), commit the regenerated manifest. If it shows hash changes for files you did NOT touch — stop and investigate; something in the tree is silently different from what was reviewed.

## Per-env cutover

The atlas-only `run-migrations.sh` is **fully self-bootstrapping**. The cutover procedure per env is:

1. Build + tag the atlas-only image (already done — PR #33008).
2. Deploy via Helm. The migration Job runs, detects the legacy tracker, seeds `nudgebee.atlas_schema_revisions` via `atlas migrate set`, then applies any pending files.
3. Sight-verify post-deploy:
   ```bash
   kubectl exec -it <any-pod-with-psql> -- psql "$APP_DATABASE_URL" -c "
     SELECT count(*) AS revs, max(version) AS max_ts FROM nudgebee.atlas_schema_revisions;
     SELECT version, dirty FROM nudgebee.schema_migrations;  -- frozen at last golang-migrate value
   "
   ```

Optional pre-deploy verification (run from a debug pod with the new image, against the env DB):

```bash
./atlas-bootstrap.sh          # idempotent — no-op if already done; refuses dirty / phantom
atlas migrate status \
  -c file://atlas.hcl --env default --url "$APP_DATABASE_URL"
```

**Rollback:** redeploy the previous image (which still has golang-migrate). The legacy `nudgebee.schema_migrations` table is unchanged, so golang-migrate picks up exactly where it left off. **Caveat:** files applied via atlas AFTER cutover do not appear in `nudgebee.schema_migrations`. golang-migrate, on rollback, will treat them as pending and try to re-run them; most are `IF NOT EXISTS`-guarded and safe, but verify any file applied post-cutover for re-entrancy before rolling back. Cleanest rollback window is within the first run before any post-cutover migrations land.

## Creating a new migration

```bash
./api-server/migrations/new-migration.sh add_widget_color
# → creates 1736953412345_V734_add_widget_color.up.sql
#          1736953412345_V734_add_widget_color.down.sql
#          regenerates api-server/migrations/migrations/app/atlas.sum
```

Requires `brew install atlas` (or [other install methods](https://atlasgo.io/docs#installation)) — the script refuses without atlas because stale `atlas.sum` fails the migration Job at deploy.

Write your SQL in the `.up.sql`. Examples:

```sql
-- Normal migration:
ALTER TABLE widgets ADD COLUMN color text NOT NULL DEFAULT '#888888';
CREATE INDEX IF NOT EXISTS idx_widgets_color ON widgets (color);
```

```sql
-- Large-table index without ACCESS EXCLUSIVE lock:
-- atlas:txmode none
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_huge_table_foo ON huge_table (foo);
```

Write the matching `.down.sql` for rollback (optional but recommended).

`atlas.sum` is regenerated automatically by `new-migration.sh`. If you hand-edit an existing migration file, regenerate manually:

```bash
cd api-server/migrations
atlas migrate hash --dir 'file://migrations/app?format=golang-migrate'
```

## Local development

### Prerequisites

```bash
brew install atlas
brew install libpq    # for psql, if not already present
```

### Apply migrations against local Postgres

```bash
cd api-server/migrations

atlas migrate apply \
  -c file://atlas.hcl --env default \
  --url 'postgres://postgres:postgrespassword@localhost:5432/nudgebee?sslmode=disable' \
  --tx-mode file
```

### Common operations

```bash
# Status
atlas migrate status \
  -c file://atlas.hcl --env default \
  --url 'postgres://postgres:postgrespassword@localhost:5432/nudgebee?sslmode=disable'

# Apply only N migrations forward
atlas migrate apply 1 \
  -c file://atlas.hcl --env default \
  --url '...'

# Roll back N migrations (runs .down.sql files in reverse)
atlas migrate down 1 \
  -c file://atlas.hcl --env default \
  --url '...'

# Refresh atlas.sum after editing migration files
atlas migrate hash --dir 'file://migrations/app?format=golang-migrate'
```

### Reproducing the production migration Job locally

Build the image, run it against a local Postgres container, skip the non-Postgres steps:

```bash
cd api-server/migrations
podman build --build-arg TARGETARCH=arm64 -t nudgebee-migration:local .

podman run --rm \
  -e APP_DATABASE_URL='postgres://postgres:postgrespassword@host.containers.internal:5432/nudgebee?sslmode=disable' \
  -e MIGRATE_SKIP_PLAYBOOK=1 \
  -e MIGRATE_SKIP_RABBITMQ=1 \
  nudgebee-migration:local
```

The full multi-env (dev/test/prod) sprint-promotion + HF-cherry-pick test that exercises the out-of-order path is documented in PR #33008's verification log.

## CI/CD workflows

> **For external contributors:** Nudgebee's hosted SaaS deploys migrations from a private internal-CD repo (`nudgebee-infra`). You don't need access to it. If you're deploying Nudgebee yourself, the [`deploy/kubernetes/nudgebee/`](../../deploy/kubernetes/nudgebee/) Helm chart in this repo runs migrations as a built-in `pre-install,pre-upgrade` Job (see ["How migrations run on deploy"](#how-migrations-run-on-deploy) above) — no separate CI pipeline required.

The migration build + deploy lives in [`nudgebee-infra`](https://github.com/nudgebee/nudgebee-infra), not this repo:

| Workflow                          | Trigger        | What it does                                                                  |
| --------------------------------- | -------------- | ----------------------------------------------------------------------------- |
| `migrations-dev-gke.yaml`         | push to `main` | Builds image, pushes to ECR, `helm upgrade --wait-for-jobs` against dev GKE   |
| `migrations-test-gke.yaml`        | push to `test` | Same against test cluster                                                     |
| `migrations-prod.yaml`            | push to `prod` | Same against prod cluster                                                     |

`--wait-for-jobs` blocks the CI step until the K8s Job completes, so CI failure correctly reflects `atlas migrate apply` failure.

In this repo:

| Workflow                          | Purpose                                                                            |
| --------------------------------- | ---------------------------------------------------------------------------------- |
| `migrations-validate.yaml`        | Per-PR: filename / V-label / pairing checks (`validate_migrations.py`) + `atlas.sum` drift check + `atlas migrate validate` + fresh-DB apply smoke test |
| `migrations-lint.yaml`            | Per-PR: rejects `CONCURRENTLY` in executable SQL outright                          |

## Environment variables

| Variable                 | Description                                                                          |
| ------------------------ | ------------------------------------------------------------------------------------ |
| `APP_DATABASE_URL`       | Postgres connection URL.                                                             |
| `SERVICE_API_SERVER_URL` | API server URL (for the agent-playbook reload step).                                 |
| `ACTION_API_SERVER_TOKEN`| Auth token for the API server reload call.                                           |
| `MIGRATE_SKIP_PLAYBOOK`  | `1` skips the playbook curl. Unset on prod migration Jobs.                           |
| `MIGRATE_SKIP_RABBITMQ`  | `1` skips RabbitMQ migrations. Unset on prod migration Jobs.                         |
| `RABBIT_MQ_HOST`         | RabbitMQ host (unused when `MIGRATE_SKIP_RABBITMQ=1`).                               |
| `RABBIT_MQ_USERNAME`     | RabbitMQ username.                                                                   |
| `RABBIT_MQ_PASSWORD`     | RabbitMQ password.                                                                   |
| `ATLAS_NO_UPDATE_NOTIFIER` | Pinned to `1` in Dockerfile — disables atlas's daily HTTP update check.            |

## Troubleshooting

### Atlas refuses with `checksum mismatch`

`migrations/app/atlas.sum` is stale relative to the on-disk migration files. Regenerate:

```bash
cd api-server/migrations
atlas migrate hash --dir 'file://migrations/app?format=golang-migrate'
git diff api-server/migrations/migrations/app/atlas.sum   # sanity-check
git add api-server/migrations/migrations/app/atlas.sum
```

`new-migration.sh` does this automatically when creating a new migration. The PR-time CI (`migrations-validate.yaml`) catches drift before merge.

### Migration Job dies with cutover error

Three classes of refusal in `run-migrations.sh`:

1. **`dirty=true` in legacy tracker.** A previous golang-migrate run wedged mid-apply. Resolve:
   ```bash
   psql "$APP_DATABASE_URL" -c "SELECT version, dirty FROM nudgebee.schema_migrations"
   # Inspect ./migrations/app/<version>_*.up.sql vs the actual DB state.
   # If fully applied:  UPDATE nudgebee.schema_migrations SET dirty=false;
   ```

2. **Phantom version.** Legacy tracker points at a ts that has no backing file (someone ran `migrate force <bogus_ts>` historically). Reset:
   ```bash
   # Find the real highest-applied version (inspect schema or hdb_catalog.hdb_version.cli_state)
   psql "$APP_DATABASE_URL" -c "UPDATE nudgebee.schema_migrations SET version=<real>, dirty=false"
   ```

3. **Atlas `pending: out of order`.** Will not happen with `--exec-order non-linear` configured in atlas.hcl (it's the whole point of the engine swap). If you see it, atlas.hcl was overridden or the apply was invoked with explicit `--exec-order linear`.

> **Note:** OSS does not ship the pre-cutover gap-detection tooling. `atlas migrate set <baseline>` marks every file at or below the legacy tracker's version as applied without running it, so on a from-scratch legacy cutover confirm the legacy tracker's high-water version is the true highest-applied migration before deploying.

### Verify applied version on a remote environment

```bash
# Atlas:
psql "$REMOTE_APP_DATABASE_URL" -c "
  SELECT count(*) AS revs, max(version) AS max_ts FROM nudgebee.atlas_schema_revisions
"

# Legacy tracker (frozen at last golang-migrate value, for forensic comparison):
psql "$REMOTE_APP_DATABASE_URL" -c "
  SELECT version, dirty FROM nudgebee.schema_migrations
"
```

### Rolling back to the previous image

Redeploy the image tag from before cutover. golang-migrate reads `nudgebee.schema_migrations`, which is unchanged. **Warning:** any files atlas applied post-cutover are not in that tracker; golang-migrate will treat them as pending and try to re-apply them. Most are `IF NOT EXISTS`-guarded and re-run safely; verify before rolling back.

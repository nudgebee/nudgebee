# Migrations

This directory contains all database migrations for the Nudgebee platform, applied via [golang-migrate](https://github.com/golang-migrate/migrate).

## Directory structure

```
api-server/migrations/
├── Dockerfile                # Migration job image (golang-migrate + psql)
├── run-migrations.sh         # Entrypoint: applies all migrations on deploy
├── new-migration.sh          # Scaffold script for new Postgres migrations
└── migrations/
    ├── app/                  # Postgres migrations (main database) — flat layout
    ├── clickhouse/           # Clickhouse migrations (analytics DB)
    └── rabbitmq/             # RabbitMQ setup scripts
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
1778762877644_V734_pinot_integration_type.up.sql
```

**Filename rules:**
- `{timestamp_ms}` — current Unix timestamp in **milliseconds**. Used as the version number by golang-migrate (leading integer is parsed as `BIGINT`). Lexicographic sort = time order.
- `V{N}` — sequential version counter for human readability. Increments by 1.
- `{description}` — `snake_case`, optional but encouraged.
- **Never** use `CREATE INDEX CONCURRENTLY` — migrations run inside a transaction by default. If you really need it, put `-- migrate:no-transaction` at the top of the file.
- Use `IF NOT EXISTS` / `IF EXISTS` where possible for idempotency.

### Clickhouse (`migrations/clickhouse/`)

Numbered SQL files applied by the same golang-migrate binary:

```
00_db.up.sql
01_create_audit_log_shard.up.sql
...
```

Only applied when `CLICKHOUSE_ENABLED=true`.

### RabbitMQ (`migrations/rabbitmq/`)

Shell scripts run sequentially after RabbitMQ is healthy:

```
001_remove_autopilot_queues.sh
```

## How migrations run on deploy

The migration job is a Helm `pre-install,pre-upgrade` hook. On every deploy, the chart creates a Kubernetes Job whose container runs [`run-migrations.sh`](./run-migrations.sh):

1. `psql ... -c "CREATE SCHEMA IF NOT EXISTS nudgebee;"` — golang-migrate doesn't auto-create the schema its tracker lives in.
2. `migrate -path ./migrations/app -database "${APP_DATABASE_URL}?x-migrations-table=%22nudgebee%22.%22schema_migrations%22&x-migrations-table-quoted=true" up` — applies pending Postgres migrations.
3. Calls the API server to reload the agent playbook.
4. If `CLICKHOUSE_ENABLED=true`: `migrate -path ./migrations/clickhouse ... up`.
5. Waits for RabbitMQ, runs each `migrations/rabbitmq/*.sh`.

CI in `nudgebee-infra` (private) builds + pushes the migration image and runs `helm upgrade ... --wait --wait-for-jobs`, which blocks until the Job exits 0.

**Tools in the image:**
- golang-migrate `v4.17.0`
- `postgresql-client` (just for `psql` to create the schema)

## Migration version tracking

golang-migrate tracks state in a **single-row** table `nudgebee.schema_migrations`:

```sql
CREATE TABLE nudgebee.schema_migrations (
  version BIGINT  NOT NULL PRIMARY KEY,
  dirty   BOOLEAN NOT NULL
);
```

How it works:

- **Always exactly one row.** Records the highest version applied + a `dirty` flag.
- Applying a migration **updates** the row in place (does not insert a new row per migration).
- "Is migration X applied?" = `current version >= X`.
- If a migration fails partway, the row is left `dirty=true` and subsequent runs refuse to proceed until a human investigates.

**Footgun: out-of-order versions.** If you add a migration with a timestamp lower than the current applied version, golang-migrate will **silently skip it** — its version is already below `current`. Always use [`new-migration.sh`](./new-migration.sh) (or `int(time.time() * 1000)`) to guarantee a fresh timestamp.

## Creating a new migration

### Easy path: scaffold script

```bash
./api-server/migrations/new-migration.sh add_widget_color
# → creates 1736953412345_V734_add_widget_color.up.sql
#          1736953412345_V734_add_widget_color.down.sql
```

The script picks the next `V<N>`, generates the unix-ms timestamp, and creates both files in the correct flat layout. Then write your SQL.

### Manual path (what the script does)

1. **Pick the next version number** (highest existing `V<N>` + 1):
   ```bash
   ls api-server/migrations/migrations/app/ | grep -oE 'V[0-9]+' | sort -V | tail -1
   ```

2. **Generate a unix-ms timestamp**:
   ```bash
   python3 -c "import time; print(int(time.time() * 1000))"
   ```

3. **Create the two files** (flat layout, no subdirectory):
   ```bash
   TS=<from step 2>
   N=<from step 1>
   NAME=add_widget_color
   touch api-server/migrations/migrations/app/${TS}_V${N}_${NAME}.up.sql
   touch api-server/migrations/migrations/app/${TS}_V${N}_${NAME}.down.sql
   ```

4. **Write SQL** in `.up.sql` (plain Postgres DDL/DML):
   ```sql
   ALTER TABLE widgets ADD COLUMN color text NOT NULL DEFAULT '#888888';
   CREATE INDEX IF NOT EXISTS idx_widgets_color ON widgets (color);
   ```

5. **Write the matching `.down.sql`** for rollback (optional but recommended):
   ```sql
   ALTER TABLE widgets DROP COLUMN IF EXISTS color;
   DROP INDEX IF EXISTS idx_widgets_color;
   ```

6. **Test locally** (see below).

7. **Commit and open a PR.** The migration job applies whatever is pending on next deploy.

## Local development

### Prerequisites

```bash
# Install golang-migrate (matches the version used in the migration image)
brew install golang-migrate   # or download from github.com/golang-migrate/migrate/releases/tag/v4.17.0
```

### Apply migrations against local Postgres

```bash
cd api-server/migrations

# Create the tracker schema once
psql "postgres://postgres:postgrespassword@localhost:5432/nudgebee?sslmode=disable" \
  -c "CREATE SCHEMA IF NOT EXISTS nudgebee;"

# Apply
migrate -path ./migrations/app \
  -database "postgres://postgres:postgrespassword@localhost:5432/nudgebee?sslmode=disable&x-migrations-table=%22nudgebee%22.%22schema_migrations%22&x-migrations-table-quoted=true" \
  up
```

### Common operations

```bash
# Current applied version
psql "$LOCAL_DB_URL" -c "SELECT version, dirty FROM nudgebee.schema_migrations;"

# Apply only N migrations forward
migrate -path ./migrations/app -database "$DB_URL_WITH_TRACKER" up 1

# Roll back N migrations (runs .down.sql files in reverse)
migrate -path ./migrations/app -database "$DB_URL_WITH_TRACKER" down 1

# Force version (e.g. to recover from dirty=true)
migrate -path ./migrations/app -database "$DB_URL_WITH_TRACKER" force <version>
```

### Apply against dev (read-only check)

```bash
# Read dev's current version
psql "$DEV_APP_DATABASE_URL" -c "SELECT version, dirty FROM nudgebee.schema_migrations;"
```

Don't run `migrate up` against dev manually — the CI job in `nudgebee-infra` (private) does it on merge.

## CI/CD workflows

> **For external contributors:** Nudgebee's hosted SaaS deploys migrations from a private internal-CD repo (`nudgebee-infra`). You don't need access to it. If you're deploying Nudgebee yourself, the [`deploy/kubernetes/nudgebee/`](../../deploy/kubernetes/nudgebee/) Helm chart in this repo runs migrations as a built-in `pre-install,pre-upgrade` Job (see ["How migrations run on deploy"](#how-migrations-run-on-deploy) above) — no separate CI pipeline required. The internal workflows below are documented for transparency only.

The migration build + deploy lives in [`nudgebee-infra`](https://github.com/nudgebee/nudgebee-infra), not this repo:

| Workflow                                     | Trigger                | What it does                                                                  |
| -------------------------------------------- | ---------------------- | ----------------------------------------------------------------------------- |
| `migrations-dev-gke.yaml` (in nudgebee-infra) | push to `main` (paths: `api-server/migrations/**`) | Builds image, pushes to ECR, `helm upgrade --wait-for-jobs` against dev GKE   |
| `migrations-test-gke.yaml`                   | push to `test`         | Same against test cluster                                                     |
| `migrations-prod.yaml`                       | push to `prod`         | Same against prod cluster                                                     |

`--wait-for-jobs` blocks the CI step until the K8s Job completes, so CI failure correctly reflects `migrate up` failure.

## Environment variables

| Variable                 | Description                                                                  |
| ------------------------ | ---------------------------------------------------------------------------- |
| `APP_DATABASE_URL`       | Postgres connection URL — runner appends `x-migrations-table` automatically. |
| `SERVICE_API_SERVER_URL` | API server URL (for the agent-playbook reload step).                         |
| `ACTION_API_SERVER_TOKEN`| Auth token for the API server reload call.                                   |
| `CLICKHOUSE_ENABLED`     | Set `true` to apply Clickhouse migrations.                                   |
| `CLICKHOUSE_HOST`        | Clickhouse host URL.                                                         |
| `CLICKHOUSE_USER`        | Clickhouse username.                                                         |
| `CLICKHOUSE_PASSWORD`    | Clickhouse password.                                                         |
| `RABBIT_MQ_HOST`         | RabbitMQ host.                                                               |
| `RABBIT_MQ_USERNAME`     | RabbitMQ username.                                                           |
| `RABBIT_MQ_PASSWORD`     | RabbitMQ password.                                                           |

## Troubleshooting

**Migration locked (`dirty=true`):**

A migration failed partway. The row is left `dirty=true` and subsequent runs refuse to proceed. Recovery:

```bash
# Identify the version that's stuck
psql "$APP_DATABASE_URL" -c "SELECT version, dirty FROM nudgebee.schema_migrations;"

# Inspect the .up.sql for that version, find what went wrong, fix the data manually if needed.
# Then either:
#   (a) Mark it clean at the previous version, so `up` retries:
migrate -path ./migrations/app -database "$DB_URL_WITH_TRACKER" force <previous_version>
migrate -path ./migrations/app -database "$DB_URL_WITH_TRACKER" up

#   (b) Mark it clean at the failing version, accepting it as fully applied:
migrate -path ./migrations/app -database "$DB_URL_WITH_TRACKER" force <failing_version>
```

**New migration silently not applied:**

Most likely its timestamp is lower than `nudgebee.schema_migrations.version`. Check:

```bash
ls api-server/migrations/migrations/app/ | sort | tail -5    # latest files
psql "$APP_DATABASE_URL" -c "SELECT version FROM nudgebee.schema_migrations;"
```

If the file's leading integer is below the DB version, regenerate the filename with a current timestamp (the SQL inside is fine):

```bash
TS=$(python3 -c "import time; print(int(time.time() * 1000))")
mv 1700000000000_V733_foo.up.sql ${TS}_V733_foo.up.sql
mv 1700000000000_V733_foo.down.sql ${TS}_V733_foo.down.sql
```

**Check applied version on a remote environment:**

```bash
psql "$REMOTE_APP_DATABASE_URL" -c "SELECT version, dirty FROM nudgebee.schema_migrations;"
```

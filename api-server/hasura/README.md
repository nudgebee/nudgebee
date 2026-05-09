# Hasura Migrations

This directory contains all database migrations and metadata for the Nudgebee platform, managed via the [Hasura CLI](https://hasura.io/docs/latest/hasura-cli/overview/).

## Directory Structure

```
api-server/hasura/
├── config.yaml                  # Hasura CLI configuration
├── Dockerfile                   # Migration job image (Hasura CLI + golang-migrate)
├── run-migrations.sh            # Entrypoint: applies all migrations on deploy
├── metadata/                    # Hasura metadata (actions, tables, permissions, cron triggers, etc.)
│   ├── actions.yaml             # Custom action definitions
│   ├── actions.graphql          # GraphQL types for actions
│   ├── cron_triggers.yaml       # Scheduled cron jobs
│   └── databases/app/           # Table & function metadata
└── migrations/
    ├── app/                     # Postgres migrations (main database)
    ├── clickhouse/              # Clickhouse migrations (analytics DB)
    └── rabbitmq/                # RabbitMQ setup scripts
```

## Migration Types

### Postgres (`migrations/app/`)

Each migration is a directory with an `up.sql` (and optionally `down.sql`):

```
migrations/app/{timestamp_ms}_V{N}_{description}/
├── up.sql     # SQL to apply
└── down.sql   # SQL to revert (optional)
```

**Directory naming rules (from CLAUDE.md):**
- Timestamp **must** be the current Unix timestamp in milliseconds — never hardcoded/arbitrary
- Generate with: `python3 -c "import time; print(int(time.time() * 1000))"`
- Version number `V{N}` increments sequentially
- Do **not** use `CREATE INDEX CONCURRENTLY` — Hasura runs migrations inside a transaction and this is unsupported; use plain `CREATE INDEX`

Examples:
```
1774614951697_V655_fix_event_duplicates_fk_cascade/
1774542362209_V698_exclude_credit_spends_from_aggregate/
```

### Clickhouse (`migrations/clickhouse/`)

Plain numbered SQL files applied by [golang-migrate](https://github.com/golang-migrate/migrate):

```
00_db.up.sql
01_create_audit_log_shard.up.sql
02_create_audit_log.up.sql
...
```

Only applied when `CLICKHOUSE_ENABLED=true`.

### RabbitMQ (`migrations/rabbitmq/`)

Shell scripts run sequentially after RabbitMQ is healthy:

```
001_remove_autopilot_queues.sh
```

## How Migrations Run (Deployment)

The `run-migrations.sh` script is the entrypoint for the migration Docker job. It:

1. Runs `hasura deploy` (metadata + migrate in one step)
2. Applies Postgres migrations: `hasura migrate apply --database-name app`
3. Applies Hasura metadata: `hasura metadata apply`
4. Reloads metadata: `hasura metadata reload`
5. Triggers agent playbook reload via API
6. (If `CLICKHOUSE_ENABLED=true`) Applies Clickhouse migrations via `golang-migrate`
7. Checks metadata consistency: `hasura metadata inconsistency status`
8. Waits for RabbitMQ and runs all shell scripts in `migrations/rabbitmq/`

**Tools used in the Docker image:**
- Hasura CLI `v2.33.0`
- golang-migrate `v4.17.0`

## Local Development

### Prerequisites

```bash
# Install Hasura CLI
curl -L https://github.com/hasura/graphql-engine/releases/download/v2.33.0/cli-hasura-linux-amd64 -o /usr/local/bin/hasura
chmod +x /usr/local/bin/hasura

# Port-forward Hasura from dev cluster
kubectl --namespace nudgebee port-forward svc/hasura 8080:80
```

### Apply Migrations Locally

```bash
cd api-server/hasura

# Apply pending Postgres migrations
hasura migrate apply --database-name app

# Apply metadata
hasura metadata apply

# Reload metadata
hasura metadata reload

# Open Hasura console (also auto-tracks schema changes as new migrations)
hasura console
```

### Check Migration Status

```bash
hasura migrate status --database-name app
```

### Squash Migrations (cleanup)

```bash
hasura migrate squash --from <start-version> --to <end-version> --name <description> --database-name app
```

## Creating a New Migration

1. Generate the timestamp:
   ```bash
   python3 -c "import time; print(int(time.time() * 1000))"
   ```

2. Create the migration directory and SQL files:
   ```bash
   TIMESTAMP=$(python3 -c "import time; print(int(time.time() * 1000))")
   VERSION=699   # next sequential version
   NAME=add_my_table

   mkdir -p api-server/hasura/migrations/app/${TIMESTAMP}_V${VERSION}_${NAME}
   touch api-server/hasura/migrations/app/${TIMESTAMP}_V${VERSION}_${NAME}/up.sql
   touch api-server/hasura/migrations/app/${TIMESTAMP}_V${VERSION}_${NAME}/down.sql
   ```

3. Write your SQL in `up.sql` (and optionally `down.sql`).

4. Apply and test against dev:
   ```bash
   cd api-server/hasura
   hasura migrate apply --database-name app \
     --endpoint <dev-hasura-endpoint> \
     --admin-secret <admin-secret>
   ```

5. If you changed Hasura metadata (tables, permissions, actions), apply it too:
   ```bash
   hasura metadata apply
   hasura metadata reload
   ```

> **CI enforces this:** The `migrations-dev-gke` workflow checks that all new migrations in a PR have already been applied to the dev database. PRs will fail if migrations were not tested against dev first.

## Action Naming Convention

When creating Hasura actions (in `metadata/actions.yaml` / `metadata/actions.graphql`), use:

```
<module>_<verb>_<description>_[<version>]
```

| Part | Values |
|------|--------|
| module | `ai`, `runbooks`, `cloud`, `tickets`, `workflows`, etc. |
| verb | `list`, `get`, `create`, `delete`, `enable`, `disable`, `update`, `sync` |
| version | Optional — only for new versions of an existing action (`v2`, `v3`) |

Examples: `ai_get_tools`, `runbooks_create_playbook`, `cloud_sync_accounts`, `ai_get_tools_v2`

CI also validates that there are no duplicate action names via `api-server/scripts/validate_graphql_actions.py`.

## CI/CD Workflows

| Workflow | Trigger | What it does |
|----------|---------|--------------|
| `migrations-dev-gke.yaml` | PR to `main` | Validates no duplicate actions; checks new migrations are applied on dev |
| `migrations-test-gke.yaml` | PR to `test` | Runs migration job against test cluster |
| `migrations-prod.yaml` | PR to `prod` | Runs migration job against prod cluster |

## Environment Variables

| Variable | Description |
|----------|-------------|
| `HASURA_GRAPHQL_ENDPOINT` | Hasura instance URL |
| `HASURA_GRAPHQL_ADMIN_SECRET` | Hasura admin secret |
| `SERVICE_API_SERVER_URL` | API server URL (for playbook reload) |
| `ACTION_API_SERVER_TOKEN` | Auth token for API server |
| `CLICKHOUSE_ENABLED` | Set `true` to apply Clickhouse migrations |
| `CLICKHOUSE_HOST` | Clickhouse host URL |
| `CLICKHOUSE_USER` | Clickhouse username |
| `CLICKHOUSE_PASSWORD` | Clickhouse password |
| `RABBIT_MQ_HOST` | RabbitMQ host |
| `RABBIT_MQ_USERNAME` | RabbitMQ username |
| `RABBIT_MQ_PASSWORD` | RabbitMQ password |

## Troubleshooting

**Migration stuck / inconsistent metadata:**
```bash
hasura metadata inconsistency status
hasura metadata drop   # WARNING: drops all metadata, re-apply after
hasura metadata apply
```

**Migration already applied error:**
```bash
hasura migrate status --database-name app
# Mark a migration as applied without running it:
hasura migrate apply --version <version> --type up --skip-execution --database-name app
```

**Check applied migrations on a remote environment:**
```bash
hasura migrate status --database-name app \
  --endpoint <hasura-endpoint> \
  --admin-secret <admin-secret>
```

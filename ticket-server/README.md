# ticket-server

Go microservice that brokers ticket lifecycle between the Nudgebee platform and external ticketing systems (Jira, ServiceNow, PagerDuty, Zenduty, GitHub, GitLab). Other backend services call its HTTP RPC surface to create, update, fetch, and sync tickets; this service handles the per-provider API call, authentication, and field mapping.

## What it does

```
       ┌─────────────────┐      ┌──────────────────┐
       │ services-server │──┐   │ runbook-server   │
       │ notifications   │  │   │ workflow         │
       │ ...             │  │   └────────┬─────────┘
       └─────────────────┘  │            │
                            ▼            ▼
                     ┌────────────────────────┐    per-tenant integration creds
                     │      ticket-server     │ ◀──── loaded from
                     │   (Go / Gin, HTTP)     │    `integration_config_values` in Postgres
                     └───┬────┬────┬────┬─────┘
                         │    │    │    │
                  ┌──────┘    │    │    └──────┐
                  ▼           ▼    ▼           ▼
                Jira  ServiceNow  PagerDuty  Zenduty / GitHub / GitLab
```

Integration credentials are **not** environment variables — they're stored per-tenant in the database (`integration_config_values`) and resolved at request time. The service itself only needs platform-level secrets (DB, encryption key, GitHub App ID for runbook PRs).

## Prerequisites

- Go 1.22+
- A reachable Postgres (primary DB)
- A reachable ClickHouse only if your changes touch ticket-event analytics; defaults to localhost / fallback values otherwise
- Redis only if `CACHE_PROVIDER=redis` — defaults to in-memory cache

## Quickstart

```bash
cd ticket-server

# Required env (minimum):
export APP_DATABASE_URL='postgresql://<user>:<pass>@localhost:5432/nudgebee?sslmode=disable'
export NUDGEBEE_ENCRYPTION_KEY='<32-byte hex>'        # decrypts stored integration creds

# Resolve deps + run
go mod download                  # Download Go module deps
make run                         # = go run ./cmd (binds to PORT, default 8080)
```

## Build commands

| Command         | What it does                                                          |
|-----------------|-----------------------------------------------------------------------|
| `make fmt`      | Format the code (`gofmt`)                                              |
| `make lint`     | Lint via `golangci-lint`                                               |
| `make test`     | Run tests with coverage + race detector                                |
| `make validate` | `fmt + lint + test` — must pass before `build`                         |
| `make build`    | Compile the binary (runs `validate` first)                             |
| `make install`  | Build + copy the binary to `~/go/bin/nudgebee-ticket-services`         |
| `make run`      | `go run ./cmd`                                                         |

Validation order before pushing: **always** `make validate`.

## API

All routes are mounted under `/tickets` (Gin route group). Common shapes:

| Path                         | Purpose                                                                    |
|------------------------------|----------------------------------------------------------------------------|
| `POST /tickets/create-meta`  | Return the per-tool field schema for ticket creation (priorities, assignees, custom fields) |
| `POST /tickets/create-ticket` | Create a ticket via the tenant's configured integration                    |
| `POST /tickets/get-ticket`   | Fetch a single ticket                                                       |
| `POST /tickets/search`       | Search tickets                                                              |
| `POST /tickets/list`         | List tickets                                                                |
| `POST /tickets/sync-tickets` | Sync tickets from the upstream provider                                     |
| `POST /tickets/rpc/create-ticket` | Service-to-service ticket creation (called by runbook-server, etc.)    |
| `POST /tickets/rpc/get-comments`   | Service-to-service: fetch ticket comments                              |
| `POST /tickets/rpc/add-comment`    | Service-to-service: add a comment                                      |
| `POST /tickets/rpc/acknowledge` / `escalate` / `resolve` / `assign` / `transition` / `update` | State transitions on an existing ticket |
| `POST /tickets/rpc/test-connection` | Validate a candidate integration's credentials                         |

Endpoints prefixed with `/tickets/rpc/` are called by other backend services and require the `X-ACTION-TOKEN` header (value = `ACTION_API_SERVER_TOKEN`).

### Example: create a ticket (service-to-service)

```bash
curl -X POST http://localhost:8080/tickets/rpc/create-ticket \
  -H "X-ACTION-TOKEN: $ACTION_API_SERVER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": "<uuid>",
    "integration_id": "<uuid>",
    "title": "High memory on payments-api",
    "description": "...",
    "severity": "high"
  }'
```

The exact request body depends on the configured integration (Jira's custom fields vs. PagerDuty's urgency vs. ServiceNow's `u_*` columns). Call `/tickets/create-meta` first to discover the schema for a given integration.

## Configuration

Configuration is read via [viper](https://github.com/spf13/viper) — environment variables auto-bind to the keys below. A local `.env` file in the working directory is also picked up.

### Required

| Variable                  | Purpose                                                                |
|---------------------------|------------------------------------------------------------------------|
| `APP_DATABASE_URL`        | Postgres connection string                                              |
| `NUDGEBEE_ENCRYPTION_KEY` | 32-byte hex key — decrypts per-tenant integration credentials at rest   |

### Service-to-service auth

| Variable                          | Default         | Purpose                                                       |
|-----------------------------------|-----------------|---------------------------------------------------------------|
| `ACTION_API_SERVER_TOKEN`         | _empty_         | Shared secret for `/tickets/rpc/*` callers                     |
| `ACTION_API_SERVER_TOKEN_HEADER`  | `X-ACTION-TOKEN`| Header name for that token                                     |
| `SERVICE_API_SERVER_URL`          | `http://services-server:8000` | Upstream services-server URL for callbacks       |

### Runtime

| Variable                  | Default        | Purpose                                              |
|---------------------------|----------------|------------------------------------------------------|
| `PORT`                    | `8080`         | HTTP port                                             |
| `ENV`                     | `production`   | Runtime profile                                       |
| `NUDGEBEE_DB_SSL_ENABLED` | `true`         | Set `false` for local Postgres without TLS            |

### GitHub App (only if using runbook-driven GitHub PRs)

| Variable              | Purpose                                          |
|-----------------------|--------------------------------------------------|
| `GITHUB_APP_ID`       | GitHub App ID                                     |
| `GITHUB_PRIVATE_KEY`  | GitHub App private key (PEM)                      |

### ClickHouse (analytics — optional)

| Variable              | Default                  |
|-----------------------|--------------------------|
| `CLICKHOUSE_HOST`     | `http://localhost:8123`  |
| `CLICKHOUSE_USER`     | `default`                |
| `CLICKHOUSE_PASSWORD` | `default`                |
| `CLICKHOUSE_DATABASE` | `nudgebee`               |

### Cache (`/tickets/create-meta` field-schema cache)

| Variable                       | Default         | Purpose                                          |
|--------------------------------|-----------------|--------------------------------------------------|
| `CACHE_PROVIDER`               | `in_memory`     | `in_memory` or `redis`                            |
| `CACHE_EXPIRATION_MINUTES`     | `30`            | TTL                                               |
| `CACHE_INMEMORY_SIZE_MB`       | `20`            | (in-memory only) max cache size                   |
| `CACHE_INMEMORY_MAX_ENTRIES`   | `1000`          | (in-memory only) max entries                      |
| `REDIS_SERVER_HOST`            | `redis`         | (redis only)                                      |
| `REDIS_SERVER_PORT`            | `6379`          | (redis only)                                      |
| `REDIS_USER_NAME`              | _empty_         | (redis only)                                      |
| `REDIS_USER_PASSWORD`          | _empty_         | (redis only)                                      |

### Other

| Variable                | Default                          | Purpose                                  |
|-------------------------|----------------------------------|------------------------------------------|
| `ETL_SERVER_ENDPOINT`   | `http://localhost:5000`          | ETL pipeline URL                         |
| `ETL_SERVER_TOKEN`      | _empty_                          | ETL auth token                           |
| `ML_SERVICE_URL`        | `http://localhost:9000`          | ML inference URL                         |
| `GPT_TOKEN` / `OPENAI_ENDPOINT` / `GPT_MODEL` | `default` / `https://api.openai.com/v1` / `gpt-3.5-turbo` | LLM-assisted ticket enrichment |
| `OTEL_*`                | various                          | OpenTelemetry tracing — see `otel.go`     |

For the complete list, see [`common/config.go`](common/config.go).

## Module structure

```
ticket-server/
├── routes/        # Gin route registration
├── controllers/   # HTTP handlers
├── services/      # Business logic; `services/tools/` holds per-integration adapters
│                  # (jira, servicenow, pagerduty, zenduty, github, gitlab);
│                  # `services/cache/` is the create-meta field-schema cache
├── clients/       # Lower-level HTTP/SDK clients per provider
├── database/      # Postgres connection + repository helpers
├── models/        # DB models + request/response DTOs
├── common/        # Shared config (viper), HTTP helpers, errors, secrets
└── utils/         # Validation, formatting, integration-specific parsers
```

## Conventions

- **Code style:** standard `gofmt`, enforced by `make fmt`.
- **Linting:** `golangci-lint`.
- **Testing:** standard Go testing library; tests alongside source (`foo_test.go`).
- **Integration credentials:** never stored in env vars — encrypted at rest in Postgres (`integration_config_values`), decrypted using `NUDGEBEE_ENCRYPTION_KEY` at request time.
- **Severity / urgency mapping:** each provider uses its own taxonomy. The canonical Nudgebee severity ↔ per-provider mapping lives in [`services/tools/`](services/tools/) and is the source of truth — don't replicate the mapping anywhere else.

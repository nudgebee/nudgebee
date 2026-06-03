# Runbook Server

A backend service that runs **operational workflows** (runbooks) on top of [Temporal.io](https://temporal.io).
You describe a workflow in YAML or JSON, the server stores it, and it gets executed reliably — with retries, scheduling, pause/resume, versioning, and a full audit trail.

It is multi-tenant, talks to a handful of other Nudgebee services (LLM, cloud collector, ticket, notifications, relay), and ships with a Gin HTTP API and an auto-generated Swagger UI.

---

## Table of contents

1. [Quick start (5 minutes)](#quick-start-5-minutes)
2. [Glossary](#glossary)
3. [What this service does](#what-this-service-does)
4. [High-level architecture](#high-level-architecture)
5. [Repository layout](#repository-layout)
6. [Core concepts](#core-concepts)
7. [Running locally](#running-locally)
8. [Make targets](#make-targets)
9. [Configuration reference](#configuration-reference)
10. [HTTP API](#http-api)
11. [Authentication](#authentication)
12. [Task framework](#task-framework)
13. [Workers and background processes](#workers-and-background-processes)
14. [Observability](#observability)
15. [Testing](#testing)
16. [OpenAPI / Swagger](#openapi--swagger)
17. [Deployment notes](#deployment-notes)
18. [Troubleshooting](#troubleshooting)
19. [Related docs](#related-docs)

---

## Quick start (5 minutes)

You need: Go 1.26+, Docker, and `golangci-lint`.

```bash
# 1. start PostgreSQL + Temporal + Temporal UI in Docker
make start-local-env

# 2. run the server (binds to :8000 by default)
make run
```

Browse the Swagger UI at <http://localhost:8000/swagger/index.html> to see every endpoint and try requests interactively, and watch executions in the Temporal UI at <http://localhost:8080>.

Want more? Jump to [Core concepts](#core-concepts), the [HTTP API](#http-api) reference, or the [Task framework](#task-framework).

---

## Glossary

| Term | Meaning |
|---|---|
| **Workflow** | A runbook: a named, versioned definition of inputs, triggers, and tasks. Stored in Postgres. |
| **Task** | One step inside a workflow (run a script, call an API, apply YAML to K8s, etc.). Each task has a `type` and `params`. |
| **Trigger** | What causes a workflow to run: `manual`, `schedule` (cron), `webhook`, or `event` (RabbitMQ). |
| **Execution** | One run of a workflow. Has its own ID, status, inputs, outputs, and history. |
| **Live version** | The version of a workflow that is actually executed when triggered. Old versions stay around for history and rollback. |
| **Dry-run** | Runs the workflow on Temporal with a `_dry_run=true` flag passed to every task. The workflow really executes — each task's `Execute()` is invoked — but persistence is skipped (no execution row written, no `workflow_last_execution_time` update) and *dry-run-aware* tasks (a handful of K8s right-sizing tasks plus `core.call_workflow`) short-circuit their side effects. Tasks that don't honor the flag (most of them — HTTP calls, scripts, ticket creates, notifications, DB queries, cloud SDK calls) will still hit external systems. Treat it as "trace what the workflow would do," not "safe sandbox." See `internal/workflow/executor.go:1609`. |
| **Temporal** | The workflow engine underneath. It guarantees that tasks finish even across process restarts. |
| **Integration** | A named external system (a Slack channel, a Jira instance, a webhook source). Workflows subscribe to integrations through their triggers. |

---

## What this service does

In one paragraph:

> Operators (or LLM agents) author runbooks as YAML. The Runbook Server validates them, versions them, and runs them on Temporal. A run can call shell scripts, hit cloud APIs (AWS / Azure / GCP), apply Kubernetes manifests, query databases, ask the LLM to investigate an alert, raise tickets, send notifications, and more. Schedules, webhooks, and RabbitMQ events all kick off runs. Everything is scoped to a tenant + account.

---

## High-level architecture

```
                    ┌──────────────────────────────────────────┐
                    │              runbook-server               │
                    │   (one Go process, default port 8000)    │
                    │                                          │
   HTTP clients ───►│  Gin HTTP API                            │
   (UI, agents,     │   /workflows, /configs, /tasks, /rpc,    │
    services)       │   /webhook, /approvals, /health, swagger │
                    │                                          │
   RabbitMQ ──────► │  Event consumers                         │
   exchanges        │   - workflow event trigger consumer      │
                    │   - LLM investigation completion consumer│
                    │                                          │
                    │  Background loops                        │
                    │   - event-registry sync (default 30s)    │
                    │   - optimizer poller   (default 180s)    │
                    │                                          │
                    │  Temporal workers (gRPC to Temporal)     │
                    │   - main executor  (queue: runbook-tasks)│
                    │   - system worker  (queue: system)       │
                    │   - optimizer worker (when enabled)      │
                    └──────────────────────────────────────────┘
                                  │            │
                                  ▼            ▼
                        ┌──────────────┐  ┌────────────────────┐
                        │  PostgreSQL  │  │      Temporal      │
                        │ (metadata)   │  │ (workflow runtime) │
                        └──────────────┘  └────────────────────┘

External services it calls out to:
  llm-server, ml-k8s-server, relay-server, ticket-server,
  services-server, notifications-server, cloud-collector
```

One process runs everything: HTTP, workers, consumers, pollers. They all start together in `cmd/main.go` and shut down together on SIGINT / SIGTERM (with a 5-second drain).

---

## Repository layout

| Directory | What lives here |
|---|---|
| `cmd/` | Entry point. `main.go` wires up logging, OTEL, DB, Temporal client, workers, consumers, and the HTTP server. |
| `api/` | Gin handlers — workflows, configs, tasks, webhooks, approvals, RPC, swagger. |
| `config/` | Viper-based config loader. All env vars and their defaults live in `config.go`. |
| `common/` | Small shared utilities: cache, encoding, errors, http client, mq helper, rdbms, secrets, time, validation, xml. |
| `internal/model/` | Domain structs: `Workflow`, `WorkflowDefinition`, `WorkflowExecution`, `Task`, `Trigger`, `Config`, `AutoOptimize`. |
| `internal/workflow/` | Temporal workflow + activity definitions, parser, DAG validator, templating, executor, switch routing, dry-run. |
| `internal/storage/` | DAOs: workflow, config, optimizer. PostgreSQL via `sqlx`. |
| `internal/tasks/` | The plug-in task framework — 19 categories of built-in tasks (see [Task framework](#task-framework)). |
| `internal/events/` | RabbitMQ consumers, event registry, recommendation poller. |
| `internal/system/` | System worker logic (cleanup, cron webhook handlers). |
| `services/` | Clients and small services: audit, cloud, config, integrations, llm, ml, notification, optimizer, relay, security, service, ticket. |
| `docs/` | `TRIGGERS.md` plus auto-generated Swagger output. |
| `tests/integration/` | Integration tests. Need a running local env (`make start-local-env`). |

---

## Core concepts

### Workflow

A workflow has:

- **Identity**: `id`, `name`, `tenant_id`, `account_id`, `status` (`ACTIVE` / `INACTIVE` / `PAUSED` / `DRAFT` — see `internal/model/workflow.go:147`), tags.
- **Definition**: inputs, triggers, tasks, hooks, output, retry policy, timeout, layout (for the UI canvas).
- **Versioning**: each update creates a new version. The **live version** is the one that runs when the workflow is triggered. Older versions can be inspected and restored.
- **Audit fields**: `created_by`, `updated_by`, `created_at`, `updated_at`, `created_from_session_id` (when an LLM agent generated the workflow).

### Task

A task is one step. Every task has:

- `id` — unique inside the workflow.
- `type` — like `core.http`, `scripting.run_script`, `k8s.apply`, `llm.summary`.
- `params` — task-specific inputs. Values can use Jinja-style templates that reference workflow inputs and previous task outputs.
- Optional: `condition` (`run_if` expression), `retry_policy`, `failure_policy`, `timeout`, `layout`.

A task's output is always a `map[string]any`. Plain string output is wrapped as `{"data": "..."}`.

### Trigger

| Type | When it fires | Notable params |
|---|---|---|
| `manual` | A user or service calls `POST /workflows/:id/trigger`. | None. |
| `schedule` | Cron-driven, via Temporal Schedules. | `cron`, `overlap_policy`, `catchup_window`. |
| `webhook` | An incoming `POST /webhook/...` request. | `integration_name`. |
| `event` | A message arrives on the RabbitMQ event exchange. | event matcher. |

Full details: [`docs/TRIGGERS.md`](docs/TRIGGERS.md).

### Execution

| Status | Meaning |
|---|---|
| `PENDING` | Accepted, not yet started. |
| `RUNNING` | Currently executing on a Temporal worker. |
| `SUCCEEDED` | All tasks finished successfully. |
| `FAILED` | A task failed and no recovery succeeded. |
| `CANCELLED` | User cancelled the run. |
| `PAUSED` | A scheduled workflow is paused (no new runs start). |

You can **dry-run** a definition (see the [Glossary](#glossary) — it really executes on Temporal, only the persistence and a handful of dry-run-aware tasks short-circuit), **retrigger** an old execution with new inputs, **cancel** a running one, and **pause / resume** scheduled workflows.

### Multi-tenancy

Every record carries `TenantID` and `AccountID`. Every API call must supply them via headers (see [Authentication](#authentication)). The server never returns data across tenants.

### Config and secrets

`/configs` stores key/value config per tenant or per account. Secret values are encrypted at rest with `nudgebee_encryption_key` and auto-decrypted when read.

### Example workflows

The minimal shape — `name` + `definition.triggers` + `definition.tasks`:

```yaml
name: timeout-example
definition:
  triggers:
    - type: manual
  tasks:
    - id: timeout-task
      type: scripting.run_script
      timeout: "1s"
      params:
        script: "sleep 3"
```

For richer examples covering every supported feature — conditionals, child workflows, event triggers, approvals, foreach loops, persistent state, compression, AI router, etc. — see [`tests/integration/testdata/`](tests/integration/testdata/). Each file is a working end-to-end fixture used by the integration test suite, so the structure is guaranteed to parse and run.

Notable examples to start from:

| File | Demonstrates |
|---|---|
| `test-conditional-true-workflow.yaml` | `if:` conditional task execution |
| `test-event-workflow.yaml` | Event trigger + payload template variables |
| `test-approval-workflow.yaml` | Approval task + external signal |
| `test-foreach-workflow.yaml` | Iterating over a list |
| `test-child-workflow.yaml` | Calling one workflow from another |
| `test-failure-handling-workflow.yaml` | `on_failure` and retry semantics |

For trigger-config details (schedule, webhook), see [`docs/TRIGGERS.md`](docs/TRIGGERS.md).

---

## Running locally

### Prerequisites

- Go **1.26+**
- Docker + Docker Compose
- `golangci-lint`
- Optional: `swag` (`go install github.com/swaggo/swag/cmd/swag@latest`) — only needed to regenerate the OpenAPI spec.

### Steps

```bash
# 1. start dependencies (Postgres 15, Temporal, Temporal UI, setup job)
make start-local-env

# 2. (optional) create a .env file in the repo root.
#    The defaults in config/config.go cover most things; you mostly only
#    need to set secrets / external URLs for whatever you plan to call.
cat > .env <<'EOF'
API_PORT=8000
LOG_LEVEL=info
AUTO_PILOT_DATABASE_URL=postgres://temporal:temporal@localhost:5432/temporal?sslmode=disable
TEMPORAL_GRPC_ENDPOINT=localhost:7233
ACTION_API_SERVER_TOKEN=  # outbound only; set if your upstream services-server requires it
NUDGEBEE_ENCRYPTION_KEY=  # set in real envs; empty disables decryption
EOF

# 3. run the server
make run
# → listens on http://localhost:8000
# → Temporal UI is at http://localhost:8080

# stop everything
make stop-local-env
```

> **Heads up:** the default API port is **8000**. Port **8080** belongs to the Temporal UI, not this server.

---

## Make targets

| Target | What it does |
|---|---|
| `make run` | `go run cmd/main.go` — boots everything in one process. |
| `make fmt` | `go fmt ./...` |
| `make lint` | `golangci-lint run ./...` |
| `make test` | `go test ./...` |
| `make validate` | `lint` then `test`. Run before pushing. |
| `make build` | Builds the binary to `dist/runbook-server`. |
| `make install` | `go install ./cmd/main.go` |
| `make swag` | Regenerates the OpenAPI spec into `docs/swagger/`. Requires `swag` installed. |
| `make start-local-env` | `docker compose -f docker-compose.test.yml up -d postgresql temporal temporal-ui temporal-setup-job` |
| `make stop-local-env` | Tears the local env down with `--remove-orphans -v` (wipes volumes). |

---

## Configuration reference

All config is read by Viper from environment variables (and an optional `.env` file in the working directory). Keys are case-insensitive; `.` is mapped to `_`. Source of truth: [`config/config.go`](config/config.go).

### HTTP / process

| Env var | Default | Purpose |
|---|---|---|
| `API_PORT` | `8000` | Port the HTTP API listens on. |
| `LOG_LEVEL` | `info` | One of `debug`, `info`, `warn`, `error`. |
| `runbook_server_name` | — | Optional name tag for logs. |

### Database (PostgreSQL)

| Env var | Default | Purpose |
|---|---|---|
| `auto_pilot_database_url` | `postgres://temporal:temporal@localhost:5432/temporal?sslmode=disable` | Postgres DSN. Required in real envs. |
| `runbook_server_db_max_connection` | `10` | Pool max size. |
| `runbook_server_db_min_connection` | `1` | Pool min size. |
| `runbook_server_db_idle_minutes` | `10` | Idle connection TTL. |

### Temporal

| Env var | Default | Purpose |
|---|---|---|
| `TEMPORAL_GRPC_ENDPOINT` | `localhost:7233` | Temporal frontend gRPC. |
| `runbook_server_temporal_queue` | `runbook-tasks` | Task queue for the main executor worker. |

### RabbitMQ

| Env var | Default | Purpose |
|---|---|---|
| `rabbit_mq_host` | `localhost` | |
| `rabbit_mq_port` | `5672` | |
| `rabbit_mq_username` | `user` | |
| `rabbit_mq_password` | `password` | |
| `rabbit_mq_runbook_event_exchange` | `runbook_event_process` | Inbound exchange for event triggers. |
| `rabbit_mq_runbook_event_queue` | `runbook_event_process` | |
| `rabbit_mq_runbook_event_routing_key` | `troubleshooting` | |
| `rabbit_mq_troubleshoot_exchange` | `llm_server_event_investigate` | Outbound: `llm.event_investigate` task publishes here. |
| `rabbit_mq_troubleshoot_routing_key` | `llm_server_event_investigate` | |
| `rabbit_mq_event_investigate_completed_exchange` | `llm_server_event_investigate_completed` | Inbound: LLM publishes here when an investigation finishes. |
| `rabbit_mq_event_investigate_completed_queue` | `runbook_server_event_investigate_completed` | |
| `rabbit_mq_event_investigate_completed_routing_key` | `llm_server_event_investigate_completed` | |

### External services

| Env var | Default | Purpose |
|---|---|---|
| `service_api_server_url` | `http://services-server:8000` | Main services backend. |
| `service_api_server_timeout_seconds` | `10` | |
| `relay_server_endpoint` | `http://localhost:52832` | Relay (runs scripts on agents). |
| `relay_server_secret_key` | `default` | |
| `ticket_service_url` | `http://localhost:9097` | |
| `llm_server_url` | `http://localhost:8000` | |
| `ml_server_url` | `http://ml-k8s-server:8000` | |
| `notification_service_url` | `http://notifications:8080` | |
| `cloud_collector_server_url` | `http://localhost:8000` | |
| `cloud_collector_server_token` | — | |

### Auth and crypto

| Env var | Default | Purpose |
|---|---|---|
| `action_api_server_token` | — | **Outbound only.** Value runbook-server attaches as `X-ACTION-TOKEN` when calling upstream backends (services-server, integrations, audit). Not validated on inbound requests. |
| `action_api_server_token_header` | `X-ACTION-TOKEN` | Header name for the outbound token above. |
| `llm_server_token` | — | Token used when calling the LLM server. |
| `llm_server_token_header` | `X-ACTION-TOKEN` | |
| `nudgebee_encryption_key` | — | Symmetric key used to encrypt secret configs. |
| `approval_signing_key` | — | Signs approval URLs sent for human-in-the-loop tasks. |

### Optimization

| Env var | Default | Purpose |
|---|---|---|
| `runbook_server_optimization_enabled` | `true` | Toggles the optimizer worker and poller. |
| `runbook_server_optimization_poll_interval_seconds` | `180` | How often to look for new optimization recommendations. |

### Cache

| Env var | Default | Purpose |
|---|---|---|
| `cache_provider` | `in_memory` | `in_memory` or `redis`. |
| `cache_expiration_minutes` | `30` | |
| `cache_inmemory_size_mb` | `20` | |
| `cache_inmemory_max_entries` | `1000` | |
| `redis_server_host` | — | Required if `cache_provider=redis`. |
| `redis_server_port` | — | |
| `redis_user_name` | — | |
| `redis_user_password` | — | |

### Observability (OpenTelemetry)

| Env var | Default | Purpose |
|---|---|---|
| `otel_service_name` | `runbook-server` | |
| `otel_traces_exporter` | `noop` | `noop`, `console`, or `otlp`. |
| `otel_exporter_otlp_traces_endpoint` | — | OTLP gRPC endpoint for traces. |
| `otel_metrics_exporter` | — | Same set of values. |
| `otel_exporter_otlp_metrics_endpoint` | — | |
| `otel_exporter_otlp_endpoint` | `127.0.0.1:4317` | Fallback OTLP endpoint. |
| `otel_grpc_timeout_seconds` | `5` | |
| `otel_grpc_max_msg_size` | `8388608` (8 MB) | |

### Miscellaneous

| Env var | Default | Purpose |
|---|---|---|
| `nudgebee_namespace` | `nudgebee` | K8s namespace. Auto-detected from `/var/run/secrets/kubernetes.io/serviceaccount/namespace` when running inside a pod. |
| `runbook_server_event_sync_interval_seconds` | `30` | Event registry refresh interval. |
| `runbook_server_task_scripting_mode` | `agent` | How scripting tasks are dispatched. |
| `runbook_server_relay_command_execution_timeout_seconds` | `120` | |
| `runbook_server_relay_pod_execution_timeout_seconds` | `120` | |
| `runbook_server_llm_retry_attempts` | `180` | |
| `runbook_server_llm_initial_backoff_seconds` | `5` | |
| `server_heartbeat_frequency_second` | `15` | Worker heartbeat frequency. |
| `server_heartbeat_timeout_second` | `30` | |
| `llm_server_tool_shell_image` | `ghcr.io/nudgebee/nudgebee-debug:0.3.10` | Image for shell tasks. |
| `script_executor_node_image` | `node:22-alpine` | |
| `script_executor_powershell_image` | `mcr.microsoft.com/powershell:lts-alpine-3.17` | |

---

## HTTP API

All routes from [`api/server.go`](api/server.go). Most endpoints expect tenant headers (`x-tenant-id`, `x-user-id`, plus `account_id` in the body for the `/rpc` action) so the server can build a `SecurityContext`. There is **no inbound token middleware** — runbook-server trusts whatever is in front of it (the upstream gateway / app layer). See [Authentication](#authentication) for the full story. `/health` and `/swagger/*` need no headers.

### Workflows

| Method | Path | What it does |
|---|---|---|
| `POST` | `/workflows` | Create a workflow. |
| `GET` | `/workflows` | List workflows for the account. |
| `GET` | `/workflows/:id` | Get a workflow's current (live) definition. |
| `GET` | `/workflows/:id/state` | Lightweight status snapshot. |
| `PUT` | `/workflows/:id` | Update a workflow (creates a new version). |
| `DELETE` | `/workflows/:id` | Delete a workflow. |
| `POST` | `/workflows/:id/trigger` | Manually start a run. |
| `POST` | `/workflows/:id/pause` | Pause schedules. |
| `POST` | `/workflows/:id/resume` | Resume schedules. |
| `POST` | `/workflows/validate` | Validate a definition without saving. |
| `POST` | `/workflows/dry-run` | Execute on Temporal with `_dry_run=true` on every task. Persistence is skipped; tasks that aren't dry-run-aware will still hit external systems. See the [Glossary](#glossary) entry. |

### Workflow executions

Both `/runs/*` and `/executions/*` are accepted (aliases of each other).

| Method | Path | What it does |
|---|---|---|
| `GET` | `/workflows/:id/runs` | List executions. |
| `GET` | `/workflows/:id/runs/:execution_id` | Get one execution (status, history, output). |
| `PUT` | `/workflows/:id/runs/:execution_id` | Send a signal / update inputs to a running execution. |
| `POST` | `/workflows/:id/runs/:execution_id/cancel` | Cancel it. |
| `POST` | `/workflows/:id/runs/:execution_id/retrigger` | Re-run with the same or new inputs. |

### Workflow versions

| Method | Path | What it does |
|---|---|---|
| `GET` | `/workflows/:id/versions` | List versions. |
| `GET` | `/workflows/:id/versions/:version_number` | Get a specific version. |
| `PATCH` | `/workflows/:id/versions/:version_number` | Update version metadata (e.g. notes). |
| `POST` | `/workflows/:id/versions/:version_number/restore` | Restore an old version (becomes a new version on top). |
| `POST` | `/workflows/:id/publish` | Publish the current draft as a new version. |
| `POST` | `/workflows/:id/versions/:version_number/make-live` | Point the live pointer at that version. |

### Configs

| Method | Path | What it does |
|---|---|---|
| `POST` | `/configs` | Save a config value (regular or secret). |
| `GET` | `/configs` | List configs. |
| `GET` | `/configs/:key` | Get one (secrets are decrypted with `nudgebee_encryption_key`). |
| `DELETE` | `/configs/:key` | Delete one. |

### Webhooks

| Method | Path | What it does |
|---|---|---|
| `POST` | `/webhook/:workflowId` | Trigger one specific workflow with the body as input. |
| `POST` | `/webhook/by-integration/:integrationName` | Fan out: trigger every active workflow whose webhook trigger is bound to that integration. |

### Approvals

| Method | Path | What it does |
|---|---|---|
| `POST` | `/approvals/:token` | Resolves a paused approval task. The token is signed with `approval_signing_key`. |

### Tasks

| Method | Path | What it does |
|---|---|---|
| `GET` | `/tasks` | List every registered task type with its input schema. |
| `POST` | `/tasks/:task_type/execute` | Run a single task standalone (no workflow context). |

### Templating

| Method | Path | What it does |
|---|---|---|
| `GET` | `/templating/functions` | List filters/functions usable inside templated `params`. |

### RPC

| Method | Path | What it does |
|---|---|---|
| `POST` | `/rpc` | Service-to-service action gateway used by the rest of the platform. The body's `action` field selects the handler (workflow_create, workflow_list, workflow_execute, …). |

### System

| Method | Path | What it does |
|---|---|---|
| `GET` | `/health` | Liveness probe. Returns `{"status":"ok"}`. Does **not** check Temporal/Postgres/RabbitMQ. |
| `GET` | `/swagger/*any` | Swagger UI. |
| `GET` | `/openapi.json` | Raw OpenAPI 2.0 spec. |

---

runbook-server does **not** enforce a token on inbound requests. Grep confirms `ServiceApiServerToken` is read in `config/config.go` and only **set as an outbound header** when this service calls upstream backends (`services/audit/service.go`, `services/security/security_context.go`, `services/integrations/service.go`, `services/service/service.go`). No middleware in `api/server.go` or `api/handlers.go` validates an inbound `X-ACTION-TOKEN`. Trust comes from whatever sits in front of runbook-server (the app gateway, k8s NetworkPolicy, etc.) — deploy accordingly.

What the server *does* read from inbound requests, in [`api/common.go`](api/common.go):

- **Direct REST handlers** (`/workflows`, `/configs`, `/webhook`, etc.) call `buildContextFromRequestPayload` and read `x-tenant-id` + `x-user-id` from headers (falling back to the request body for `tenant_id` / `user_id` / `account_id` if headers are absent). The tenant id is required; the user id defaults to a system user.
- **`/rpc` action handler** calls `buildContextFromPayload` and reads tenant / user / role from the JSON body's `session_variables` (Hasura-style envelope) — falling back to the same `x-tenant-id` / `x-user-id` headers.
- The resulting `SecurityContext` carries roles (`super_admin` / `tenant_admin` / `account_admin` / `account_read_admin`) that handlers consult before touching data via `HasAccountAccess` / `HasTenantAccess`.

`/health` and `/swagger/*` read nothing. Approval URLs handed to humans carry an HMAC-signed token (`approval_signing_key`) — that signature is the only auth on `POST /approvals/:token`.

> **Heads up:** if you expose runbook-server directly to the internet without an auth-enforcing gateway, anyone who can set `x-tenant-id` / `x-user-id` can act as any tenant. This is by design for the in-cluster deployment model; do not change it without a corresponding middleware.

---

## Task framework

A task implements the small interface in `internal/tasks/types/`. It returns `map[string]any` so downstream tasks can reference its fields via templates. New tasks are wired up in [`internal/tasks/registry.go`](internal/tasks/registry.go), and they then show up automatically on `GET /tasks`.

Built-in task categories:

| Category | Folder | What it covers |
|---|---|---|
| `ai` | `internal/tasks/ai` | LLM calls and AI-driven helpers. |
| `aws` | `internal/tasks/aws` | AWS SDK actions. |
| `azure` | `internal/tasks/azure` | Azure SDK actions. |
| `cicd` | `internal/tasks/cicd` | CI/CD provider integrations. |
| `cloud` | `internal/tasks/cloud` | Cross-cloud / generic cloud ops. |
| `core` | `internal/tasks/core` | Building blocks: HTTP, switch, foreach, wait, set-variable, call-workflow, etc. |
| `crypto` | `internal/tasks/crypto` | Hashing, signing, encoding. |
| `data` | `internal/tasks/data` | Transformations on collections / JSON. |
| `dbms` | `internal/tasks/dbms` | Database queries. |
| `events` | `internal/tasks/events` | Emit / consume events. |
| `gcp` | `internal/tasks/gcp` | Google Cloud SDK actions. |
| `integrations` | `internal/tasks/integrations` | Third-party integrations. |
| `k8s` | `internal/tasks/k8s` | `kubectl`, helm, pod exec, manifest apply. |
| `mq` | `internal/tasks/mq` | Message-queue producers/consumers. |
| `network` | `internal/tasks/network` | DNS, TCP probes, etc. |
| `notifications` | `internal/tasks/notifications` | Send alerts via the notifications service. |
| `observability` | `internal/tasks/observability` | Query metrics / logs / traces. |
| `scm` | `internal/tasks/scm` | Source control providers (GitHub, GitLab, etc.). |
| `scripting` | `internal/tasks/scripting` | Run shell / Node / PowerShell scripts. |
| `system` | `internal/tasks/system` | Internal house-keeping. |
| `tickets` | `internal/tasks/tickets` | Create / update tickets. |

Use `GET /tasks` to see the live list, parameter schemas, and descriptions.

---

## Workers and background processes

When `cmd/main.go` boots, these all start in the same process:

| Component | Purpose | Trigger / queue |
|---|---|---|
| Gin HTTP server | Serves the API. | Port `API_PORT` (default `8000`). |
| Main Temporal worker | Runs user workflows and their activities. | Temporal task queue `runbook_server_temporal_queue` (default `runbook-tasks`). |
| System Temporal worker | House-keeping workflows (cron-driven cleanup, webhooks). | Task queue `system`. |
| Optimizer Temporal worker | Executes auto-rightsizing / optimization workflows. | Starts only if `runbook_server_optimization_enabled=true`. |
| Workflow event consumer | Triggers workflows whose `event` trigger matches an inbound message. | RabbitMQ exchange `rabbit_mq_runbook_event_exchange`. |
| LLM investigation completion consumer | Resumes a paused activity when the LLM server publishes a result. | RabbitMQ exchange `rabbit_mq_event_investigate_completed_exchange`. |
| Event-registry sync loop | Refreshes the in-memory registry of integrations / event subscriptions. | Every `runbook_server_event_sync_interval_seconds` (default 30s). |
| Recommendation poller | Pulls optimization recommendations and turns them into workflow runs. | Every `runbook_server_optimization_poll_interval_seconds` (default 180s). |

On SIGINT / SIGTERM the process cancels its root context (stops loops + workers) and gives the HTTP server 5 seconds to drain.

---

## Observability

- **Tracing & metrics**: OpenTelemetry. Pick an exporter via `otel_traces_exporter` / `otel_metrics_exporter` (`noop` / `console` / `otlp`). HTTP requests are auto-instrumented via `otelgin`. Temporal client calls are traced too.
- **Structured logs**: `slog` JSON to stdout. Level via `LOG_LEVEL`. HTTP request logs go through `slog-gin` and skip `/health`.
- **Temporal UI**: when using `make start-local-env`, browse to <http://localhost:8080> to see workflow histories, search attributes, and pending activities.
- **Liveness**: `GET /health` returns `{"status":"ok"}` if the process is up. It does **not** verify Temporal / Postgres / RabbitMQ connectivity, so wire it to a K8s liveness probe, not a readiness one.

---

## Testing

```bash
# unit tests (fast, no infra needed for most)
go test ./...

# integration tests (need local Postgres + Temporal)
make start-local-env
RUN_INTEGRATION_TESTS=true go test ./tests/integration/...
```

Integration tests live under [`tests/integration/`](tests/integration/) and cover: workflow CRUD, versioning, dry-run, triggers (manual/schedule/webhook/event), input validation, container/K8s tasks, relay tasks, optimizer flows, async LLM tasks, the `/rpc` action endpoint, and more.

---

## OpenAPI / Swagger

The spec is generated from godoc-style annotations on handlers.

```bash
# install once
go install github.com/swaggo/swag/cmd/swag@latest

# regenerate after changing handlers
make swag
```

Output lands in `docs/swagger/`. At runtime, browse it at <http://localhost:8000/swagger/index.html> (raw JSON at `/openapi.json`).

---

## Deployment notes

- The [`Dockerfile`](Dockerfile) is multi-stage and Alpine-based. The final image bundles `iputils`, `ca-certificates`, `nodejs`, and PowerShell 7.4 so scripting tasks can run inside the container.
- When running in Kubernetes, the process auto-detects the namespace from `/var/run/secrets/kubernetes.io/serviceaccount/namespace` and overwrites `nudgebee_namespace`.
- The first run in a new Temporal namespace needs the Temporal **search attributes** registered (`nb_tenant_id`, `nb_account_id`, `nb_workflow_id`, etc.). The local `temporal-setup-job` from `docker-compose.test.yml` does this for dev; production clusters need an equivalent one-shot job.
- The HTTP server only needs port `API_PORT` exposed. Everything else (Temporal, RabbitMQ, Postgres) is outbound.

---

## Troubleshooting

| Symptom | Likely cause | Where to look |
|---|---|---|
| Server starts but workflows never run | Workers never connected to Temporal. | Logs for `Failed to start worker`, check `TEMPORAL_GRPC_ENDPOINT`. |
| `/webhook/by-integration/:name` returns 404 | No active workflow has a webhook trigger with that `integration_name`. | `GET /workflows` and inspect each trigger config. |
| Secret config comes back as gibberish | `nudgebee_encryption_key` is unset or different from when the secret was written. | `config/config.go`, env. |
| Schedule never fires | Temporal search attributes weren't registered. | Run the `temporal-setup-job`; check Temporal UI → Search Attributes. |
| `/rpc` returns 401 | `buildContextFromPayload` couldn't resolve a tenant — neither headers nor `session_variables.tenant_id` nor the body's `tenant_id` were set. | Make sure the caller sets `x-tenant-id` / `x-user-id` headers or a Hasura-style `session_variables` block. |
| Can't reach the API on `:8080` | Wrong port — that's the Temporal UI. | Use `:8000` (or whatever `API_PORT` is set to). |
| RabbitMQ consumer never receives anything | Wrong exchange / queue / routing key, or the producer service hasn't started. | The defaults are in [`config/config.go`](config/config.go); both ends must match. |
| `make swag` fails | `swag` CLI not installed. | `go install github.com/swaggo/swag/cmd/swag@latest`. |

---

## Related docs

- [`docs/TRIGGERS.md`](docs/TRIGGERS.md) — full reference for schedule, webhook, manual, and event triggers, plus the runtime variables they expose (`workflow_execution_time`, `workflow_scheduled_time`, `workflow_last_execution_time`, `workflow_execution_id`, `workflow_id`, `workflow_name`).
- [`docs/swagger/`](docs/swagger/) — generated OpenAPI 2.0 spec.
- [`config/config.go`](config/config.go) — authoritative list of every env var and its default.
- [`api/server.go`](api/server.go) — authoritative list of HTTP routes.
- [`internal/tasks/registry.go`](internal/tasks/registry.go) — authoritative list of registered task types.

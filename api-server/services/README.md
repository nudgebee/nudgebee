# api-server / services

The **services-server** is Nudgebee's core Go backend — a single binary that exposes the tenant / account / recommendation / integration HTTP RPC surface the frontend and other backend services call into.

It's a Gin web app, organized as one Go package per business domain. Each package owns its routes, controllers, repository code, and models; cross-cutting concerns (auth, config, DB pooling, internal models) live in shared packages under `internal/`, `common/`, `security/`, etc.

## Prerequisites

- Go 1.22+
- A reachable Postgres (the app's primary DB)
- A reachable RabbitMQ (for any service that consumes / publishes events)
- A reachable Relay server (only required if your changes touch the in-cluster gateway plumbing)

## Quickstart

```bash
cd api-server/services

# Required env (minimum):
export APP_DATABASE_URL='postgresql://<user>:<pass>@localhost:5432/nudgebee?sslmode=disable'
# Optional: port-forward Postgres from your dev cluster
kubectl --namespace nudgebee port-forward svc/pg-main-proxy-service 5432:5432

# If you need the relay tunnel:
export RELAY_SERVER_SECRET_KEY=<your-key>
kubectl -n nudgebee port-forward svc/relay-server 52832:8080

# Resolve deps + run
go mod download                  # Download Go module deps
make run                         # = go run ./cmd
```

## Build commands

| Command          | What it does                                                              |
|------------------|---------------------------------------------------------------------------|
| `make fmt`       | Format the code (`gofmt`)                                                 |
| `make lint`      | Run `golangci-lint`                                                       |
| `make test`      | Run unit tests with coverage                                              |
| `make benchmark` | Run Go benchmarks with memory profiling                                   |
| `make validate`  | `fmt + lint + test` — must pass before `build`                            |
| `make build`     | Build the `services` binary (runs `validate` first)                        |
| `make install`   | Build + copy the binary to `~/go/bin/nudgebee-services-server`             |
| `make run`       | `go run ./cmd`                                                            |

Validation order before pushing: **always** `make validate`.

## Service inventory

Each subdirectory below is a domain package owning its own routes, controllers, repositories, and models.

| Package              | Purpose                                                                                          |
|----------------------|--------------------------------------------------------------------------------------------------|
| `account`            | Tenant accounts and account-level configuration                                                  |
| `anomoly`            | Anomaly-detection orchestration *(directory name retains a historical typo)*                     |
| `api`                | Generic HTTP RPC handlers (PromQL action, fallback dispatch)                                     |
| `application`        | Workload-level application context and metadata                                                  |
| `audit`              | Audit-log persistence + hooks for tenant-scoped action tracking                                  |
| `autopilot`          | Auto-remediation + policy-driven action triggers                                                 |
| `cloud`              | Cloud-account onboarding, integration validation (AWS / Azure / GCP)                             |
| `conversation`       | LLM conversation history + Optimize / investigation chat plumbing                                |
| `crawl`              | Periodic cloud-resource discovery (AWS / Azure / Civo)                                           |
| `entitlement`        | Feature gating + tier limits                                                                     |
| `event`              | Event records (alerts, signals) — ingestion + lifecycle                                          |
| `eventrule`          | Event classification rules + rule-engine dispatch                                                |
| `feedback`           | User feedback capture (RCA quality, recommendation quality)                                      |
| `insight`            | Insight aggregation across observability sources                                                 |
| `integrations`       | Third-party integration registry (Datadog, Slack, GitHub, ArgoCD, …)                             |
| `k8s_upgrade`        | Kubernetes upgrade-readiness checks                                                              |
| `knowledge_graph`    | Entity graph (services, resources, dependencies) — see [`knowledge_graph/CLAUDE.md`](knowledge_graph/CLAUDE.md) |
| `license`            | License key validation + tenant-license lookup                                                   |
| `llm`                | LLM caching, model routing, conversation memory                                                  |
| `ml`                 | ML-pipeline metadata (registry, inference results)                                               |
| `nb`                 | Nudgebee-agent version metadata (queries GitHub releases for the k8s-agent)                      |
| `notification`       | Notification routing + delivery preferences                                                      |
| `observability`      | Observability-provider integrations (Azure App Insights, Datadog, NewRelic, …)                   |
| `pr_raise`           | GitHub PR creation for runbook-driven fixes                                                      |
| `query`              | Generic metrics / log / trace query dispatching                                                  |
| `recommendation`     | Recommendation persistence + apply / dismiss lifecycle                                           |
| `relay`              | Relay-server gateway plumbing (in-cluster tunnel state)                                          |
| `reports`            | Tenant-scoped report generation                                                                  |
| `scan_orchestrator`  | Image-scan + secret-scan orchestration                                                           |
| `security`           | Request context + per-tenant authz primitives                                                    |
| `slo`                | Service Level Objectives — definition, evaluation, breach tracking                               |
| `spend`              | Cloud-spend aggregation + Optimize-cost surfaces                                                 |
| `tenant`             | Tenant lifecycle (create / suspend / delete) + tenant-scope authz                                |
| `traces`             | Distributed-trace ingestion + service-map building                                               |
| `triage`             | Event correlation + dedup (see [`triage/README.md`](triage/README.md))                           |
| `user`               | User accounts, roles, RBAC primitives                                                            |
| `workflow`           | Runbook / workflow definitions + execution state                                                 |

### Infrastructure / scaffolding packages (not business domains)

| Package      | Purpose                                                              |
|--------------|----------------------------------------------------------------------|
| `cmd/`       | `main.go` — composes packages, wires routes, starts the HTTP server   |
| `common/`    | Cross-cutting helpers (HTTP, errors, validation)                      |
| `config/`    | Environment + viper-based configuration                               |
| `docs/`      | Swagger / OpenAPI generation outputs                                  |
| `ee/`        | Enterprise-edition-only code (billing, marketplace, license)          |
| `internal/`  | Go-internal modules — DB pool, models, request context, test harness  |
| `migration/` | DB-migration entry points used at boot                                |
| `sidecar/`   | Sidecar binaries and helpers                                          |

> The `ee/` directory is stripped from OSS builds by the extraction pipeline. Contributors targeting the OSS distribution won't see it.

## Conventions

- **Code style:** standard `gofmt` formatting, enforced via `make fmt`.
- **Linting:** `golangci-lint` configured in `.golangci.yaml`.
- **Testing:** standard Go testing library; place tests alongside source code (`foo_test.go`).
- **RPC action naming:** see the **RPC action naming convention** section in the root [CLAUDE.md](../../CLAUDE.md) — `<module>_<verb>_<description>` (`accounts_list`, `runbooks_create_playbook`, …).
- **Migrations:** add new migrations via `api-server/migrations/new-migration.sh <name>` — see [migrations/README.md](../migrations/README.md).

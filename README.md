# Nudgebee

Open-source SRE copilot — observability, FinOps, runbook automation, and incident response across Kubernetes and AWS / Azure / GCP.

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev/dl/)
[![Node](https://img.shields.io/badge/Node-25+-339933?logo=node.js&logoColor=white)](https://nodejs.org/en/download)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](./CONTRIBUTING.md)
[![GitHub Discussions](https://img.shields.io/github/discussions/nudgebee/nudgebee)](https://github.com/nudgebee/nudgebee/discussions)

## What is Nudgebee?

Nudgebee is an open-source SRE copilot that watches your Kubernetes clusters and AWS / Azure / GCP accounts, turns raw signals into ranked findings, and walks operators through investigation and remediation. It bundles:

- **Observability ingestion** — Kubernetes events, metrics, traces, plus cloud-provider scans across AWS, Azure, and GCP.
- **FinOps & cost optimization** — surface unused / underutilized resources (idle workloads, oversized pods, stale snapshots, dangling volumes) and right-sizing recommendations across cloud and Kubernetes.
- **LLM-powered triage** — agentic planners that reproduce, root-cause, and propose fixes for incidents.
- **ChatOps** — Slack / Teams chatbot for SRE workflows: query state, run runbooks, ack alerts, and drive investigations from the channel where on-call already lives.
- **Runbook automation** — codify recurring fixes as reusable runbooks and trigger them from chat, alert, or schedule.
- **Ticketing + notifications** — bidirectional sync with Jira, ServiceNow, PagerDuty, Zenduty; alert delivery to Slack, Teams, email.

> _Dashboard screenshot — to be added. Track [discussion thread](https://github.com/nudgebee/nudgebee/discussions) or contribute via a PR._

## Quick Start (Local Development)

The fastest way to run Nudgebee from source. **Infra in containers, backend and frontend from source on the host.** This is the path contributors should use.

### Prerequisites

- [Docker](https://www.docker.com/products/docker-desktop/) (with `docker compose`) **or** [Podman Desktop](https://podman-desktop.io/) (with `podman-compose`)
- [Go 1.26+](https://go.dev/dl/)
- [Node 25+ and npm](https://nodejs.org/en/download)

### 1. Clone

```bash
git clone https://github.com/nudgebee/nudgebee.git
cd nudgebee
```

### 2. Bring up infra + apply migrations

```bash
docker compose up -d
```

The default compose profile starts Postgres, Redis, RabbitMQ, Qdrant, Temporal, and a one-shot `migrations` container that applies the Postgres + RabbitMQ schema and then exits. Re-runs are safe — golang-migrate is idempotent against an up-to-date tracker. To also run the backend and frontend in containers (instead of from source), use `docker compose --profile full up -d`.

See [api-server/migrations/README.md](api-server/migrations/README.md) for how migration tracking works and how to add a new migration.

### 3. Configure backend env

```bash
# macOS / Linux / WSL
cp api-server/services/.env.example api-server/services/.env

# Windows PowerShell
Copy-Item api-server\services\.env.example api-server\services\.env
```

Then generate the encryption key and replace the `__REPLACE__` placeholder for
`NUDGEBEE_ENCRYPTION_KEY` in the new `.env`:

```bash
openssl rand -hex 32
```

Keep this value — you'll paste the **same** key into `app/.env` in step 5, and
into every other service's `.env` if you later run more from source (see
**Local Stack Bootstrap** below). Rotating it after data is written makes
previously-encrypted DB rows unreadable, so treat it like a database master
password.

Other defaults work as-is against the compose stack from step 2. Read the
inline comments before any non-local deploy — a few other values (private keys)
also need rotation.

### 4. Run the backend

```bash
# macOS / Linux / WSL (requires make)
cd api-server/services
make run

# Windows (no make required — runs the same command directly)
cd api-server\services
go run ./cmd
```

Listens on <http://localhost:8000>. Leave it running.

### 5. Configure frontend env

In a new terminal:

```bash
# macOS / Linux / WSL
cp app/.env.example app/.env

# Windows PowerShell
Copy-Item app\.env.example app\.env
```

Replace `__REPLACE__` for `NUDGEBEE_ENCRYPTION_KEY` with the **same value** you
generated in step 3. The app can't decrypt what services-server writes unless
these match.

The `NEXTAUTH_SECRET` in the example is a dev-only sample; rotate it for any
non-local deploy.

### 6. Run the frontend

```bash
cd app
npm install --legacy-peer-deps
npm run dev
```

Open <http://localhost:3000>.

### 7. Sign in

On the sign-in page, click **Admin Login**. Then:

- **Email**: any address (e.g. `dev@example.com`) — a tenant + admin user are created automatically on first sign-in.
- **Password**: literally `Test!24#5` — the value of `NEXTAUTH_DUMMY_CREDS_PASSWORD` shipped in `app/.env.example`. Type it exactly; this is the dummy-credentials provider, not your own password.

### Secrets & required env vars

The sample values in steps 3 and 5 above are fine for local dev. For any
non-local deployment, generate fresh values and review the notes below.

| Var                                                    | Used by                     | How to generate           | Notes                                                                                                                                                                                                                 |
| ------------------------------------------------------ | --------------------------- | ------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `APP_DATABASE_URL`                                     | services-server             | —                         | Compose default: `postgres://postgres:postgrespassword@localhost:5432/nudgebee?sslmode=disable`. Use `localhost` from the host, `postgres` hostname from inside the compose network.                                  |
| `NUDGEBEE_ENCRYPTION_KEY`                              | services-server **and** app | `openssl rand -hex 32`    | Encrypts integration credentials and other sensitive columns. **Must match between services-server and app.** Rotating it makes previously-encrypted rows unreadable — there is no automatic re-encryption migration. |
| `ACTION_API_SERVER_TOKEN`                              | services-server **and** app | `openssl rand -hex 32`    | **Optional.** Shared secret for internal app↔services-server action calls. Defaults to empty on both sides, which disables the check (fine for local dev). If you set it, the value must match in both files.         |
| `NEXTAUTH_SECRET`                                      | app                         | `openssl rand -base64 32` | Signs both NextAuth session cookies and the inner HS256 session JWT (used by nbctl / Bearer-flow callers). Rotating it logs everyone out and invalidates outstanding bearer tokens.                                   |
| `NEXTAUTH_DUMMY_CREDS_ENABLED` / `_PASSWORD`           | app                         | —                         | Enables the any-email/password provider. Use for local development only; turn it off in any deployment exposed beyond your laptop.                                                                                    |
| `RABBIT_MQ_USERNAME` / `_PASSWORD` / `_HOST` / `_PORT` | services-server             | —                         | Compose defaults: `guest` / `guest` / `localhost` / `5672`.                                                                                                                                                           |

### Troubleshooting

- **`error pinging postgres: lookup postgres: no such host`** from backend → `APP_DATABASE_URL` in `api-server/services/.env` still uses container hostname. Replace `@postgres:5432` with `@localhost:5432`.
- **`migrate: error: pq: relation "..." already exists`** → tracker schema drifted from actual tables. Inspect `SELECT version, dirty FROM nudgebee.schema_migrations;` and use `migrate force <version>` to align. See [api-server/migrations/README.md](api-server/migrations/README.md#troubleshooting).
- **Action call from frontend returns 502 `RPC gateway could not handle the operation`** → the requested action isn't registered in `app/src/lib/actions.yaml`, or it's a subscription / fragment / parse error. Check the frontend dev-server console for the unhandled reason.

---

## Deploy to Kubernetes (Helm)

The umbrella chart is published as a public OCI artifact at `oci://ghcr.io/nudgebee/charts/nudgebee` and bundles Postgres, RabbitMQ, Redis, Qdrant, and Temporal as subcharts.

```bash
# 1. Generate a permanent encryption key — store this securely.
#    Losing it makes previously-encrypted DB rows unreadable.
export NUDGEBEE_ENC_KEY=$(openssl rand -hex 32)
echo "Save this key: $NUDGEBEE_ENC_KEY"

# 2. Install
helm install nudgebee oci://ghcr.io/nudgebee/charts/nudgebee \
  --namespace nudgebee --create-namespace \
  --set nudgebee_secret.NUDGEBEE_ENCRYPTION_KEY="$NUDGEBEE_ENC_KEY" \
  --wait --timeout 20m
```

To pin a specific version, pass `--version <X.Y.Z>` (latest is used by default). To install from source instead — useful when iterating on chart changes — clone the repo, run `helm dep update deploy/kubernetes/nudgebee`, and point `helm install` at the local path.

The post-install hook applies database migrations automatically. Once the pods are ready:

```bash
kubectl -n nudgebee port-forward svc/app 3000:80

# Retrieve the bootstrap admin password
kubectl -n nudgebee get secret nudgebee \
  -o jsonpath='{.data.NEXTAUTH_DUMMY_CREDS_PASSWORD}' | base64 -d
```

Open <http://localhost:3000> and sign in with any email + that password. See [deploy/kubernetes/README.md](deploy/kubernetes/README.md) for production-grade configuration (ingress, TLS, external Postgres, ClickHouse, observability sidecars).

### First Run — what to do after you sign in

The platform is live but empty. A quick tour that takes ~10 minutes:

1. **Connect a cloud account or a Kubernetes cluster** — _Settings → Integrations_. Onboarding a real cluster lets the K8s collector populate the knowledge graph; an AWS / GCP / Azure account lights up the spend + recommendations surfaces. Without at least one of these, most of the dashboard is intentionally empty.
2. **Wire up a notification channel** — _Settings → Integrations → Slack / Teams / Email_. Without one, you can still try the product, but notifications + chatops flows won't reach you.
3. **Run a sample runbook** — _Runbooks → Library_. The bundled library has runnable examples (health-check loops, K8s investigation, cost spotlights). Run one manually to see end-to-end orchestration.
4. **Ask the AI assistant something** — bottom-right corner of the dashboard. It can answer questions about your connected clusters, walk you through a recommendation, or kick off an investigation.

### Stuck? Want to help?

- **Want to chat?** Join us on [Discord](https://discord.gg/g2spbkGFC5) — async help, design discussions, contributor coordination.
- **Hit a bug?** Open an issue using the [bug template](.github/ISSUE_TEMPLATE/BUG-REPORT.yml).
- **Idea for a feature?** Use the [feature template](.github/ISSUE_TEMPLATE/FEATURE-REQUEST.yml).
- **Want to contribute code?** Read [CONTRIBUTING.md](./CONTRIBUTING.md) — covers CLA, branch model, PR conventions, and local-dev debugging tips. Look for issues tagged [`good first issue`](https://github.com/nudgebee/nudgebee/labels/good%20first%20issue).
- **Question that doesn't fit a template?** Email `dev@nudgebee.com` or use the contact links on the [new-issue page](https://github.com/nudgebee/nudgebee/issues/new).

## Architecture

Nudgebee is a Kubernetes-native monorepo of Go, Python, and TypeScript services. The high-level flow:

```
                ┌─────────────────────────────────────────────────┐
                │  Browser (Next.js dashboard — `app/`)           │
                └────────────────────┬────────────────────────────┘
                                     │ HTTP / WebSocket
                                     ▼
                ┌─────────────────────────────────────────────────┐
                │  app (Next.js server) — RPC gateway + NextAuth  │
                └──┬─────────────┬──────────────┬────────────┬────┘
                   │             │              │            │
                   ▼             ▼              ▼            ▼
       ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
       │ api-server   │ │ llm-server   │ │ ticket-server│ │ ml-k8s-      │
       │ services     │◀│ + rag-server │ │ notifications│ │  server      │
       │ (Go / Gin)   │ │ + code-      │ │ runbook      │ │ (right-      │
       │              │ │  analysis    │ │              │ │  sizing ML)  │
       └─┬──────┬─────┘ └──────┬───────┘ └──────┬───────┘ └──────┬───────┘
         │      │              │                │                │
         │      └──────┬───────┴────────────────┴────────────────┘
         │             ▼          ▼              ▼              ▼
         │         Postgres  RabbitMQ events  Qdrant       Temporal
         │         (state)   (cross-service)  (vectors)    (workflows)
         │                        ▲
         │ proxy /                │ publish + consume
         │ control                │
         ▼                  ┌─────┴──────────────────────┐
    ┌──────────────┐        │ cloud-collector            │
    │ relay-server │        │ k8s-collector              │
    │ (in-cluster  │        └────────────────────────────┘
    │  agent gw)   │
    └──────┬───────┘
           │ wss tunnels (also used by api-server / runbook to command agents)
           ▼
    in-cluster agents
```

- **`app/`** — Next.js dashboard; in-process RPC gateway at `/api/graphql` forwards client calls to backend `/rpc/*` handlers.
- **`api-server/services/`** — core Go backend (Gin) for tenants, accounts, recommendations, integrations.
- **`llm/llm-server` + `llm/rag-server` + `llm/code-analysis`** — LLM session state, retrieval-augmented context, on-demand code analysis.
- **`ticket-server/`** — bidirectional sync with external ticketing (Jira, ServiceNow, PagerDuty, Zenduty).
- **`runbook-server/`** — runbook orchestration via Temporal workflows. See [runbook-server/README.md](runbook-server/README.md) for architecture, env reference, API, and task framework.
- **`notifications-server/`** — Slack/Teams/email alert delivery.
- **`collector-server/`** — cloud-scan + Kubernetes metrics pipelines; `relay-server` bridges in-cluster agents back to the central plane.
- **`ml-k8s-server/`** — Python ML pipelines for workload right-sizing.

For per-service detail, see the **Project Structure** table below or each module's `README.md`.

## Local Stack Bootstrap

The repo ships a `docker-compose.yaml` that wires every service against the public container registry. You rarely need to bring all 18 containers up — start only the services that match what you're working on. The table below lists each service and the minimum set of upstream services required for it to boot successfully.

### Infra (no deps)

| Service       | Image                        | Notes                                                                                                                                |
| ------------- | ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| `postgres`    | postgres:16                  | Primary RDBMS. App schema applied by golang-migrate on deploy.                                                                       |
| `rabbitmq`    | rabbitmq:3-management        | Message bus. UI at `:15672`.                                                                                                         |
| `redis`       | redis:7-alpine               | Cache.                                                                                                                               |
| `qdrant`      | qdrant/qdrant:v1.16.0        | Vector store for RAG / LLM.                                                                                                          |
| `temporal`    | temporalio/auto-setup:1.29.1 | Workflow engine. Backed by `postgres` (creates `temporal` + `temporal_visibility` DBs on first boot). Required by `workflow-server`. |
| `temporal-ui` | temporalio/ui:2.44.0         | Optional Temporal Web UI at `:8233`.                                                                                                 |

### Application services

| Service                            | Min upstream deps                  | Why                                                                                                                                                                   |
| ---------------------------------- | ---------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `api-server-services`              | `postgres`, `rabbitmq`, `redis`    | Core backend. Won't bootstrap RabbitMQ consumers without RabbitMQ; queries fail without Postgres; cache pulls hit Redis.                                              |
| `ticket-server`                    | `postgres`, `rabbitmq`             | DB writes + async ticketing sync.                                                                                                                                     |
| `workflow-server` (runbook-server) | `postgres`, `rabbitmq`, `temporal` | Workflow state in PG; events on RMQ; Temporal SDK calls `Dial(7233)` at startup and crashes if unreachable.                                                           |
| `notifications-server`             | `postgres`, `rabbitmq`             | Persists messages, consumes RMQ events.                                                                                                                               |
| `cloud-collector`                  | `rabbitmq`                         | Publishes scrape events to RMQ.                                                                                                                                       |
| `relay-server`                     | `postgres`, `rabbitmq`             | K8s gateway; tunnel state in PG, events on RMQ.                                                                                                                       |
| `k8s-collector-app`                | `rabbitmq`                         | Publishes K8s metrics to RMQ.                                                                                                                                         |
| `ml-k8s-server`                    | `postgres`                         | Reads/writes scaling features.                                                                                                                                        |
| `llm-server`                       | `postgres`, `qdrant`               | LLM session state + vector lookups. Spawns `code-analysis` per account on demand (not a long-running compose service).                                                |
| `rag-server`                       | `postgres`, `qdrant`               | RAG retrieval against Qdrant; metadata in PG.                                                                                                                         |

### Frontend

| Service         | Min upstream deps     | Why                                                                                                                                                                                                                                    |
| --------------- | --------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `app` (Next.js) | `api-server-services` | All GraphQL operations are served by the in-process RPC gateway in the Next.js server (`/api/graphql`) and forwarded to api-server-services action handlers. RabbitMQ/Redis are required indirectly via api-server-services' own deps. |

### Common minimal stacks

- **DB exploration:** `postgres` (connect with `psql` / DBeaver).
- **Login + dashboard render:** `postgres` + `api-server-services` + `app` (pulls RabbitMQ/Redis transitively).
- **Cloud findings pipeline:** add `rabbitmq` + `cloud-collector`.
- **LLM/RAG flows:** add `qdrant` + `llm-server` + `rag-server`.

Start a subset with `docker compose up -d <service> [<service> ...]` (or `podman-compose up -d ...`); transitive deps are pulled in automatically via `depends_on`.

## Project Structure

Each module has its own README with setup and development instructions.

### Frontend

| Module | Description                                    | README                         |
| ------ | ---------------------------------------------- | ------------------------------ |
| `app/` | Frontend dashboard (Next.js + React, NextAuth) | [app/README.md](app/README.md) |

### API & GraphQL Layer

| Module                   | Description                                                       | README                                                             |
| ------------------------ | ----------------------------------------------------------------- | ------------------------------------------------------------------ |
| `api-server/`            | GraphQL API layer overview                                        | [api-server/README.md](api-server/README.md)                       |
| `api-server/services/`   | Core backend Go services (Gin)                                    | [api-server/services/README.md](api-server/services/README.md)     |
| `api-server/migrations/` | DB migrations (Postgres via golang-migrate, ClickHouse, RabbitMQ) | [api-server/migrations/README.md](api-server/migrations/README.md) |

### Data Collection

| Module                                         | Description                      | README                                                                                                         |
| ---------------------------------------------- | -------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| `collector-server/cloud-collector/`            | AWS/cloud data collection        | [collector-server/cloud-collector/README.md](collector-server/cloud-collector/README.md)                       |
| `collector-server/k8s-collector/app/`          | K8s metrics aggregation (Python) | [collector-server/k8s-collector/app/README.md](collector-server/k8s-collector/app/README.md)                   |
| `collector-server/k8s-collector/relay-server/` | K8s relay gateway (WebSocket)    | [collector-server/k8s-collector/relay-server/README.md](collector-server/k8s-collector/relay-server/README.md) |

### ML & LLM

| Module               | Description                          | README                                                     |
| -------------------- | ------------------------------------ | ---------------------------------------------------------- |
| `ml-k8s-server/`     | ML models & K8s autoscaling          | [ml-k8s-server/README.md](ml-k8s-server/README.md)         |
| `llm/llm-server/`    | LLM inference service                | [llm/llm-server/README.md](llm/llm-server/README.md)       |
| `llm/code-analysis/` | Code analysis engine                 | [llm/code-analysis/README.md](llm/code-analysis/README.md) |
| `llm/rag-server/`    | RAG (Retrieval Augmented Generation) | [llm/rag-server/README.md](llm/rag-server/README.md)       |
| `llm/benchmark/`     | LLM benchmarking                     | [llm/benchmark/README.md](llm/benchmark/README.md)         |

### Automation & Operations

| Module                  | Description                                                           | README                                                           |
| ----------------------- | --------------------------------------------------------------------- | ---------------------------------------------------------------- |
| `runbook-server/`       | Runbook orchestration + automation engine (Temporal)                  | [runbook-server/README.md](runbook-server/README.md)             |
| `ticket-server/`        | External ticketing integration (Jira, ServiceNow, PagerDuty, Zenduty) | [ticket-server/README.md](ticket-server/README.md)               |
| `notifications-server/` | Notification delivery (Slack, Teams, email)                           | [notifications-server/README.md](notifications-server/README.md) |

### Deploy & Infrastructure

| Module               | Description                           | README                                                     |
| -------------------- | ------------------------------------- | ---------------------------------------------------------- |
| `deploy/kubernetes/` | Helm charts & Kubernetes config files | [deploy/kubernetes/README.md](deploy/kubernetes/README.md) |

### Tests

| Module           | Description                  |
| ---------------- | ---------------------------- |
| `app-e2e-tests/` | End-to-end integration tests |

## Contributing

We welcome contributions! Before opening your first PR:

1. Read [CONTRIBUTING.md](./CONTRIBUTING.md) for the development workflow, conventional-commit format, and PR guidelines.
2. Review the [Code of Conduct](./CODE_OF_CONDUCT.md).
3. Browse [open issues](https://github.com/nudgebee/nudgebee/issues) — look for `good first issue` and `help wanted` labels if you're getting started.

By contributing, you agree your contributions are licensed under the Apache License, Version 2.0. On your first PR, the [CLA Assistant](https://cla-assistant.io/) bot will post a one-click sign link — subsequent PRs need no further action.

## Security

If you believe you have found a security vulnerability in Nudgebee, **please do not open a public GitHub issue**. Instead follow the responsible disclosure process in [SECURITY.md](./SECURITY.md).

## Telemetry

Nudgebee ships with **no telemetry or product analytics**. No data leaves your cluster except what you explicitly configure — notification webhooks, ticket-system sync, LLM provider calls, and any outbound integrations you wire up.

## Community & Support

- **Questions and ideas** — [GitHub Discussions](https://github.com/nudgebee/nudgebee/discussions)
- **Bugs and feature requests** — [GitHub Issues](https://github.com/nudgebee/nudgebee/issues)
- **Security reports** — [SECURITY.md](./SECURITY.md)

## License

Apache License 2.0 — see [LICENSE](./LICENSE).

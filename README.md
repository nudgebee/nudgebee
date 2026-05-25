# Nudgebee

Open-source SRE copilot — Kubernetes observability, runbook automation, and incident response.

## Quick Start (Local Development)

The fastest way to run Nudgebee from source. **Infra in containers, backend and frontend from source on the host.** This is the path contributors should use.

### Prerequisites

- [Docker](https://www.docker.com/products/docker-desktop/) (with `docker compose`) **or** [Podman Desktop](https://podman-desktop.io/) (with `podman-compose`)
- [Go 1.26+](https://go.dev/dl/)
- [Node 22+ and npm](https://nodejs.org/en/download)

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
cp api-server/services/.env.example api-server/services/.env
```

The defaults work as-is against the compose stack from step 2. Read the inline
comments in the file before any non-local deploy — a few values (encryption key,
private keys) must be rotated.

### 4. Run the backend

```bash
cd api-server/services
make run
```

Listens on <http://localhost:8000>. Leave it running.

### 5. Configure frontend env

In a new terminal:

```bash
cp app/.env.example app/.env
```

The defaults work as-is. `NUDGEBEE_ENCRYPTION_KEY` matches the value in
`api-server/services/.env.example` — this **must stay in sync** between the
two files. The `NEXTAUTH_SECRET` in the example is a dev-only sample;
rotate it for any non-local deploy.

### 6. Run the frontend

```bash
cd app
npm install --legacy-peer-deps
npm run dev
```

Open <http://localhost:3000>.

### 7. Sign in

The local-dev signin form accepts any email with the dummy password:

- **Username**: any email (e.g. `dev@example.com`)
- **Password**: `Test!24#5` (the value of `NEXTAUTH_DUMMY_CREDS_PASSWORD`)

A tenant + admin user are created automatically on first sign-in.

### Secrets & required env vars

The sample values in steps 3 and 5 above are fine for local dev. For any
non-local deployment, generate fresh values and review the notes below.

| Var | Used by | How to generate | Notes |
|---|---|---|---|
| `APP_DATABASE_URL` | services-server | — | Compose default: `postgres://postgres:postgrespassword@localhost:5432/nudgebee?sslmode=disable`. Use `localhost` from the host, `postgres` hostname from inside the compose network. |
| `NUDGEBEE_ENCRYPTION_KEY` | services-server **and** app | `openssl rand -hex 32` | Encrypts integration credentials and other sensitive columns. **Must match between services-server and app.** Rotating it makes previously-encrypted rows unreadable — there is no automatic re-encryption migration. |
| `ACTION_API_SERVER_TOKEN` | services-server **and** app | `openssl rand -hex 32` | **Optional.** Shared secret for internal app↔services-server action calls. Defaults to empty on both sides, which disables the check (fine for local dev). If you set it, the value must match in both files. |
| `NEXTAUTH_SECRET` | app | `openssl rand -base64 32` | Signs both NextAuth session cookies and the inner HS256 session JWT (used by nbctl / Bearer-flow callers). Rotating it logs everyone out and invalidates outstanding bearer tokens. |
| `NEXTAUTH_DUMMY_CREDS_ENABLED` / `_PASSWORD` | app | — | Enables the any-email/password provider. Use for local development only; turn it off in any deployment exposed beyond your laptop. |
| `RABBIT_MQ_USERNAME` / `_PASSWORD` / `_HOST` / `_PORT` | services-server | — | Compose defaults: `guest` / `guest` / `localhost` / `5672`. |
| `CLICKHOUSE_ENABLED` | services-server | — | `false` for the OSS local stack — ClickHouse is not part of compose. |

### Troubleshooting

- **`error pinging postgres: lookup postgres: no such host`** from backend → `APP_DATABASE_URL` in `api-server/services/.env` still uses container hostname. Replace `@postgres:5432` with `@localhost:5432`.
- **`migrate: error: pq: relation "..." already exists`** → tracker schema drifted from actual tables. Inspect `SELECT version, dirty FROM nudgebee.schema_migrations;` and use `migrate force <version>` to align. See [api-server/migrations/README.md](api-server/migrations/README.md#troubleshooting).
- **Action call from frontend returns 502 `RPC gateway could not handle the operation`** → the requested action isn't registered in `app/src/lib/actions.yaml`, or it's a subscription / fragment / parse error. Check the frontend dev-server console for the unhandled reason.

---

## Developer Setup (Internal — nudgebee maintainers)

> Steps below are for the nudgebee dev cluster. Open-source contributors should follow [Quick Start](#quick-start-local-development) above.

### Softwares

- [Docker](https://www.docker.com/products/docker-desktop/)
- [Python3 & pip](https://www.python.org/downloads/)
- [Nodejs & npm](https://nodejs.org/en/download)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [AWS Cli](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html)
- [psql (DBeaver or similar)](https://dbeaver.io/)
- [Helm](https://helm.sh/)

### Configurations

- Get AWS Credentials
- [Configure Kubectl for EKS](https://docs.aws.amazon.com/eks/latest/userguide/create-kubeconfig.html)
  - `aws sts get-caller-identity` - varify AWS is configured correctly
  - `aws eks update-kubeconfig --region us-east-1 --name nudgebee-dev` update K8s Configs

#### Test Configurations

Varify AWS configured properly

```
aws sts get-caller-identity
```

Varify k8s configured properly

```
kubectl --namespace nudgebee get pods
```

### Accessing Dev Servers

Dev servers can be accessed using port-forwarding using kubectl

- Nudgebee Postgres

```
kubectl --namespace nudgebee port-forward svc/pg-main-proxy-service 5432:5432
```

- Clickhouse

```
kubectl --namespace clickhouse port-forward svc/clickhouse 8123:8123
```

- RabbitMQ

```
kubectl --namespace rabbit port-forward svc/rabbitmq 15672:15672
```

## Local Stack Bootstrap

The repo ships a `docker-compose.yaml` that wires every service against the public container registry. You rarely need to bring all 17 containers up — start only the services that match what you're working on. The table below lists each service and the minimum set of upstream services required for it to boot successfully.

### Infra (no deps)

| Service | Image | Notes |
|---|---|---|
| `postgres` | postgres:16 | Primary RDBMS. App schema applied by golang-migrate on deploy. |
| `rabbitmq` | rabbitmq:3-management | Message bus. UI at `:15672`. |
| `redis` | redis:7-alpine | Cache. |
| `qdrant` | qdrant/qdrant:v1.16.0 | Vector store for RAG / LLM. |
| `temporal` | temporalio/auto-setup:1.29.1 | Workflow engine. Backed by `postgres` (creates `temporal` + `temporal_visibility` DBs on first boot). Required by `workflow-server`. |
| `temporal-ui` | temporalio/ui:2.44.0 | Optional Temporal Web UI at `:8233`. |

> ClickHouse is intentionally **not** in the local-dev compose. Backend services run with `CLICKHOUSE_ENABLED=false` locally; bring up a `clickhouse` container manually if you're working on analytics-pipeline code.

### Application services

| Service | Min upstream deps | Why |
|---|---|---|
| `api-server-services` | `postgres`, `rabbitmq`, `redis` | Core backend. Won't bootstrap RabbitMQ consumers without RabbitMQ; queries fail without Postgres; cache pulls hit Redis. ClickHouse is gated on `CLICKHOUSE_ENABLED`. |
| `ticket-server` | `postgres`, `rabbitmq` | DB writes + async Jira sync. |
| `workflow-server` (runbook-server / auto-pilot) | `postgres`, `rabbitmq`, `temporal` | Workflow state in PG; events on RMQ; Temporal SDK calls `Dial(7233)` at startup and crashes if unreachable. |
| `notifications-server` | `postgres`, `rabbitmq` | Persists messages, consumes RMQ events. |
| `cloud-collector` | `rabbitmq` | Publishes scrape events to RMQ. ClickHouse writes are skipped when disabled. |
| `relay-server` | `postgres`, `rabbitmq` | K8s gateway; tunnel state in PG, events on RMQ. |
| `k8s-collector-app` | `rabbitmq` | Publishes K8s metrics to RMQ. ClickHouse writes skipped when disabled. |
| `ml-k8s-server` | `postgres` | Reads/writes scaling features. |
| `llm-server` | `postgres`, `qdrant` | LLM session state + vector lookups. Spawns `code-analysis` per account on demand (not a long-running compose service). |
| `rag-server` | `postgres`, `qdrant` | RAG retrieval against Qdrant; metadata in PG. |

### Frontend

| Service | Min upstream deps | Why |
|---|---|---|
| `app` (Next.js) | `api-server-services` | All GraphQL operations are served by the in-process RPC gateway in the Next.js server (`/api/graphql`) and forwarded to api-server-services action handlers. RabbitMQ/Redis are required indirectly via api-server-services' own deps. |

### Common minimal stacks

- **DB exploration:** `postgres` (connect with `psql` / DBeaver).
- **Login + dashboard render:** `postgres` + `api-server-services` + `app` (pulls RabbitMQ/Redis transitively).
- **Cloud findings pipeline:** add `rabbitmq` + `cloud-collector` (start a manual `clickhouse` container too if you want findings persisted; set `CLICKHOUSE_ENABLED=true` in backend env).
- **LLM/RAG flows:** add `qdrant` + `llm-server` + `rag-server`.

Start a subset with `podman-compose up -d <service> [<service> ...]`; transitive deps are pulled in automatically via `depends_on`.

## Project Structure

Each module has its own README with setup and development instructions. Refer to them for module-specific guidance.

### Frontend

| Module | Description | README |
|--------|-------------|--------|
| `app/` | Frontend dashboard (Next.js + React, NextAuth) | [app/README.md](app/README.md) |

### API & GraphQL Layer

| Module | Description | README |
|--------|-------------|--------|
| `api-server/` | GraphQL API layer overview | [api-server/README.md](api-server/README.md) |
| `api-server/services/` | Core backend Go services (Gin) | [api-server/services/README.md](api-server/services/README.md) |
| `api-server/migrations/` | DB migrations (Postgres via golang-migrate, Clickhouse, RabbitMQ) | [api-server/migrations/README.md](api-server/migrations/README.md) |

### Data Collection

| Module | Description | README |
|--------|-------------|--------|
| `collector-server/cloud-collector/` | AWS/cloud data collection | [collector-server/cloud-collector/README.md](collector-server/cloud-collector/README.md) |
| `collector-server/k8s-collector/app/` | K8s metrics aggregation (Python) | [collector-server/k8s-collector/app/README.md](collector-server/k8s-collector/app/README.md) |
| `collector-server/k8s-collector/relay-server/` | K8s relay gateway (WebSocket) | [collector-server/k8s-collector/relay-server/README.md](collector-server/k8s-collector/relay-server/README.md) |

### ML & LLM

| Module | Description | README |
|--------|-------------|--------|
| `ml-k8s-server/` | ML models & K8s autoscaling | [ml-k8s-server/README.md](ml-k8s-server/README.md) |
| `llm/llm-server/` | LLM inference service | [llm/llm-server/README.md](llm/llm-server/README.md) |
| `llm/code-analysis/` | Code analysis engine | [llm/code-analysis/README.md](llm/code-analysis/README.md) |
| `llm/rag-server/` | RAG (Retrieval Augmented Generation) | [llm/rag-server/README.md](llm/rag-server/README.md) |
| `llm/benchmark/` | LLM benchmarking | [llm/benchmark/README.md](llm/benchmark/README.md) |

### Automation & Operations

| Module | Description | README |
|--------|-------------|--------|
| `auto-pilot/` | Automation & remediation engine | *(no README — see CLAUDE.md)* |
| `runbook-server/` | Runbook management | [runbook-server/README.md](runbook-server/README.md) |
| `ticket-server/` | Ticket management (Jira integration) | [ticket-server/README.md](ticket-server/README.md) |
| `notifications-server/` | Notification delivery (Slack, Teams, etc.) | [notifications-server/README.md](notifications-server/README.md) |

### Deploy & Infrastructure

| Module | Description | README |
|--------|-------------|--------|
| `deploy/kubernetes/` | Helm charts & Kubernetes config files | [deploy/kubernetes/README.md](deploy/kubernetes/README.md) |

### Tests

| Module | Description |
|--------|-------------|
| `app-e2e-tests/` | End-to-end integration tests |

## App Deployments

- Currently using Github Actions for deployment
- Most of the apps are deployed on Kubernetes

## Infrastructure Components

Current Infra Components

### Logs

- Loki

### Ingress

- Nginx Ingress using Network load balancer
- Within Kubernetes `ingress-nginx` namespace

### SSL/TLS

- [Cert Manager](https://cert-manager.io/)
- Within Kubernetes `cert-manager` namespace

### Metrics Collection

- [Prometheus](https://prometheus.io/)
- Within Kubernetes `prometheus` namespace

### Messaging Queue

- [RabbitMQ](https://www.rabbitmq.com/)
- Within Kubernetes `rabbit` namespace

### Analytics DB

- [Clickhouse](https://clickhouse.com)
- Within Kubernetes `clickhouse` namespace

### RDBMS

- Primary DB, Postgres, Deployed on AWS

## Tech Docs -

https://drive.google.com/drive/folders/1l8q5SsZa-lQVCDbV6NQPOE565erullEi?usp=sharing

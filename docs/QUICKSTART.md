# Quick Start

The full local-development walkthrough — clone, bring up infra with Docker Compose, configure backend + frontend env, run the app, and sign in — lives in the root README:

➜ **[README → Quick Start (Local Development)](../README.md#quick-start-local-development)**

## TL;DR (after reading the README walkthrough)

```bash
git clone https://github.com/nudgebee/nudgebee.git && cd nudgebee
docker compose up -d postgres rabbitmq redis qdrant temporal migrations
cp api-server/services/.env.example api-server/services/.env  # edit as needed
cd api-server/services && make run
# in another shell:
cp app/.env.example app/.env  # edit as needed
cd app && npm install --legacy-peer-deps && npm run dev
```

Then open `http://localhost:3000` and follow the **Sign in** instructions in [README → step 7](../README.md#7-sign-in).

## Everything in Docker (`full` profile)

To run **all** services (not just infra) in containers, use the `full` profile. The
compose file ships working local-dev defaults for every service, so this boots
out of the box:

```bash
cp .env.example .env      # optional but recommended — see notes below
docker compose --profile full up -d
```

A few real-world notes the tooling now handles for you (documented so you know
why the settings exist):

- **`NUDGEBEE_ENCRYPTION_KEY` must be identical across every service.** It
  encrypts integration credentials at rest, so if two services disagree on the
  key, one can't read what the other wrote. The compose file injects one shared
  value into all services; override it in `.env` with `openssl rand -hex 32`
  before using this anywhere real. Changing it after credentials are stored makes
  them undecryptable.
- **macOS host port 5000 is taken by AirPlay Receiver.** The `k8s-collector`
  container listens on 5000 but is published on host **5055** by default (set
  `K8S_COLLECTOR_HOST_PORT` to change, or free 5000 in *System Settings →
  General → AirDrop & Handoff → AirPlay Receiver*).
- **Services call the backend at hostname `services-server`.** That's the SaaS
  service name; the compose service is `api-server-services`. A network alias
  maps `services-server` → `api-server-services`, so event ingestion and RPC
  work without per-service URL overrides.
- **Each service reads its DB URL under a different variable name**
  (`APP_DATABASE_URL`, `LLM_SERVER_DB_URL`, `COLLECTOR_DB_URL`,
  `AUTO_PILOT_DATABASE_URL`, `ML_INFERENCE_DATABASE_URL`, `NOTIFICATION_DB_URL`,
  …). The compose file already sets the right one per service.
- **ClickHouse is optional and off by default.** In Docker it's stubbed
  (`CLICKHOUSE_ENABLED=false`); on Kubernetes, a `clickhouse.enabled=false`
  Helm install renders an inert `clickhouse` secret so the deploy never fails
  on a missing reference.

## When in doubt

- **What's the matching command for service X?** [README → Project Structure table](../README.md#project-structure) lists each service's run command.
- **Build failing locally?** [CONTRIBUTING.md → Troubleshooting](../CONTRIBUTING.md#troubleshooting) covers the common ones (`fetch failed`, Postgres `connection refused`, RabbitMQ stuck queues, npm peer deps, OOM on build, etc.).
- **Need to deploy this to a real cluster?** [README → Deploy to Kubernetes (Helm)](../README.md#deploy-to-kubernetes-helm).

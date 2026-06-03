# Kubernetes Collector App

Python service that runs inside a target Kubernetes cluster and ships **metrics + events** to the central Nudgebee platform. The Python app handles non-realtime aggregation (queries, schedules, summaries); the Go-based [relay-server](../relay-server/README.md) sits next to it and handles the realtime WebSocket pipe back to the cloud.

## What this service does

```
   Kubernetes API ─┐
                   │      ┌──────────────────────┐                ┌─────────────┐
   Prometheus  ────┼─────▶│ k8s-collector/app    │──── HTTP ────▶│  Nudgebee   │
                   │      │ (Python, Flask)      │                │  cloud API  │
   RabbitMQ ──────┘       │                       │──── publish ─▶│  RabbitMQ   │
                          └──────────────────────┘                └─────────────┘
```

Specifically it:

1. Queries the in-cluster Kubernetes API + Prometheus for workload state.
2. Consumes work items off RabbitMQ (scheduled collection jobs, ad-hoc requests).
3. Posts aggregated payloads back to the cloud (`SERVICE_API_SERVER_URL`).
4. Exposes a `/health` HTTP endpoint for liveness/readiness probes.

It does **not** handle pod-exec / log-streaming / WebSocket traffic — that's the relay-server's job.

## Prerequisites

- Python 3.11 or 3.12
- [Poetry](https://python-poetry.org/) (dependency management)
- A reachable Kubernetes API (in-cluster or via kubeconfig)
- A reachable Prometheus
- A reachable RabbitMQ (for non-server modes)

## Quickstart

```bash
cd collector-server/k8s-collector/app

# 1. Install dependencies
make install                     # = poetry install

# 2. Run locally (in-cluster auth requires a kubeconfig pointing at your dev cluster)
make run                         # = python app.py
```

## Collector modes

The same binary can run in three modes, selected by `COLLECTOR_MODE`:

| Mode      | What runs                                | When to use                                      |
|-----------|------------------------------------------|--------------------------------------------------|
| `both`    | HTTP server **and** RabbitMQ consumers   | Default. Suitable for single-replica deployments |
| `server`  | HTTP server only (no consumers)          | Horizontal scale: multiple HTTP-only replicas    |
| `worker`  | RabbitMQ consumers only (no HTTP)        | Horizontal scale: separate consumer replicas     |

## Configuration

Key environment variables (see code for a full list):

| Variable                      | Default              | Purpose                                           |
|-------------------------------|----------------------|---------------------------------------------------|
| `COLLECTOR_MODE`              | `both`               | Process role — `both` / `server` / `worker`        |
| `SERVICE_PORT`                | `5000`               | HTTP port (overridable per workload)               |
| `SERVICE_HOST`                | `0.0.0.0`            | HTTP bind address                                  |
| `ENV`                         | `PROD`               | Logging/behavior profile — `DEV` / `DEVELOPMENT` map to Debug; any other value (`PROD`, `PRODUCTION`, etc.) maps to Production |
| `APP_VERSION`                 | `0.0.0`              | Reported as a label on Prometheus metrics          |

Other expected env vars (provided by the chart):

- `RABBIT_MQ_HOST` / `_PORT` / `_USERNAME` / `_PASSWORD`
- `SERVICE_API_SERVER_URL` — upstream Nudgebee API endpoint
- `RELAY_SERVER_ENDPOINT` — for any cross-talk with the relay sidecar

## Module structure

```
apis/         # HTTP route handlers
controllers/  # Business logic invoked by APIs
config/       # Settings + environment resolution
db/           # DB clients
handlers/     # Long-running task handlers
metrics/      # Prometheus metrics exporters for this service
middleware/   # Flask middleware
rabbitmq/     # Consumer wiring + message schemas
utils/        # Shared helpers
exception/    # Custom exceptions
```

Entry point is `app.py` — it selects which of the three modes (`both` / `server` / `worker`) to run via `COLLECTOR_MODE`.

## Development

```bash
make fmt           # black
make lint          # black --check + flake8 + mypy
make test          # pytest
make clean         # remove caches
```

Validation order before pushing: `make lint && make test`.

## Deployment

This service is deployed inside the target Kubernetes cluster (typically as a Deployment, not a DaemonSet — there's one collector per cluster, not per node). The Helm chart and image build are under `deploy/kubernetes/`. See the [root README](../../../README.md) for the broader install flow.

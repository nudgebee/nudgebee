# ML K8s Server

Python service that produces **rightsizing recommendations** and **anomaly detections** for Kubernetes workloads. Reads historical metrics from Prometheus, runs them through scikit-learn / Keras / TensorFlow models, and either returns the prediction synchronously or publishes it to RabbitMQ for asynchronous processing by downstream consumers.

Part of the broader Nudgebee platform; see the [root README](../README.md) for the full service map.

## Architecture at a glance

```
                            ┌─────────────────┐
              POST /anomaly │                 │   read PromQL queries
              /rightsizing  │ ml-k8s-server   │ ───────────────────────▶  Prometheus
              /metrics      │  (Flask + Gunicorn)
              ──────────────▶                 │   publish recommendations
                            │                 │ ───────────────────────▶  RabbitMQ
                            └─────────────────┘                              │
                                    │                                        ▼
                                    │ (one Gunicorn worker = consumer)   downstream
                                    └────────────────────────────────────▶ services
                            ┌─────────────────┐
              GET /health   │   health_app    │   (separate Flask app, separate port)
              ──────────────▶ (sidecar port)  │
                            └─────────────────┘
```

The RabbitMQ consumer is started exactly once via `gunicorn.conf.py`'s `post_fork` hook so TF-heavy work is isolated to a single Gunicorn worker.

## Prerequisites

- Python 3.11 or 3.12
- [Poetry](https://python-poetry.org/) (dependency management)
- Prometheus reachable from the host running this service (port-forward in dev)
- RabbitMQ reachable from the host (only required for endpoints that publish work asynchronously — `/rightsizing/*`)

## Quickstart

```bash
cd ml-k8s-server

# 1. Install dependencies
make install                     # = poetry install

# 2. Port-forward Prometheus from your dev cluster
kubectl port-forward service/prometheus-kube-prometheus-prometheus 9090 -nprometheus &> /dev/null &

# 3. Run the server (binds to ML_PORT, default 9999)
make run                         # = python -m server.app
```

## API endpoints

| Method | Path                       | Purpose                                                                 |
|--------|----------------------------|-------------------------------------------------------------------------|
| POST   | `/metrics`                 | Ingest a batch of metrics                                               |
| POST   | `/anomaly`                 | Detect anomalies for a known metric type (`memory`/`cpu`/`latency`/...) |
| POST   | `/anomaly/detect`          | Detect anomalies from a custom PromQL query + time range                |
| POST   | `/rightsizing/cluster`     | Cluster-level node rightsizing recommendation                           |
| POST   | `/rightsizing/vertical`    | Vertical (per-pod) rightsizing — async, returns 202 + queues to RabbitMQ |
| POST   | `/rightsizing/volume`      | Volume rightsizing — async, queues to RabbitMQ                          |
| POST   | `/update_recommendations`  | Trigger a refresh of stored recommendations                             |
| GET    | `/health`                  | Liveness/readiness probe (served on the separate `health_app`)          |

### Example: anomaly detection

```bash
curl -X POST http://localhost:9999/anomaly/detect \
  -H "Content-Type: application/json" \
  -d '{
    "account": "your-account-id",
    "query": "rate(container_memory_usage_bytes[5m])",
    "analysis_start_time": "2026-06-01T10:00:00Z",
    "analysis_end_time":   "2026-06-01T11:00:00Z",
    "historical_window_hours": 24,
    "anomaly_type": "memory"
  }'
```

Full request-body docs are in the controller docstrings — `server/controllers/anomaly.py` and `server/controllers/rightsizing.py`.

## Configuration

All configuration is via environment variables. Defaults are shown in parentheses.

| Variable                          | Default              | Purpose                                          |
|-----------------------------------|----------------------|--------------------------------------------------|
| `ML_PORT`                         | `9999`               | HTTP port for the main Flask app                  |
| `HEALTH_PORT`                     | `8081`               | HTTP port for the separate health Flask app       |
| `ANOMALY_ALGO`                    | `ISOLATION_TREE`     | Anomaly detection algorithm (`ISOLATION_TREE` or `DB_SCAN`) |
| `ML_INFERENCE_DATABASE_URL`       | _empty_              | Postgres URL for storing inference results        |
| `ML_INFERENCE_TABLE_NAME`         | `ml_inference`       | Table that holds inference results                |
| `ML_RECOMMENDATION_TABLE_NAME`    | `recommendation`     | Table that holds recommendations                  |
| `ML_DB_POOL_SIZE` / `_MAX_OVERFLOW` / `_POOL_RECYCLE` | `5` / `5` / `1800` | SQLAlchemy pool tuning |
| `RABBIT_MQ_HOST` / `_PORT` / `_USERNAME` / `_PASSWORD` | `localhost` / `5672` / `guest` / `guest` | RabbitMQ connection |
| `ML_RECOMMENDATION_QUEUE`         | `ml-recommendation`  | RabbitMQ queue name (recommendations)             |
| `ML_RECOMMENDATION_EXCHANGE`      | `ml-recommendation`  | RabbitMQ exchange name                            |
| `ML_FILE_BASE_PATH`               | `tempfile.gettempdir()` | On-disk path for cached model artifacts        |
| `ML_PREDICTION_STEP`              | `168`                | Forecast horizon (steps)                          |
| `ML_PREDICTION_INITIAL_SET`       | `24`                 | Initial training-window size                      |
| `ML_RUN_EPOCHS`                   | `5`                  | Training epochs                                   |
| `ML_RUN_BATCH_SIZE`               | `64`                 | Training batch size                               |
| `ML_RUN_VALIDATION_SPLIT`         | `0.20`               | Train/val split ratio                             |

For a complete list, see `server/utils/utils.py:QueueConfig`.

## Module structure

```
server/
├── controllers/   # One Flask Blueprint per endpoint family: health, metrics,
│                  # anomaly, rightsizing, prediction (the last hosts
│                  # /update_recommendations)
├── anomaly/       # Anomaly-detection algorithm implementations
├── model/         # ML model loading + persistence
├── metrics/       # Prometheus metric exporters for this service
└── utils/         # Shared config (QueueConfig + env-var resolution)
```

Entry point is `server/app.py` (`python -m server.app`). The RabbitMQ consumer is wired in `gunicorn.conf.py` via a `post_fork` hook so only one Gunicorn worker runs it.

For deeper anomaly-detection algorithm notes, see [`ANOMALY_DETECTION.md`](./ANOMALY_DETECTION.md).

## Development

```bash
make fmt                # Format with black
make lint               # black --check + flake8 + mypy
make test               # pytest
make clean              # Remove caches + build artifacts
```

Validation order before pushing: `make lint && make test`.

## Conventions

- **Line length:** 120 (enforced by black)
- **Type checking:** mypy, namespace packages enabled
- **Test framework:** pytest, tests under `tests/`
- **Logging:** structured JSON via `python-json-logger`, configured in `server/logging.json`
- **Tracing:** OpenTelemetry — exporter wired in `server/utils/utils.py:set_global_trace()`

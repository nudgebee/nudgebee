# Anomaly Detection Subsystem

> Cross-service feature. This package (`api-server/services/anomoly` — note the misspelled directory
> name, kept for historical reasons) is the **orchestration core**, but the feature spans
> `api-server`, `ml-k8s-server` (Python ML), the Next.js `app`, and `llm-server`. Read this before
> touching anomaly code anywhere.

There are **two distinct anomaly subsystems** sharing one `anomaly` table:

1. **K8s metric anomalies** — ML-based (IsolationForest / DBSCAN / ZScore), runs against Prometheus
   workload metrics. The bulk of this doc.
2. **Cloud spend anomalies** — statistical Z-score only, no ML server. See
   [`spend_anomaly.go`](spend_anomaly.go).

## Data flow (K8s metric path)

```
Frontend (Next.js)                api-server (Go)                  ml-k8s-server (Python)
─────────────────                ───────────────                  ──────────────────────
KubernetesAnomaly.tsx ──RPC──▶  /rpc/anomaly (api/anomaly.go)
  (trigger button)                     │  handleAnomaly() → async goroutine, returns 200 immediately
                                       ▼
                              anomoly/service.go
                                executeForAccountPairs()
                                  ├─ ML path ──RabbitMQ──▶  controllers/anomaly.py  /anomaly
                                  │                            anomaly_algo/* (IsolationForest default)
                                  │   ProcessAnomaly() ◀──────┘  (RabbitMQ consumer)
                                  └─ Prometheus path (direct 1/7/14-day compare, no ML)
                                       ▼
                              insert into `anomaly` table
                              + GenerateAnomalyEvent() + collectEvidences()
                                       │
Frontend reads ◀──GraphQL── anomalies_list / anomalies_list_v2 / anomaly_type_v2
```

## Key files

### api-server (Go)
| File | What |
|---|---|
| [`api/anomaly.go`](../api/anomaly.go) | RPC handler `handleAnomaly()`. Dispatches `anomaly_execute` (+ legacy `trigger_anomaly_execute` alias) and `anomaly_template_list`. Spawns an **async goroutine** and returns `200 {status:"triggered"}` — fire-and-forget. |
| [`service.go`](service.go) | Orchestration core. `Execute()` (batch all accounts), `ExecuteForAccount()` (manual), `executeForAccountPairs()` (fan-out apps × configs, shuffles for load spread), `processSingleApplicationMlAsync()` (publish to RabbitMQ), `processSingleApplicationPrometheus()` (direct compare), `ProcessAnomaly()` (RabbitMQ consumer), `insertAnomaly()`, `GenerateAnomalyEvent()`, `collectEvidences()`. RabbitMQ consumer wired in `init()`. |
| [`model.go`](model.go) | `Anomaly` struct, `AnomalyType` enum (CPU, Memory, Latency, Network, ErrorRate, Replicas, CloudSpendAccount, CloudSpendService), `AnomalyTemplate`. |
| [`anamoly_template.json`](anamoly_template.json) | Embedded config for the 5 metric types — `change_operator`, `title`, `buffer_percentage`. |
| [`spend_anomaly.go`](spend_anomaly.go) | Separate Z-score spend detection (30-day baseline, Z≥3.0, ≥20% change). OPEN/RESOLVED state via `anomaly_status`. |
| [`../ml/service.go`](../ml/service.go) | `GetAnomaly()` — HTTP bridge to ml-k8s-server `/anomaly`. |
| [`../ml/entity.go`](../ml/entity.go) | `AnomalyRequest` / `AnomalyResponse` / `AnomalyInsight` wire types. |

### ml-k8s-server (Python)
| File | What |
|---|---|
| `server/controllers/anomaly.py` | Flask endpoints: `/anomaly` (template-based, predefined metrics) and `/anomaly/detect` (custom PromQL + custom time range). |
| `server/anomaly/anomaly_algo/__init__.py` | Algorithm factory → `(algo_class, config_class)` for ISOLATION_TREE (default), DB_SCAN, ZSCORE. |
| `server/anomaly/anomaly_algo/abstract.py` | Base pipeline. `TEMPLATES` dict (per-metric PromQL + tuned contamination/eps/thresholds), `process_metrics()` (main entry), zero-trimming + `check_new_workload_and_cancel()` (raises `CancelPrediction` on insufficient training data), metric-specific spike/threshold filters, `generate_insights()`. |
| `server/anomaly/anomaly_algo/isolation_tree.py` | IsolationForest: StandardScaler → train on historical → score eval window → IQR + spike filtering. |
| `db_scan.py`, `zscore.py` | Alternate algorithms. |

### Frontend (app/)
| File | What |
|---|---|
| `src/components1/k8s/details/KubernetesAnomaly.tsx` | Main listing + detail. `DrilldownChartComponent` plots metric line w/ training/eval phase markers, baseline, anomaly stars, insights list. Trigger button gated by `hasWriteAccess`. |
| `src/hooks/useTriggerAnomaly.ts` | Wraps the `anomaly_execute` mutation. |
| `src/api1/kubernetes1/index.ts` | GraphQL: `TRIGGER_ANOMALY_EXECUTE` (mutation), `anomalies_list` (grouped), `anomalies_list_v2` (detail+insights), `anomaly_type_v2` (filter opts). |
| `src/lib/anomalyInsights.ts` | Insight → human sentence, severity colors, metric value formatting. |
| `src/components1/k8s/investigate/cards/AnomalyCard.js` | Investigate-page card; rendered when `actionType==='metric_anomaly_enricher'` or `is_anomaly`. |
| `src/pages/kubernetes/details/[KubernetesDetails].jsx` | Anomaly tab (beta) + render. |

### llm-server
- `llm/llm-server/tools/tool_anomaly.go` — exposes `anomaly_execute` as a **read-only SQL tool** over the `anomaly` table (last 30 days). Used by the FinOps and Events agents to answer "any anomalies?" questions.

## Database

Table `anomaly`, created in
[`migrations/.../V474_anamoly_tables.up.sql`](../../migrations/migrations/app/1737625990010_V474_anamoly_tables.up.sql).
Notable columns / later migrations:
- `reference_value` JSONB — baseline stats + timeseries that the frontend chart replays.
- `current_value`, `anomaly_type`, `is_anomaly`, `evaluated_at`.
- `pod_name` (V496), `training_end_time` (V650), `insights` JSONB (V665), `anomaly_status` (V667, spend OPEN/RESOLVED).

Event dedup fingerprint: `anomaly-{account}-{type}-{name}-{namespace}`.

## RabbitMQ

ML path is async. Message `AnomalyProcessingMessage` (`service.go`) published with **1h TTL** to
`RabbitMqServicesAnomalyProcessingQueue`, consumed by `ProcessAnomaly()` at
`RabbitMqServicesAnomalyProcessingConcurrency`. Consumer registered in `init()`.

## Feature flags

- `FEATURE_ANOMALY_DETECTION` — tenant gate; processing is skipped if off.
- `FEATURE_ANOMALY_DETECTION_ERROR_RATE` — separate gate for error-rate anomalies.

## Gotchas

- **Directory is misspelled** `anomoly` (Go package `anomoly`); the table/type/JSON is `anomaly`/`anamoly_template.json`. Don't "fix" the path — imports across the repo depend on it.
- **Fire-and-forget**: the RPC returns before any detection runs. A `200 {status:"triggered"}` means *queued*, not *done*. Results land asynchronously in the table.
- **New-workload guard**: ML cancels prediction (`CancelPrediction`) when there's too little real training data — Prometheus `or vector(0)` prefix zeros are trimmed first for memory/CPU. Expect empty results for fresh deployments by design.
- **`trigger_anomaly_execute`** is a legacy alias still accepted by the handler; `trigger_*` is on the actions.yaml "avoid" list — use `anomaly_execute`.
- Per-metric thresholds (contamination, min spike %, absolute floors) live in `abstract.py:TEMPLATES`, **not** in the Go template JSON. Tune detection sensitivity there.

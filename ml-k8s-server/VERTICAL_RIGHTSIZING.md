# Vertical Rightsizing

How the per-container CPU/memory recommendations are produced — the ones that surface in the
product under **Optimize → Right Sizing** and that the coding agent turns into Helm-values PRs.

Code lives in [`server/recommendation/vertical_rightsizing/`](server/recommendation/vertical_rightsizing/).
The engine is organised as metric "loaders" (one PromQL query each) plus a pluggable "strategy" that
turns the loaded series into a recommendation. Some strings in the package still refer to a
command-line tool and its flags — those are inherited scaffolding, not something this service has.

For volume rightsizing see `server/recommendation/volume_rightsizing.py`; for node/cluster
rightsizing see `server/recommendation/cluster_rightsizing_recommendation.py`. Neither is covered here.

---

## The pipeline

`POST /rightsizing/vertical` returns `202` and queues the job to RabbitMQ; a single Gunicorn
worker consumes it (see [README](README.md#architecture-at-a-glance)). From there:

```
list_scannable_objects()      k8s_workloads + k8s_pods (Postgres)      -> one K8sObjectData per CONTAINER
        |
load_pods()                   kube_replicaset_owner -> kube_pod_owner  -> every pod that existed in the
        |                     (Prometheus, 336h back)                     window, live and deleted
gather_data()                 8 PromQL loaders, in parallel, batched   -> {loader_name: {pod: ndarray}}
        |                     50 pods per query
strategy.run()                NudgebeeStrategy                         -> ResourceRecommendation per resource
        |
_round_value()                ceil + minimum floors                    -> the numbers users actually see
        |
ResourceScan.calculate()      Severity + estimated_savings             -> row in `recommendation`
```

| Stage | Where |
|---|---|
| Orchestration, DB writes, archival | `vertical_rightsizing/__init__.py` |
| Scan loop, timeouts, rounding | `services/service.py` |
| Workload discovery (Postgres) | `services/nudgebee_cluster_service.py` |
| Pod discovery + query execution | `services/metrics_prometheus_service.py` |
| PromQL per metric | `metrics/cpu.py`, `metrics/memory.py` |
| The maths | `strategy/strateggy_nudgebee.py` |
| Severity thresholds | `models/severity.py` |

Datadog is supported as an alternative metrics source (`services/metrics_datadog_service.py`);
everything below describes the Prometheus path.

### Workload discovery

Not from the Kubernetes API — from `k8s_workloads` joined against `k8s_pods`, filtered to
`is_active = true`, `namespace != 'kube-system'`, and workloads that have at least one active
pod. One `K8sObjectData` is produced **per container**, so a two-container Deployment is two
independent recommendations.

### Pod discovery

`load_pods()` resolves the workload to its pods *through Prometheus*, over the full history
window: `kube_replicaset_owner` → `kube_pod_owner` (or `kube_job_owner` for CronJobs,
`kube_replicationcontroller_owner` for DeploymentConfigs). It returns **historic pods too**, marked
`deleted=True` — a Deployment that rolled 350 times in two weeks contributes all 350 pods. If
Prometheus returns nothing, it falls back to the `k8s_pods` table and tags the object
`NoPrometheusPods`.

Every downstream query then selects on `pod=~"<pod1>|<pod2>|..."`, batched 50 pods at a time.

---

## The strategy

`NudgebeeStrategy` — `strategy/strateggy_nudgebee.py`. Defaults: **336h (14 day) history,
1.25 min (75s) step**.

### CPU

```
request = max over pods of the 99th-percentile CPU usage
limit   = unset (None)
```

The 92nd/95th/97th percentiles are computed too and stored in `add_info` for the UI, but only p99
drives the recommendation. Leaving the CPU limit unset is deliberate — a CPU limit throttles
rather than kills, and throttling a latency-sensitive service is worse than letting it burst.

### Memory

```
peak    = max over pods of max_over_time(container_memory_working_set_bytes)
request = max(peak * 1.15, oom_limit_at_kill * 1.25)
limit   = max(request, high_water * 1.15)          <- high_water from container_memory_max_usage_bytes
```

with a cap on the limit described in [Why the limit is not the request](#why-the-limit-is-not-the-request).

### The "not enough data" filter

`CPUAmountLoader` / `MemoryAmountLoader` count how many samples each pod has. Pods with fewer than
`points_required` (50, ≈62 min of lifetime at the 75s step) are dropped before the maths. If that
leaves nothing, the recommendation is `"Not enough data"` rather than a guess.

Note the asymmetry: for CPU, `Job` and `CronJob` kinds are exempt from this filter; for memory they
are not. That is a quirk, not a design decision.

---

## The metrics

All eight loaders are plain PromQL in `metrics/cpu.py` and `metrics/memory.py`. `{ __CLUSTER__ }` is
substituted by the relay layer with the tenant's cluster selector.

| Loader | Metric | Used for |
|---|---|---|
| `PercentileCPULoader` ×4 | `container_cpu_usage_seconds_total` (quantile_over_time on rate) | CPU request |
| `CPUAmountLoader` | sample count | the `points_required` filter |
| `MaxMemoryLoader` | `container_memory_working_set_bytes` | memory **request** |
| `MaxUsageMemoryLoader` | `container_memory_max_usage_bytes` | memory **limit floor** |
| `MemoryAmountLoader` | sample count | the `points_required` filter |
| `MaxOOMKilledMemoryLoader` | `kube_pod_container_resource_limits` × `kube_pod_container_status_last_terminated_reason{reason="OOMKilled"}` | the OOM bump |

Every one wraps its inner expression in a **subquery** — `max_over_time(... [336h:75s])`,
`quantile_over_time(0.99, ... [336h:75s])`, `count_over_time(... [336h:75s])` — which means the inner
expression is evaluated at 75-second-spaced instants, not over raw samples.

---

## Why the limit is not the request

This is the part worth understanding before changing anything here.

`container_memory_working_set_bytes` is a **gauge sampled at the scrape interval**. cAdvisor scrapes
every ~40s on dev, ~14s on prod. A peak that rises and falls between two scrapes is simply never
recorded. Measured on the same pod on the same day (dev):

| how the peak is measured | result |
|---|---|
| what the strategy computes, `[1d:75s]` | 278.7 Mi |
| every raw sample, `working_set[1d]` | 278.7 Mi |
| `container_memory_max_usage_bytes[1d]` (cgroup high-water) | **648.1 Mi** |

The subquery step is not the problem — raw samples give the same answer. The **scrape sampling
itself** is the blind spot. Across namespace `nudgebee`: true/sampled peak p50 **1.51x** on dev
(p50 1.17x on prod, where scrapes are 3× finer), and 28 of 34 dev containers exceed the 15% buffer.
The buffer is smaller than the measurement error for most workloads.

`container_memory_max_usage_bytes` is the high-water mark the **kernel** maintains for the cgroup.
It cannot miss a peak. It is not a safe basis for the *request*, because it counts reclaimable page
cache — but it is exactly the right floor for the *limit*, for one decisive reason:

> **Savings are computed from requests only** (`__init__.py:calculate_container_savings`). Lowering a
> memory limit yields $0. A limit set equal to the request therefore takes 100% of the OOMKill risk
> for 0% of the savings.

That is not hypothetical. In August 2026 a rightsizing recommendation set `app-dev` to
`request = limit = 421Mi`; the container's real high-water was 648Mi and every pod entered a
CrashLoop (`next-server`, `anon-rss:425548kB`, 28 node `OOMKilling` events).

### The ratchet guard

Page cache grows to fill whatever limit a container is given, so a naive
`limit = high_water * 1.15` would inflate a cache-saturated container's limit by 15% on every scan.
The guard: **when no OOMKill was detected and `high_water <= allocated_limit`, the floor is capped
at the allocated limit.** A container that never breached its limit and never OOMKilled has proven
it needs no more than it has. The cap can only bind when the limit already exceeds the high-water,
so "never recommend below the observed peak" still holds.

Consequence worth knowing: the limit is only ever raised above what is allocated today for a
container already sitting **above 87% of its limit** — that is arithmetic (`high_water * 1.15 > limit`
implies `high_water > 0.87 * limit`), not a tuned threshold.

### Graceful degradation

`MaxUsageMemoryLoader` sets `warning_on_no_data = False`. Where the metric is unavailable (older
cAdvisor, some cgroup v2 setups), `high_water` is 0 and the limit falls back to the request — the
behaviour that existed before the loader. Verified present on both GKE dev and EKS prod.

---

## Rounding and floors

Applied in `services/service.py:_round_value`, **after** the strategy — the strategy's raw output is
not what users see.

- CPU is rounded up to the millicore, memory up to the MiB.
- Floors: `cpu_min_value` **100m**, `memory_min_value` **100Mi** (`models/config.py`).
- Escape hatch: if the rounded value is below the floor *and* the container's current allocation is
  also below the floor, the current allocation is used instead — so a deliberately tiny sidecar is
  not force-upgraded to 100Mi.

The 100Mi floor is load-bearing and easy to forget when reasoning about small containers: it
silently rescues under-recommendations below 100Mi. Any analysis of raw strategy output that
ignores it will overstate the number of unsafe recommendations.

---

## Severity and savings

`models/severity.py`, on the **request** delta:

| | GOOD | OK | WARNING | CRITICAL |
|---|---|---|---|---|
| CPU (cores) | < 0.1 | ≥ 0.1 | ≥ 0.25 | ≥ 0.5 |
| Memory | < 100 Mi | ≥ 100 Mi | ≥ 250 Mi | ≥ 500 Mi |

`estimated_savings` is monthly dollars, `(allocated_request - recommended_request) × unit_cost × 24 × 30`,
summed per container. **Limits never contribute.**

---

## Settings

`NudgebeeStrategySettings` — all overridable per scan via `other_args`.

| Setting | Default | Meaning |
|---|---|---|
| `history_duration` | 336 (hours) | Look-back window |
| `timeframe_duration` | 1.25 (minutes) | Subquery step |
| `cpu_percentile` | 99 | Percentile driving the CPU request |
| `cpu_percentile_92/95/97/99` | 92/95/97/99 | Extra percentiles stored in `add_info` |
| `memory_buffer_percentage` | 15 | Buffer over the sampled peak → memory request |
| `memory_limit_buffer_percentage` | 15 | Buffer over the cgroup high-water → memory limit floor |
| `use_oomkill_data` | true | Bump memory when OOMKills are seen |
| `oom_memory_buffer_percentage` | 25 | Bump size, applied to the limit **at the time of the kill** |
| `points_required` | 50 | Minimum samples per pod (≈62 min at the default step) |
| `allow_hpa` | true | Still recommend when an HPA targets the same resource |

`add_info` on each memory recommendation carries the diagnostics: `actual_recommended_request`
(the raw sampled peak), `memory_high_water_mark`, `memory_sampling_gap_ratio` (high-water ÷ sampled
peak — a value well above 1 means the sampled peak is not trustworthy for that container), and the
OOM details (`oomkill_detected`, `oom_affected_pods`, `oom_memory_limit_at_kill`, …).

---

## Running it against a real Prometheus

The strategy and loaders can be driven directly, without RabbitMQ, Postgres or the relay — useful
for "what would we recommend for X, and is that number defensible?".

```python
# Point a stub at any Prometheus-compatible endpoint and swap it in.
class LocalProm:
    def custom_query(self, query, start_time=None, end_time=None): ...        # GET /api/v1/query
    def custom_query_range(self, query, start_time, end_time, step): ...      # GET /api/v1/query_range
    # both must strip the "__CLUSTER__" placeholder from the query

svc = PrometheusMetricsService(config, "local", ThreadPoolExecutor(4))
svc.prometheus = LocalProm()
strategy = NudgebeeStrategy(NudgebeeStrategySettings())

obj.pods = await svc.load_pods(obj, strategy.settings.history_timedelta)
metrics = await svc.gather_data(obj, strategy, strategy.settings.history_timedelta,
                                step=strategy.settings.timeframe_timedelta)
result = strategy.run(metrics, obj)
```

Build the `K8sObjectData` by hand (kind, namespace, name, container, current allocations) to skip
the Postgres-backed workload discovery. Importing the package requires `ML_INFERENCE_DATABASE_URL`,
`RELAY_SERVER_SECRET_KEY` and `RELAY_SERVER_URL` to be set to *something* — they are read at import
time — but nothing needs to be reachable.

To sanity-check a memory recommendation, compare it against the cgroup high-water mark for the same
pods and window:

```promql
max(max_over_time(container_memory_max_usage_bytes{namespace="ns",container="c",pod=~"..."}[14d]))
```

A recommendation below that number is one the container has already disproved.

---

## Known limitations

- **The memory request is still sampled.** Only the limit is protected by the high-water floor. A
  request below true peak usage does not OOMKill anything, but it does understate the workload to
  the scheduler and makes eviction under node pressure more likely.
- **The OOM bump is reactive and weak.** `limit_at_kill × 1.25` converges on a container whose true
  peak is far above its sampled peak only after several CrashLoop generations. The high-water floor
  now covers most of these cases before an OOM happens at all.
- **The high-water mark is limit-dependent.** It counts page cache, which expands to fill the limit,
  so it reads ~100% of the limit for cache-heavy containers (qdrant, the vulnerability servers). The
  ratchet guard bounds the consequence but cannot distinguish "cache filled the limit" from
  "genuinely near OOM" — cAdvisor exposes no never-miss anonymous-memory metric.
- **HPA is not really handled.** `allow_hpa` defaults to true, so recommendations are emitted even
  when an HPA targets the same resource, where they may fight each other.
- **Retention bounds the window.** The default look-back is 14 days; if Prometheus retention is
  shorter, the scan silently uses whatever exists.

# logs_default agent benchmark

Tests the `logs` parent agent in [agent_log.go](../../../../../llm-server/agents/agent_log.go),
which wires [`logs_default`](../../../../../llm-server/agents/agent_log_default.go)
(provider path — Datadog / Signoz / Loki) as primary and `kubectl_log` as
fallback.

## What this suite covers

| Fixture | Arm | What it exercises | Expected `AgentName` |
|---|---|---|---|
| `01_kubectl_fallback_crashpod` | fallback | No provider — parent skips `logs_default` | `kubectl_log` |
| `02_kubectl_fallback_oom` | fallback | OOMKilled + pre-crash stdout via `kubectl logs --previous` | `kubectl_log` |
| `03_loki_http_errors` | provider | Loki + Promtail sidecar happy path, HTTP 500 filter | `logs_default` |
| `04_loki_empty_punt_to_kubectl` | fallback-handoff | Loki reachable but empty → `Failed` → kubectl fallback | `kubectl_log` |
| `05_loki_time_range_last_hour` | provider | `range` parameter extraction ("last 1 hour") | `logs_default` |
| `06_loki_multi_condition` | provider | Multi-label where-clause (`namespace AND app AND status`) | `logs_default` |
| `07_loki_error_recovery_bad_label` | provider | 2-retry recovery loop at [agent_log_default.go:366](../../../../../llm-server/agents/agent_log_default.go#L366) | `logs_default` |
| `08_loki_resource_search_heuristic` | provider | `resolveResource` slug → real label mapping | `logs_default` |
| `09_kubectl_multi_container` | fallback | kubectl picks correct container in a multi-container pod | `kubectl_log` |
| `10_loki_json_structured_logs` | provider | JSON field extraction (`level`, `service`, `duration_ms`) | `logs_default` |

## How the Loki fixtures work

Each Loki fixture is fully self-contained:

1. `before_test`:
   1. Creates an isolated namespace (`log-default-<NN>`).
   2. Deploys Loki via [nubi/shared/loki.yaml](../nubi/shared/loki.yaml).
   3. Applies the fixture's `promtail-config.yaml` (a ConfigMap).
   4. Applies the fixture's `manifest.yaml`, which runs the app **plus** a
      Promtail sidecar that tails `/var/log/app/app.log` (a shared
      `emptyDir`) and pushes to `loki:3100` inside the namespace.
   5. Starts `kubectl port-forward -n <ns> svc/loki 3100:3100` in the
      background so the LLM server (running on the host) can reach Loki at
      `http://localhost:3100`.
2. `after_test` kills the port-forward and deletes the namespace.

Fixtures reuse a single local port (3100). Because pytest serialises test
runs, there's no collision — each fixture's `after_test` tears the forward
down before the next `before_test` starts a new one.

## Required benchmark configuration

The account running the benchmark must have `grafana/loki` configured as its
log provider pointing at the local port-forward:

```yaml
# via the global TOOL_CONFIG env var at benchmark run time
toolsets:
  grafana/loki:
    enabled: true
    config:
      url: http://localhost:3100
      labels:
        pod: pod_name
        namespace: namespace
        app: app
```

Kubectl-fallback fixtures (01, 02, 09) and the empty-Loki fixture (04) will
still work regardless of this toolset — they either skip provider lookup
entirely or depend on the empty-result handoff.

## Running

```bash
# From llm/benchmark/
pytest llm/agents/log_default/ -m benchmark -v

# Single fixture
TEST_INDICES=0 pytest llm/agents/log_default/ -m benchmark -v
```

## Adding new fixtures

One directory per case under `fixtures/`. Required: `test_case.yaml` with
`user_prompt`, `expected_output`, `before_test`, `after_test`, `tags`.
Optional: `manifest.yaml`, `promtail-config.yaml`.

Label every namespace `benchmark=true` so the global
[nubi/scripts/nuke.sh](../nubi/scripts/nuke.sh) can clean up after an aborted
run.

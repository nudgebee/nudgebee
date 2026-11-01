# Grafana Tempo integration — design

**Date:** 2026-09-29 · **Status:** Revised after `/challenge` (REVISE) — awaiting review ·
**Branch:** `feature/grafana-tempo-integration`

## 1. Intent

A customer running the Grafana stack (Loki + Prometheus/Mimir + Tempo) should be able to connect
Tempo to Nudgebee and see its **traces** in the existing Kubernetes Traces tab — listing, filters,
waterfall, grouped per-operation stats — labelled **Grafana Tempo** and filtered by the same
Kubernetes workload/namespace labels as every other provider.

| Asked | Agreed scope |
|---|---|
| "logs, traces and metrics from Tempo" | Tempo stores **traces only**. v1 is **traces only** (user decision, 2026-09-29: "leave the span metrics for now, just focus on traces"). Logs stay with Splunk/Loki; metrics with VictoriaMetrics/Prometheus. |
| "correct labels should be displayed" | Provider badge reads "Grafana Tempo"; spans carry Nudgebee's canonical labels (workload, namespace, destination, status, http status), not raw Tempo attribute paths. |
| Trace grouping | **In v1**, inside the Traces tab: Tempo computes it server-side with TraceQL metrics; degrades to an error in the grouped view only. |

Approach: **query Tempo directly over its HTTP API** (the pattern Splunk Enterprise, CubeAPM and
OpenObserve use). Rejected: Grafana datasource proxy (second auth model, no customer asking);
agent relay actions (needs nudgebee-agent changes and a live agent); reusing the Jaeger source via
`tempo-query` (Jaeger's user mode is a stub, agent mode is relay-bound, grouping calls a Jaeger SPM
endpoint tempo-query does not serve).

### Out of scope (v1)
Tempo as a metrics provider (no `getMetricsSource` case — see §8 for why and the future path) ·
Tempo as a log provider · utilisation · service map (feasible later via TraceQL structural `>>`
queries, which carry a namespace on both ends) · alert rules / webhooks · Grafana-proxy URLs ·
llm-server changes (the default traces agent sends canonical where-clauses, translated server-side).

## 2. Architecture

```
UI Traces tab ──traces_*──▶ api-server observability ──getTraceSource("grafana_tempo","user")──▶ GrafanaTempoTraceSource ──HTTP──▶ Tempo
Integrations page ──────────▶ integrations/grafana_tempo.go (schema, validate, test connection, stored config)
```

A small unexported client in the trace source owns URL joining, auth headers, `X-Scope-OrgID`,
TLS skip, the 30 s timeout, window clamping and error mapping.

| Purpose | Tempo endpoint | Tempo 3.0.3 limit |
|---|---|---|
| Test connection | `GET /api/echo`, then `GET /api/v2/search/tags` (proves auth + tenant) | — |
| Span search | `GET /api/search?q=<TraceQL> with (most_recent=true)&start&end&limit&spss` | window ≤ **168h** |
| Trace waterfall | `GET /api/v2/traces/<traceID>?start&end` | — |
| Label keys / values | `GET /api/v2/search/tags`, `/api/v2/search/tag/<scoped.tag>/values?q=` | — |
| Grouping only | `GET /api/metrics/query` (instant TraceQL metrics) | window ≤ **24h** |

## 3. Components

### 3.1 Registration
- **Type string `grafana_tempo`**, category `observability_platform`, description
  "Grafana Tempo (distributed traces)". Category injects `log_label_mappings`, but
  `withLogLabelMappingOffer` (`api/actions_integration.go:803`) strips it for non-log sources, so
  no log settings appear on the form.
- Migration via `./api-server/migrations/new-migration.sh add_grafana_tempo_integration_type`
  (idempotent insert into `integration_types`; down deletes it; regenerates `atlas.sum`).

### 3.2 `api-server/services/integrations/grafana_tempo.go` (template: `jaeger.go` + `splunk_enterprise.go` auth pattern)
Schema, `Testable: true`, required `[tempo_url, tempo_auth_type]`:

| Key | Type | Notes |
|---|---|---|
| `integration_name`, `account_id` | std | as every integration |
| `tempo_url` | string | base URL incl. any path prefix; `IsTestable` |
| `tempo_auth_type` | enum `none`\|`basic`\|`bearer`, default `none` | Tempo has no built-in auth |
| `tempo_username` / `tempo_password` | string / **encrypted** | ShowWhen/RequiredWhen `basic` |
| `tempo_token` | **encrypted** | ShowWhen/RequiredWhen `bearer` |
| `tempo_tenant_id` | string, optional | sent as `X-Scope-OrgID` |
| `tempo_insecure_skip_verify` | bool | |
| `core.DefaultTraceProvider` | bool | **the only default flag** |

`TestConnection` maps failures to actionable messages: 401/403 → auth; 404 on `/api/echo` →
"URL is not a Tempo query endpoint (check the path prefix)"; TLS error → skip-verify hint;
401 "no org id" → tenant hint. `GetGrafanaTempoConfig(ctx, accountId)` with the empty-account
guard; secrets via `common.Decrypt`.

### 3.3 `observability/grafana_tempo_traces.go` — `GrafanaTempoTraceSource`

**Label mapping** (`GetLabelMapping`) — canonical field → TraceQL:

| Canonical | TraceQL |
|---|---|
| `trace_id` · `span_id` · `parent_id` | `trace:id` · `span:id` · `span:parentID` |
| `span_name` | `name` |
| `workload_name` | `resource.service.name` |
| `workload_namespace` | `resource.k8s.namespace.name` |
| `destination_workload_name`, `destination_name` | `span.net.peer.name` |
| `http_status_code` | `span.http.status_code` |
| `resource` | `span.http.target` |
| `status_code` | `status` (`ok`/`error`/`unset`; the UI's Ok/Error value is normalised to these) |

Display (not filtering) also reads `resource.k8s.pod.name`, `span.peer.service`,
`span.server.address`, `span.http.response.status_code`, `span.url.path` as fallbacks.
Unsupported, stripped rather than empty-resulting (as Splunk/OpenObserve):
`destination_workload_namespace`, `destination_workload_zone`.

**Where clause → TraceQL:** one span selector `{ a && b && … }`. Operators `_eq =`, `_neq !=`,
`_like`/`_contains` → `=~ "(?i).*x.*"`, `_nlike`/`_ncontains` → `!~`, `_gt/_gte/_lt/_lte`,
`_in` → `=~ "a|b"`; every regex value is escaped, strings quoted and escaped, numerics unquoted.
`GetSupportedOperators` returns exactly this set.

**Raw TraceQL (Code mode):** used as the selector. If it contains no `select(`, the source appends
the standard `| select(…)`; if it already selects, it is sent unchanged (columns it didn't select
render empty). `with (most_recent=true)` is appended when absent. `GetQuery` returns exactly the
string sent.

**Window clamping:** search windows longer than 168h are clamped to the most recent 168h and
grouping windows to the most recent 24h; every clamp is logged (Info, naming the requested and
effective window). Surfacing it in the UI is not done in v1.

**Methods:**
- `QueryTraces` — search with `| select(resource.service.name, resource.k8s.namespace.name,
  resource.k8s.pod.name, span.net.peer.name, span.peer.service, span.http.status_code,
  span.http.target, span.url.path, status, kind, span:parentID, name) with (most_recent=true)`.
  **Paging:** newest-first; requests `limit = min(offset + pageSize, cap)` traces with
  `spss = min(limit, 100)` (Tempo's `max_spans_per_span_set`, §7), flattens `traces[].spanSets[].spans[]`, sorts by the requested order (default start
  time desc), slices `[offset, offset+pageSize)`. Cap = 1000 spans (matches the largest caller, the
  LLM path). Beyond the cap, the page is empty rather than a re-shuffled sample.
  Each span → `OpenTelemetryTrace` with `TraceSource: "grafana_tempo"`; `StatusCode`,
  `HTTPStatusCode`, `Workload*`, `Destination*` derived with `jaegerSpanToOTEL`'s heuristics.
- `QueryRootSpansByTrace` / `CountTracesByTrace` — shared `queryRootSpansViaSpans` /
  `countTracesByTraceEstimate` (`common.go:356-369`); roots are spans whose `span:parentID` is
  `0000000000000000`.
- `CountTraces` — **estimate** (`Count: -1`, the UI's existing estimate path), not TraceQL metrics,
  so the listing never depends on the metrics engine or its 24h cap.
- `QueryGroupedTraces` — three instant TraceQL-metrics queries, run concurrently, sharing
  `by (resource.service.name, resource.k8s.namespace.name, name)`: `count_over_time()`,
  `{… && status = error} | count_over_time()`, `quantile_over_time(duration, .95, .99)`; merged per
  key into `TraceGroupingValues` (`MaxLatency` left 0 unless the grouping UI renders it — checked
  during implementation). Any failure returns an error **for the grouped view only**.
  `QueryGroupedTracesCount` = number of merged keys.
- `QueryTracesHeatmap` — `/api/v2/traces/<id>`, OTLP JSON → spans → `OpenTelemetryTraceHeatMap`.
- `QueryLabels` / `GetLabelValues` — tags / tag-values endpoints; canonical names translated both
  ways through the mapping.

**Capabilities** (`allProviderCaps["grafana_tempo"]`): TraceGrouping ✅, Heatmap ✅, RawQuery ✅,
ServiceMap ❌.

### 3.4 api-server wiring
`service.go`: case in `getTraceSource`; `allProviderCaps` entry. **No `getMetricsSource` case.**
`trace_source_canonical_fields_test.go`: pin the new source (case + `registered` list).

### 3.5 Frontend (`app/`)
- Rename tile key `GRAFANA-TEMPO` → `GRAFANA_TEMPO` (integration.jsx `SECTIONS_CONFIG`, common.ts
  label, integrationDescriptions.ts, CloudIcon.jsx, images index) and remove it from
  `DISABLED_PROVIDERS`. No persisted rows use either spelling (checked by the adversarial review).
- Add `grafana_tempo` to: `api1/account/index.ts` `integrationTypes`; `account-form.jsx` switch;
  `ListIntegrations.jsx` primary field `tempo_url`; `IntegrationDynamicFormModal.jsx`
  `TRACE_INTEGRATIONS`; `navSearchPages.ts` `integrationProviders`.
- Badge text is `snakeToTitleCase("grafana_tempo")` = **"Grafana Tempo"** — no special case.

## 4. Error handling
- Tempo unreachable / 5xx / 4xx → the tab's existing error UI with Tempo's message; never an empty success.
- Grouping failure (metrics engine disabled on Tempo 2.x without `local-blocks`, or rejected window)
  → error in the grouped view only; listing, filters and waterfall unaffected.
- Unknown canonical field → 400 naming it (scoped `resource.*`/`span.*` names pass through); the named unsupported fields are stripped; unsupported operator → 400 naming it.
- 30 s timeout per request; span cap 1000.

## 5. Test data (shared dev cluster, `splunk` namespace)
- Tempo 3.0.3 deployed (`svc/tempo`, no auth). Port-forward
  `kubectl port-forward -n splunk svc/tempo 3200:3200` → integration URL `http://localhost:3200`, auth `none`.
- Extend `telemetry-generator` (ConfigMap `telemetry-generator-src`) with an OTLP/HTTP JSON exporter
  posting every trace it builds to `http://tempo:4318/v1/traces` with the same resource attributes
  (`service.name`, `k8s.namespace.name`, `k8s.pod.name`, `k8s.cluster.name`) and span attributes
  (`http.*`, `net.peer.name`, error status). Splunk and Tempo then hold identical traces — the
  provenance cross-check. Gated by `EMIT_TEMPO` (default true), stdlib only. Dry-run, then apply.

## 6. Testing & acceptance criteria

**Unit (Go, `make validate` in api-server/services):**
- `integrations/grafana_tempo_test.go` — schema shape, validate per auth type, test-connection
  error mapping (httptest server).
- `observability/grafana_tempo_traces_test.go` — where→TraceQL for every operator + escaping; raw
  query `select`/`most_recent` appending; search flattening incl. `"nil"` values and root detection;
  paging slice at offsets 0 / mid / beyond cap; window clamping; OTLP trace-by-id conversion;
  grouping merge of the three queries and grouped-only failure; unsupported-field stripping.
- Pinning test updated. **Frontend:** `npm run lint2 && npm run test` (navSearchPages drift, CloudIcon).

**Acceptance (observed in the local UI against the real dev Tempo, account `k8s-dev`; screenshots in the PR):**
1. Integrations → Grafana Tempo tile enabled; form shows the fields above with only a Default
   Traces Provider box; Test Connection succeeds against `http://localhost:3200`.
2. Traces tab badge reads **"Grafana Tempo"**; spans list the generator's workloads with workload /
   namespace / destination populated (e.g. `frontend` / `demo` / `payments`); filtering namespace
   `demo` returns only `demo` spans; an error span shows status Error + HTTP 503.
3. Page 2 contains different, older spans than page 1 (no overlap).
4. Clicking a trace opens the waterfall with the full chain (frontend → payments → ledger).
5. Grouped view shows per-operation count, error count, p95/p99 from Tempo; selecting "Last 7 days"
   still lists spans (grouping clamped to 24h, listing to 7d).
6. **Provenance:** the same trace ID is found in Splunk (`index=otel_traces trace_id=<id>`) and in
   Tempo; switching the account's trace default back to Splunk changes the badge.
7. Logs and Metrics tabs unchanged (Splunk / VictoriaMetrics).

## 7. Verified Tempo 3.0.3 facts (probed 2026-09-29 against the dev Tempo)
- Search spans always carry `spanID`, `startTimeUnixNano`, `durationNanos`; everything else —
  **including the span `name`** — only via `select()`. Selected attributes come back under the
  **unscoped** key (`service.name`); a selected `name` comes back as the span's top-level `name`
  (the converter also accepts a `name` attribute). Without `name` in the select, every listed span
  has an empty span name.
- `spss` (spans per span set) above Tempo's default `max_spans_per_span_set` of 100 is rejected
  (400 "spans per span set exceeds 100"): paging past 100 spans raises `limit` only, `spss` stays ≤ 100.
- Search trace ids drop leading zeros (a 30- or 31-char hex id is common); trace-by-id decodes to the
  full 32 chars, so search ids are left-padded to 32 (span ids to 16). `trace:id = "0…"` matches
  either form.
- `/api/v2/traces/<id>` with a `start`/`end` window that misses the trace answers 200 `{"trace":{}}`;
  without `start`/`end` it searches all blocks. The waterfall sends no window unless the caller did,
  and an empty trace is reported as "not found".
- `kind` is an unquoted enum (`kind = server`); `kind = "server"` is a 400. A missing numeric
  attribute is matched with `nil` (`span.http.status_code != nil`); `!= ""` matches no span.
- A pipeline stage must precede the `with (…)` hint: `{…} with (…) | select(…)` is a parse error.
- A selected attribute absent on a span comes back as the string `"nil"` — treated as empty.
- `kind` and `status` arrive as attributes (`"server"`, `"unset"|"ok"|"error"`).
- `select(span:parentID)` works; a root span reports `0000000000000000`.
- `with (most_recent=true)` is accepted.
- Search window > 168h → 400 "range … exceeds 168h0m0s"; metrics window ≥ 24h → 400
  "exceeds the maximum allowed duration of 24h0m0s".
- TraceQL-metrics series labels keep the **scoped** key (`resource.service.name`).
- `/api/v2/traces/<id>` returns OTLP JSON with `traceId`/`spanId`/`parentSpanId` **base64**-encoded
  (`3bpdDNzCQdc=`), `kind` as `SPAN_KIND_*` and `status.code` as `STATUS_CODE_*` strings, whereas
  search returns hex ids — the waterfall converter must decode base64 to hex.
- Posting OTLP to a local `localhost:4318` hits the local stack's Docker otel-collector (it binds
  `0.0.0.0:4318`); forward Tempo's OTLP port to `14318` instead.
- `/api/v2/search/tags` returns `{scopes:[{name:"resource"|"span"|"intrinsic", tags:[…]}]}`.

## 8. Decision record — why no Tempo metrics source
`/challenge` (2026-09-29) found that any provider `getMetricsSource` accepts becomes a first-class
metrics provider in the switcher, dashboards (`panelProviders.ts:146`), AI query generation (which
emits PromQL from the account default), the Builder (PromQL-only) and alert-rule capability — none
of which can speak TraceQL. The user chose traces only. When span metrics are wanted, the
recommended path is Tempo's metrics-generator remote-writing `traces_spanmetrics_*` into the
customer's Prometheus/Mimir, surfaced by the existing Prometheus integration with zero new code.

## 9. Risks
- Grouping issues three concurrent metrics queries per view (bounded by the 30 s timeout); a single
  combined query is a later optimisation.
- Deep paging stops at 1000 spans; beyond that the UI shows no further pages (the count is an estimate).

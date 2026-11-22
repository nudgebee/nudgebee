# CPU SLO with breach forecasting — spec (v7)

Ticket: _none yet_ (to be filed via `/create-issue` before the first PR).

## Review history

- **v1:** REDESIGN.
- **v2–v5:** the forecast maths, the metrics-API input and the event lifecycle went through four
  rounds and came out PROCEED.
- **v6:** opt-in per service, Check now plus a Watch toggle. Round 5 returned REVISE; its fixes
  were applied and it came out PROCEED.
- **v7 (user direction):**
  - **Opt-in becomes a CPU SLO** configured on the service, replacing the Watch toggle and its
    table and API actions.
  - **The CPU SLO is threshold-style:** "must stay under X%". Nudgebee alerts when the trend
    projects that X will be crossed.

  The maths (D4), metrics input (D2), Check now (D10), Nubi tool (D12) and the flag (D15) carry
  over from v6. The *(r5)* fixes still apply.
- **Round 6 (v7):** REVISE, with five fixes:
  - notification routing to the SLO category
  - per-type removal and a threshold guard
  - non-NULL row shape plus the metadata column
  - a dedicated close path
  - the overlap lock and fetch timeout

  They are folded in and marked *(r6)*. That makes the verdict **PROCEED**.
- **v8 (user direction, PR review):** the engine becomes a **generic metric forecast** that SLO
  and other work can reuse, and the tenant flag goes. `/challenge` returned REVISE (presets
  only, no ad-hoc metrics or direction); with those changes, PROCEED:
  - **Presets** (`forecast.Presets`): `cpu_throttling` and `cpu_saturation`, both also SLO
    types. Each names its value series, the limit and usage it is measured against, its unit
    and rounding, and its default threshold. Adding a metric is one entry.
  - **Memory is deferred.** A `memory_saturation` preset was built and then dropped (user
    decision): as a workload-wide ratio (total working set ÷ total limit) it dilutes the one
    container an OOM kill hits, and an OOM-looping container's sawtooth never reads as a
    climb. It needs a worst-container value first.
  - **RPC `forecast_generate`** and **Nubi tool `metric_forecast`** take a `metric` preset
    name; they replace `forecast_generate_cpu_throttling` and `cpu_throttling_forecast`.
    The tool is no longer preloaded in the k8s core.
  - **Generic result fields:** `no_limit` (was `no_cpu_limit`); `per_pod_limit`,
    `per_pod_usage`, `suggested_limit` with a `limit_unit` (`cores` for both presets).
  - **No flag** (D15 removed): a CPU SLO is opted into per workload; `nb_forecast_providers=""`
    is the server-level kill switch. Settings are renamed `nb_forecast_*`.
  - It is a threshold-crossing forecast, not an anomaly model; anomaly baselines stay out of
    scope.

## Problem

Nudgebee hears about CPU throttling only after the fact, when `CPUThrottlingHigh` fires. Today's
SLOs (availability and latency) alert on burn rate, which also only starts once bad minutes
begin. A service whose throttling climbs 10% → 15% → 50% → 89% over a week gives warning on day 2
that nothing acts on.

## Goal

1. **The user configures a CPU SLO on a service** in the existing SLO dialog. Example: "throttling
   must stay under 25%" and/or "CPU usage must stay under 90% of its limit".
2. **Nudgebee checks that SLO every hour** and raises an alert when the trend projects a breach,
   e.g. "order-service will cross 25% throttling in ~38h". The alert carries the evidence.
3. **Check now:** from the SLO dialog or from Nubi, see the forecast for any service on demand.

## Terms

- **CPU SLO:** an SLO of type `cpu_throttling` or `cpu_saturation`, stored as an `slo_config` row
  like availability and latency. Its **threshold** is the user's limit.
- **`CPUThrottlingPredicted` / `CPUSaturationPredicted`:** the *event type names* of the warnings.
  They are events Nudgebee creates, **not alert rules**; nothing is installed in Prometheus.

## Decisions

- **D1. Two CPU SLO types, each with a user-set threshold.**
  - **`cpu_throttling`**: the threshold is the maximum throttled share of CFS periods.
    Default **25%**, matching `CPUThrottlingHigh`.
  - **`cpu_saturation`**: the threshold is the maximum CPU usage as a share of the limit.
    Default **90%**.

  The threshold is stored in `slo_config.threshold` (percent) with `method = 'forecast'`. *(r6)*
  CPU rows write **explicit non-NULL** `goal = 0` and `window = 3600`, the hourly cadence.
  `DBSLOConfig.Goal` and `Window` are non-nullable, and one NULL row would fail `ExecuteSLO`'s
  Select for the whole account. `CreateOrUpdateSLOConfig` refuses a forecast-type threshold that
  is not in (0, 100]. A service with no CPU limit can't have a CPU SLO: saving runs Check now and
  refuses with the reason (*r5* rule).

  *(r6)* All forecast-specific SLO code lives in one file, `slo/forecast_slo.go`, behind one
  helper, `isForecastConfig(c)`. Call sites branch once and delegate; no `cpu_*` checks are
  scattered through the package.
- **D2. Input: the services-server metrics API** (`FetchMetricUtilisation`), unchanged from v6:
  - **Keys:** `cpu_limit`, `pod_count`, and two new ones: `cpu_throttling_ratio` (0–1) and
    `cpu_usage_limited`, usage of only the containers that have a CPU limit. *(Review fix.)*
    Plain `cpu_usage` counts every container, so a workload whose main container has no
    limit and whose sidecar has one got saturation = all usage ÷ the sidecar's limit.
  - **Pod scope:** `k8s_pods` rows with `last_seen` inside the window.
  - **Hourly buckets.**
  - **Provider list:** `NB_FORECAST_PROVIDERS`, default `prometheus`. Datadog is next, once
    its keys meet the contract.
- **D3. One engine.** `forecast.Evaluate(ctx, account, namespace, kind, name, condition,
  threshold, endTime)` returns a `ForecastResult`:
  - `status`, one of: `projected`, `not_projected`, `breached`, `insufficient_history`,
    `no_limit`, `provider_unsupported`, `error`
  - the current value, slope per day, `eta_hours` and `reason`
  - the daily table
  - the suggested per-pod limit
  - `hours_of_data` and `evaluated_at`

  The SLO run (D11), Check now (D10) and the Nubi tool (D12) all call it.
- **D4. The fit** is unchanged:
  - a pure Go function
  - cut at the last change of the per-pod limit
  - at least 48 hourly buckets
  - a least-squares fit over whole days of a line **plus the daily and half-daily harmonics**;
    the line's slope is the trend *(review fix)*
  - `ETA = (threshold − fitted line at the last bucket) / slope`, never below 0 *(review fix)*

    Why both fixes: a whole-day window cancels a day/night cycle's mean but not its
    covariance with time, so a plain line's slope depended on the hour the window ended;
    an 8-point cycle on a 0.4 pp/h climb moved the ETA by 139 h between a peak and a
    trough. "Now" measured as the last-6h mean carried the same cycle. A level crossing
    the 48 h line twice a day would raise a new HIGH alert daily, since level changes are
    exempt from the cooldown. The last-6h mean still decides **breached** and is the
    "now" shown in titles.
  - guards: the slope is positive, the value is below the threshold, and the rise is at least
    the minimum
  - no maximum ETA
- **D5–D7. Event lifecycle**, unchanged except for cadence and naming:
  - **Levels:** HIGH for an ETA of 48 h or less, which notifies and triggers Nubi
    auto-investigation; MEDIUM otherwise.
  - **Fingerprint:** `cpuforecast|{account}|{namespace}|{kind}|{workload}|{type}|{level}`.
    **Finding ID per firing:** `{fingerprint}-{start_unix}`.
  - **Damping:** close after two consecutive clean runs (`updated_at` at least 90 min old at the
    hourly cadence), and a 24 h re-fire cooldown that ignores closes tagged `unwatched`.
  - **Escalation:** MEDIUM is resolved before HIGH is inserted.
  - **Close:** SQL plus `notify_resolved`, setting `nb_status = 'RESOLVED'`.
  - **Event:**
    - source `forecast`
    - `AggregationKey` `CPUThrottlingPredicted` or `CPUSaturationPredicted`
    - title with the ETA, e.g. `order-service: CPU throttling projected to reach 25% in ~38h
      (now 15%)`
    - labels `slo_config_id`, `workload_kind` and `container` (when there is one)
    - three evidence cards from the same keys
  - **Close triggers:**
    - a clean close;
    - `breached`: the threshold was reached, and the breach alert below takes over;
    - the SLO is **disabled or deleted**, closed with `close_reason=unwatched`.
  - **Breach alert** *(user direction, during testing)*. When the current value reaches the
    user's threshold, the warnings close and a HIGH `CPUThrottlingSLOBreached` /
    `CPUSaturationSLOBreached` event opens (fingerprint level `BREACHED`). The 25%
    `CPUThrottlingHigh` cannot stand in for an SLO set at 75%.
    - Its evidence keeps the fitted slope and the daily table, so Nubi's investigation can
      describe the climb that led to the breach.
    - It closes after two clean runs below the threshold, and the 24 h re-fire cooldown
      applies.
    - It is wired like the warnings: fix path, `isDerivedSignal`, copy-library names and the
      `list_events` hint.
- **D8. Last result per SLO.** A new nullable column, `slo_config.last_forecast jsonb`, holds the
  latest `ForecastResult`, so the SLO list can show status and ETA without a live call. CPU SLOs
  **don't write `slo_report`**: its error-budget and attainment columns don't apply, and
  `slo_report_observation_v2` would mix them into availability and latency attainment.
  - *(r6)* PR 1 adds `last_forecast` (`ColumnDefinitionTypeJson`) to `slo_config_v2` in
    `api-server/services/query/metadata.go` and to the api1 SLO query
    (`app/src/api1/kubernetes1/index.ts`).
  - *(r6)* **Known and accepted:** the cluster and application-group SLO summary widgets count a
    workload with only a CPU SLO as "configured" and never as "firing", because those widgets
    read `slo_report`.
- **D9. Fixes and triage.**
  - The resolution branch accepts both new keys, falling back to `labels.container`.
  - The UI's resolvable keys include them.
  - Both keys are added to `isDerivedSignal`.
  - The copy-library gets display names.
  - The deliberately-not-matched list stands.
- **D10. Check now.** RPC `forecast_generate` (account, namespace, kind, name, `metric` preset,
  threshold, optional `end_time`) *(v8)*. Otherwise unchanged from v6:
  - synchronous
  - 90 s timeout
  - the same roles as `metrics_aggregate_utilisation`
  - creates no event
- **D11. Hooking into the SLO feature.**
  - **Hourly run:** no new cron. In `slo.executeSLOConfig`, a config with `method = 'forecast'`
    goes to `forecast.Evaluate` instead of the agent's `slo_generator`. It then writes
    `last_forecast` and runs D5–D7.
    - `slo.Execute`'s account query currently requires a connected agent. It is kept for v1,
      because the provider list is Prometheus and Prometheus is served by the agent.
    - The other SLO types are unchanged.
  - **Create and update:** `CreateOrUpdateSLOConfig` accepts `cpu_throttling` and
    `cpu_saturation` with a threshold. It builds no PromQL (`method = 'forecast'`), and refuses on
    `no_limit` or `provider_unsupported`.
  - **Disable and delete** *(r6)*: a dedicated `closeForecastEvents(config)` closes the MEDIUM
    and HIGH fingerprints, sets `nb_status='RESOLVED'` and `close_reason=unwatched`, and publishes
    `notify_resolved`. It is called from the `!Enabled` branch of `CreateOrUpdateSLOConfig`
    (service.go ~401) and from `DeleteSLOConfig` (~474). `closeSLOEventByFingerprint` is **not**
    reused, because it sets only `status`.
  - **Runner** *(r6; cross-pod since review)*: `slo.Execute()` takes a Postgres advisory lock
    (`pg_try_advisory_lock`, session-level on one dedicated connection, released at the end
    or when the pod's connection drops). An hourly run that starts while the previous one
    is still going, on this pod or another, logs and exits, so forecasts are never
    evaluated by two writers. An in-process mutex was the first cut; the rpc-cron trigger
    can land on any api-server pod, so it did not hold. Each forecast fetch has its own **60 s** context timeout, under
    the relay's 180 s. Run duration is logged per account.
  - **Notifications** *(r6)*: forecast events use the normal HIGH-only event path (source
    `forecast`), **not** `slo.triggerNotification`. In notifications-server, `forecast` is added
    to the source→category map so it routes to the **`slo`** rule category
    (`message.py` ~307/367). A user's "SLO → #slo-alerts" rule then receives CPU SLO alerts.
    `message.py` ~406 gets a finding-shaped dedup key for `forecast` messages. **CPU SLOs are not
    listed in the SLO digest,** which is keyed on `slo_alert`; that is stated, not fixed, in v1.
- **D12. Nubi.**
  - **Tool `metric_forecast`** *(v8; was `cpu_throttling_forecast`)*: calls the D10 RPC through
    the `services_server` read-path client, with `AccountScopeFollowsTarget`. It is listed in the
    Events agent and found through `search_tools`; it is not preloaded in the k8s core, so k8s
    turns don't all carry it.
  - **"Which services will breach their CPU SLO?"** Uses `list_events`, filtered to the two keys.
    One line is added to its description.
  - **The trend in the investigation** *(user direction, during testing)*. The investigation
    inlines only evidence *insights*; the markdown and the daily table reach it as a file it
    may not open. So every forecast and breach event carries insights:
    - the value by day, the current value and the slope (rising, falling or flat), with CPU
      usage per pod against the limit (the daily table gains a usage column);
    - the forecast (ETA, projected days, suggested limit) or, for a breach, that the climb
      continues.

    llm-server adds a short guidance block to the investigation prompt for `source =
    forecast` events: lead Quick Investigation with that trend as a table inside the
    section (the report headers are a fixed contract), then explain the cause. Verified on
    dev: the breach report opened with the day-by-day table of throttling and usage.
- **D13. UI.**
  - **SLO dialog** ([SLOConfigDialog.tsx](../app/src/components/k8s/common/SLOConfigDialog.tsx)):
    - a new **CPU** section with two optional thresholds (throttling %, saturation % of limit);
    - a **Check now** button that renders the `ForecastResult` for the threshold entered;
    - **validation is relaxed**: at least one section configured, and each configured section
      complete. Today it demands both availability and latency, which would block a CPU-only SLO.
    - *(r6)* **Removing one type:** each CPU threshold gets a **Remove** action that calls
      `deleteSLOConfig` with `name` (the backend already supports per-name delete at
      service.go ~442). Clearing a field never saves a 0 threshold. The workload-level delete
      modal's text changes from "Delete the availability and latency SLOs" to "Delete all SLOs
      on this workload".
  - **SLO list** ([KubernetesSLOConfigs.tsx](../app/src/components/k8s/KubernetesSLOConfigs.tsx)):
    CPU rows show `last_forecast`'s status (OK / Breach projected in ~Nh / Breached / Not enough
    data), and a **CPU Alerts** row tab lists the workload's forecast and breach events (each
    opens the alert). A tab instead of a per-row link: `last_forecast` carries no event id, and
    the events table already filters by the four keys.
  - Everything is built with `ds/*` primitives.
- **D14. The forecast stays separate from the predictive-HPA model.**
- **D15. No feature flag** *(v8)*. The tenant flag `CPU_THROTTLING_FORECAST` was removed in
  review: a CPU SLO is already an opt-in per workload. `nb_forecast_providers=""` is the
  server-level kill switch: every save and Check now then answers `provider_unsupported`.

## Acceptance criteria

1. **Unit: the fit and `Evaluate`.**
   - The user's ramp yields `projected` with an ETA between 12 h and 72 h at hour 48.
   - A slow climb yields `projected` with an ETA of over a week.
   - Flat load and a single spike yield `not_projected`.
   - A value at the threshold yields `breached`.
   - A limit-changing rollout yields `insufficient_history`.
   - No limit yields `no_limit`.
   - A Datadog account yields `provider_unsupported`.
   - A custom threshold (e.g. 40%) moves the ETA as expected.
2. **The key.** `cpu_throttling_ratio` returns a 0–1 ratio that matches a hand-run cAdvisor query.
3. **SLO flow on dev.** Configure a CPU SLO at 25% on a test service whose load ramps, then
   trigger `/rpc-cron` with `{"name":"SLO Execute"}`.
   - The list shows "Not enough data", then "Breach projected in ~Nh".
   - A MEDIUM event appears, then a HIGH event that notifies and gets investigated.
   - A re-crossing after the cooldown notifies again.
   - Disabling the SLO closes the event.
   - Existing availability and latency SLOs on the same service behave exactly as before, and
     their attainment is unchanged.
4. **Check now.** The dialog button, the RPC and Nubi give the same result. No event is created.
5. **Backtest with a bar.** `Evaluate` is replayed before past `CPUThrottlingHigh` alerts (24 h and
   48 h earlier) and across 7 normal days. The bars:
   - at least 70% of HIGH warnings followed by a breach within 72 h
   - at most 5 MEDIUM warnings per 100 services per week

## Delivery

| PR | Contents |
|---|---|
| 1 | Engine, `cpu_throttling_ratio` key (Prometheus), the generate RPC, the SLO backend (`slo/forecast_slo.go`: new types, the branch, explicit goal and window, threshold validation, `closeForecastEvents`, Execute advisory lock, `last_forecast` column plus `slo_config_v2` metadata and the api1 query), events, migrations (`forecast` source, `last_forecast` column, catalog flag), notifications-server (`forecast` → `slo` category, dedup key, copy-library names), triage exclusion, fix matching, Nubi tool and `list_events` hint |
| 2 | UI: SLO dialog CPU section with Check now, relaxed validation and a per-type Remove; CPU rows in the SLO list; delete-modal text |
| next | Datadog keys fixed to the contract and added to the provider list |

## Out of scope

- Error-budget-style CPU SLOs ("under 25% for 99% of the time"). The user chose threshold-style.
- Checking every service automatically.
- Memory forecasting.
- A projection line on the chart.

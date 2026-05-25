# Alert Threshold Suggestions — Design Document

## Overview

Automated system that analyzes noisy cloud alerts, computes optimal threshold values using statistical methods, and presents actionable recommendations to reduce alert fatigue.

**Supported Sources:** AWS CloudWatch, Azure Monitor, GCP Metric Alerts, Prometheus, PagerDuty

---

## 1. Core Logic

### 1.1 Alert Selection

The system identifies noisy alerts using a simple heuristic:

- Query all alerts with **≥5 firings in the last 30 days**
- Group by alert identity (source-specific key — see §3)
- Return up to **500 alerts** ordered by fire count descending
- Skip alerts already processed in the last 24 hours

### 1.2 Threshold Calculation

For each alert, the system fetches 7 days of metric history and computes a suggested threshold using a **fallback chain** of statistical methods:

**For upper thresholds (GreaterThan operators):**

| Priority | Method | Formula | When Used |
|----------|--------|---------|-----------|
| 1 | MAD | `median + 3.0 × MAD` | MAD > 0 (most data) |
| 2 | IQR | `P75 + 1.5 × IQR` | MAD = 0, IQR > 0 |
| 3 | P95 | `P95 × 1.10` | Both MAD and IQR = 0 |

**For lower thresholds (LessThan operators):**

| Priority | Method | Formula | When Used |
|----------|--------|---------|-----------|
| 1 | MAD | `median - 3.0 × MAD` | MAD > 0 |
| 2 | IQR | `P25 - 1.5 × IQR` | MAD = 0, IQR > 0 |
| 3 | P5 | `P5 × 0.90` | Both MAD and IQR = 0 |

**Why MAD over standard deviation?**
MAD (Median Absolute Deviation) is robust to outliers. A single spike won't skew the suggestion like it would with StdDev. Computed as: `median(|x_i - median(X)|)`.

**Sanity guards:**
- Suggested threshold is capped at `max(current × 10, P99)` — prevents absurd suggestions
- If suggestion would increase noise (wrong direction), it's discarded
- Changes >5× current threshold are flagged: `[Large change (>5x) — review manually.]`

### 1.3 Estimated Noise Reduction

Calculated by counting how many of the historical metric values would **not** have crossed the suggested threshold:

```
reduction = (values_below_suggested - values_below_current) / total_values × 100
```

For GT operators, a higher threshold means fewer crossings → fewer alerts.

### 1.4 Sparse Metric Handling (Spike Analysis)

When metric data is sparse (<100 data points in 7 days), the system uses spike analysis:
- Identifies spike clusters (values above P90)
- If >30% of values are spikes, uses P95 × 1.5 as the suggestion
- Prevents false suggestions based on too little data

---

## 2. Alert Quality Assessment

### 2.1 Metrics

All computed over a **30-day window** from the events table:

| Metric | Formula | Meaning |
|--------|---------|---------|
| **Resolution Rate** | `COUNT(ends_at > starts_at) / total` | % of alerts that actually resolved |
| **Transient Rate** | `COUNT(duration < 10 min) / resolved_count` | % of resolved alerts that were very short-lived |
| **Flapping Rate** | `COUNT(duration < 5 min) / total` | % of alerts that toggled on/off within 5 minutes |
| **Engagement Rate** | `COUNT(nb_status_changed_by IS NOT NULL) / total` | % of alerts manually triaged by a human |
| **Instant Rate** | `COUNT(ends_at = starts_at) / total` | % of alerts that fired and resolved at the same timestamp |
| **Duration P90** | `PERCENTILE_CONT(0.9) of (ends_at - starts_at)` | 90th percentile of alert duration in seconds |
| **Firing Frequency** | `total / days_since_first_fire` | Average daily firings |
| **Mean Time to Close** | `AVG(ends_at - starts_at)` for resolved events | Average time to resolution |

### 2.2 Classification

Evaluated in priority order — first match wins:

| # | Classification | Condition | Default Action |
|---|---------------|-----------|----------------|
| 1 | **False Positive** | freq > 1/day AND resolution > 80% AND engagement < 10% | Increase duration if transient > 70%, else disable |
| 2 | **Broken** | resolution < 10% AND instant rate < 50% | Investigate / disable |
| 3 | **Noisy but Real** | freq > 1/day AND resolution > 50% AND engagement > 10% | Increase duration if transient > 70%, else tune threshold |
| 4 | **False Positive** (lower freq) | freq > 0.5/day AND resolution > 50% AND engagement < 10% | Increase duration if transient > 70%, else tune threshold |
| 5 | **Healthy** | Default — none of the above | No action (threshold tuning can still reduce noise) |

### 2.3 Duration Recommendation

When transient rate is high (>70%), the system also suggests increasing the evaluation window:

```
suggested_duration = ceil(duration_P90 × 1.5 / 5) × 5   # Rounded up to nearest 5 minutes
                                                          # Clamped to 10-30 min range
```

### 2.4 Final Recommendation Type

| Type | When |
|------|------|
| `tune_threshold` | Has threshold suggestion, transient rate ≤ 70% |
| `increase_duration` | No threshold suggestion, transient rate > 70% |
| `tune_both` | Has threshold suggestion AND transient rate > 70% |
| `disable` | Classification is `broken` |
| `none` | No actionable changes found |

---

## 3. Source-Specific Handling

### 3.1 Alert Identity (alertRuleKey)

Each source uses a different field to uniquely identify an alert rule:

| Source | Key Field | Example |
|--------|-----------|---------|
| AWS CloudWatch | `labels->>'aws_event_arn'` | `arn:aws:cloudwatch:us-east-1:123:alarm:HighCPU` |
| Azure Monitor | `COALESCE(labels->>'azure_alert_name', labels->>'alertname')` | `High CPU Alert` |
| Prometheus | `labels->>'alertname'` | `KubeHpaMaxedOut` |
| GCP | `labels->>'gcp_policy_id'` | `projects/my-proj/alertPolicies/123` |
| PagerDuty | `labels->>'nb_alert_name'` (fallback: alertname) | `PD_HighLatency` |

### 3.2 Alert Definition Extraction

| Source | Threshold From | Metric From |
|--------|---------------|-------------|
| AWS CloudWatch | Event labels + `evidences[0]` JSON (Period, EvaluationPeriods, ComparisonOperator) | Labels: metric_name, namespace, statistic |
| Azure Monitor | `azure_alert_criteria.allOf[0]` or `azure_alert_context.condition.allOf[0]` | Criteria: metricName, metricNamespace |
| Prometheus | Parsed from PromQL expression in `event_rules.expr` | Extracted metric name from PromQL |
| GCP | AlertPolicy JSON in `event_rules.expr` — `conditionThreshold.thresholdValue` | `conditionThreshold.filter` |
| PagerDuty | Reuses Prometheus PromQL extraction from `event_rules.expr` | Same as Prometheus |

### 3.3 Metric Data Fetch

| Source | Method | Details |
|--------|--------|---------|
| AWS CloudWatch | `cloud.QueryMetrics()` | Standard namespaces via SDK; custom namespaces via CLI (`aws cloudwatch get-metric-statistics`) |
| Azure Monitor | `cloud.QueryMetrics()` | Uses resource IDs from alert criteria |
| Prometheus | `relay.ExecutePrometheus()` | Via relay server to cluster |
| GCP | `cloud.QueryMetrics()` | Uses MQL filter from alert policy |

All queries use a **7-day lookback** window.

### 3.4 PromQL Threshold Extraction

The system parses PromQL binary expressions to extract thresholds:
- Supports compound operators (`AND`, `OR`, `UNLESS`)
- Identifies and ignores guard conditions (e.g., `> 0`)
- Handles reversed comparisons (e.g., `100 < expr`)
- Evaluates constant arithmetic (e.g., `25 / 100`)

---

## 4. Confidence Scoring

Confidence is computed from four weighted factors (total: 100 points):

| Factor | Max Points | Criteria |
|--------|-----------|----------|
| **Data Coverage** | 30 | >500 values: 30, >100: 20, else: 10 |
| **Data Stability (CV)** | 25 | CV < 0.5: 25, CV < 1.0: 15, else: 5 |
| **Suggestion Reasonableness** | 25 | Ratio 0.5-2.0×: 25, 0.1-10×: 15, else: 5 |
| **Alert Quality** | 20 | noisy_but_real: 20, healthy: 10, else: 5 |

**Final rating:** ≥70 = High, ≥40 = Medium, <40 = Low

CV = Coefficient of Variation (stddev / mean). Low CV means stable metric → higher confidence.

---

## 5. Implementation Details

### 5.1 Batch Pipeline

```
Temporal cron (daily 6AM UTC)
  → POST /hasura-cron {name: "Threshold Suggestion Refresh"}
  → triage.AnalyzeNoisyAlerts(ctx)
    → findNoisyAlerts() — SQL query, ≥5 firings, top 500
    → cleanStaleRows() — remove rows >30 days old
    → for each account (max 5 concurrent):
        → for each alert (sequential):
            → skip if computed_at < 24h ago
            → findRepresentativeEvent()
            → GetThresholdSuggestion()
            → computeAlertQuality()
            → computeConfidence()
            → computeDurationRecommendation()
            → upsertSuggestion() or upsertSkippedSuggestion()
```

### 5.2 Database Schema

**Table:** `event_threshold_suggestions`

```sql
CREATE TABLE event_threshold_suggestions (
  id                  UUID PRIMARY KEY,
  fingerprint         TEXT NOT NULL,
  alert_rule_key      TEXT NOT NULL,
  cloud_account_id    UUID NOT NULL,
  tenant_id           UUID NOT NULL,
  source              TEXT NOT NULL,

  -- Alert definition
  alert_name          TEXT,
  metric_name         TEXT,
  metric_namespace    TEXT,
  current_threshold   NUMERIC,
  operator            TEXT,
  aggregation         TEXT,

  -- Suggestion
  suggested_threshold NUMERIC,
  reason              TEXT,
  confidence          TEXT,           -- low | medium | high
  estimated_reduction NUMERIC,
  recommendation_type TEXT,

  -- Analysis data (JSONB)
  firing_analysis     JSONB,          -- total_occurrences, avg_firings_per_day, etc.
  metric_stats        JSONB,          -- p50, p90, p95, p99, median, mad, duration info
  alert_quality       JSONB,          -- classification, resolution_rate, transient_rate, etc.
  method              TEXT,           -- MAD | IQR | P95 | spike
  query_metadata      JSONB,          -- metadata for frontend to re-query metrics live

  status              TEXT,           -- ok | skipped | error
  computed_at         TIMESTAMPTZ,
  created_at          TIMESTAMPTZ,
  updated_at          TIMESTAMPTZ,

  UNIQUE(alert_rule_key, cloud_account_id)
);

-- Indexes
CREATE INDEX idx_ets_tenant ON event_threshold_suggestions (tenant_id, cloud_account_id);
CREATE INDEX idx_ets_alert_rule_key ON event_threshold_suggestions (alert_rule_key, cloud_account_id, status);
```

**Migrations:** V656 (create table) → V682 (add status) → V687 (add alert_rule_key, reindex) → V703 (add alert_quality, method JSONB) → V704 (add query_metadata)

### 5.3 API

**RPC actions:**

| Action | Purpose |
|--------|---------|
| `event_get_threshold_suggestion` | Get/compute suggestion for a single event |
| `event_list_threshold_suggestions` | List cached suggestions for a tenant (with filters, pagination) |

**Get Suggestion** — tries cache first (`GetCachedSuggestion`), falls back to live computation, then async-caches the result.

**List Suggestions** — queries `event_threshold_suggestions WHERE status = 'ok'` with optional filters: `cloud_account_id`, `source`, `confidence`. Supports `limit`/`offset` pagination.

### 5.4 Frontend

- **Listing page:** `ThresholdSuggestionsManager.tsx` — table with filters (Account, Source, Confidence), expandable rows
- **Evidence panel:** `ThresholdEvidence.tsx` — recommendation summary, metric chart (live 7d or percentile bars), firing trend (30d), quality stats with tooltips
- **API client:** `triage.ts` — `listThresholdSuggestions()` wraps the RPC action

### 5.5 Safety Measures

- **Input sanitization:** CLI args for AWS custom namespaces only allow `[a-zA-Z0-9-./: ]`
- **Data sanitization:** Replaces `+Inf`, `-Inf`, `NaN` with 0 before JSON storage
- **Sanity cap:** Never suggests >10× current threshold or above P99
- **Concurrency control:** Max 5 accounts processed concurrently in batch
- **Stale cleanup:** Rows older than 30 days are deleted each batch run
- **Tenant isolation:** All queries filter by `tenant_id` and `cloud_account_id`

---

## 6. Key Files

| File | Purpose |
|------|---------|
| `triage/threshold_suggestion_batch.go` | Batch orchestration, cron handler, upsert logic |
| `triage/threshold_suggestion.go` | Core engine: threshold calc, alert quality, confidence, source extraction |
| `api/actions_triage.go` | API handlers for get/list suggestions |
| `api/cron.go` | Cron trigger dispatcher |
| `app/src/lib/actions.yaml` | RPC action definitions (`event_get_threshold_suggestion`, `event_list_threshold_suggestions`) |
| `runbook-server/internal/system/cron_triggers.yaml` | Temporal cron schedule (daily 6 AM UTC) — fires `POST /hasura-cron` |
| `api-server/migrations/migrations/app/` | V656, V682, V687, V703, V704 |
| `app/src/components1/triage/ThresholdSuggestionsManager.tsx` | Listing page UI |
| `app/src/components1/triage/ThresholdEvidence.tsx` | Evidence panel UI |
| `app/src/api1/triage.ts` | Frontend API client |

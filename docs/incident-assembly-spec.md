# Incident Assembly — Spec (Step 3, #34658)

Part of epic #34655. Plan reviewed adversarially 2026-07-22 (plan comment on #34658).
Evidence base: `blast-radius-correlation-audit.md` (repo root). Prerequisites merged:
subject normalization (#34671), stable fingerprints (#34681), replay harness (#34689),
eBPF cross-namespace edge fix (#34692), blast-radius panel (#34635).

> **Scope decision (2026-07-22):** Phase 3 targets **workload subjects only**
> (deployments/services), where identity is solid (#34671). **Database-family
> grouping (§5 datastore path) is deferred to a follow-up** — live data (acct
> ff87fbfd, 3d) shows `_series` present on only ~38% of datname alerts, stored as
> an unparseable Go-`fmt` string, with no clean always-present fallback
> (`prometheus`/`source_url` 58%, `alertname`/`routing_key` 100% but wrong
> granularity). The robust fix is **data-side** (enrichment stamping a stable
> server identity — revives the useful half of parked #34657), not assembly logic.
> The chronic tier (§6) folds the constant datname chatter regardless, so the 62%
> volume is handled as noise even while precise grouping waits. §5's `_series`
> union-find, §9 golden case (2), and §12 risk (1) move to that follow-up.

> **Implementation deviations accepted at Slice A (2026-07-22)** — the code and the
> golden corpus encode these; they supersede the sections below where they conflict:
> 1. **Core never folds to chronic** (§4.7): alerts on the seed's own subject always
>    show as `same_incident` — you opened this subject, so a real incident on a noisy
>    subject can never be hidden. Chronic-rate `evidence` is still attached; the UI
>    should badge high-rate core items. (Corpus case `sd-ss-lat` endorses this.)
> 2. **Cause lane is −2h only for now** (§4.4): the −7d `context` label and
>    `extractConfigChangeSummary` enrichment are deferred to the panel slice —
>    everything shown is in the likely-cause window; the Timeline still covers older
>    deploys.
> 3. **Collapse is keyed `(tier, SubjectKey, aggregation_key)` over the whole window**
>    (§4.3), not fingerprint+90s: repeat firings and cross-source copies collapse into
>    one item with `occurrence_count` + `sources[]`. Note `occurrence_count` counts
>    rows, i.e. firings *and* duplicate deliveries.
> 4. The legacy annotate query and `impacted[]` are untouched this slice — the response
>    transiently carries both the binary `alerting` flag and `assembly.impact/chronic`;
>    the panel slice must render from `assembly` only and consolidates the two fetches.

## 1. Problem

Opening an event shows an isolated alert. In the audited account: a real P1 had 6 sibling
alerts and a causing deploy in-window — none surfaced as related; 62% of alert volume is
one shared-postgres story split across 8 subject identities; chronic alerts (10–43
firings/day) land in any 2.5h window 30–52% of the time, so "alerted in window" is
uninformative. The pairwise correlation engine (`triage/correlation.go`) is dead for
cross-service work (10-min window + requires ServiceMap evidence on the event).

## 2. Goal / non-goals

**Goal:** the incident page shows one story, sorted into four sections — same incident,
probable cause, impact, background noise — computed deterministically from four
meaning-free signals: subject identity, time order, the call graph, and per-alert firing
history. Works for any tenant, any alert source, no hardcoded scenarios, no LLM.

**Non-goals (explicitly deferred):**
- Semantic judgment ("is this alert *plausibly* caused by that one") — Step 4 (#34659),
  which consumes this spec's output.
- Notification/paging de-noise — separate initiative; this is read-path only.
- A persisted incident entity — revisit at Step 4 (its verdict cache pins a snapshot).
- Composite-alert decomposition (one alert bundling several causes) — Step 4, needs logs.
- Collapsing rows in the events *list* — dedup Step 2 (with #34660 corpus).

## 3. API contract

No new RPC. `event_get_impact` (api/actions_triage_impact.go) gains **additive** fields;
existing fields (`impacted`, `depends_on`, `correlated_count`, `coverage_confidence`, …)
keep their semantics, so the shipped panel keeps working during rollout.

```jsonc
{
  // ---- existing fields unchanged ----
  "event_id": "…", "seed": {…}, "resolved": true,
  "impacted": [ … ],           // dependents; each gains "tier" + "evidence" (below)
  "depends_on": [ … ],
  "coverage_confidence": "observed",

  // ---- new ----
  "same_incident": [            // alerts sharing the root's subject identity
    {
      "event_id": "…", "title": "…", "aggregation_key": "HighErrorCriticalLogs",
      "priority": "P2", "starts_at": "…",
      "dt_seconds": -14,              // start offset vs root (negative = before)
      "sources": ["prometheus", "pagerduty"],   // merged cross-source copies
      "occurrence_count": 7,          // from the event_duplicates chain
      "tier": "core" | "chronic",
      "evidence": { "rate_per_day": 0.2, "burst_factor": 1.0 }
    }
  ],
  "cause": {
    "config_changes": [           // deploys/changes BEFORE root, on subject ∪ depends_on
      {
        "event_id": "…", "subject": "llm-server", "starts_at": "…",
        "dt_seconds": -2460,
        "label": "likely_cause" | "context",   // ≤2h before root → likely_cause
        "summary": "image tag v1.4.2 → v1.4.3; git 722c1d5"  // extractConfigChangeSummary
      }
    ],
    "upstream": [                 // alerts on depends_on identities at/before root
      { "event_id": "…", "subject": "runbook-server", "dt_seconds": -180,
        "tier": "core" | "chronic", "evidence": {…} }
    ]
  },
  "chronic": [ /* same shape as same_incident items; the collapsed tier */ ],
  "assembly": {                   // meta
    "window": {"core_s": 7200, "impact_s": 7200, "lead_in_s": 1800},
    "root_identity": "nudgebee|llm-server",
    "chronic_burst_factor": 3.0
  }
}
```

`impacted[]` items gain `"tier": "impact" | "chronic"` and the same `evidence` object.
Items classified chronic appear ONLY in `chronic[]` (not duplicated in their source lane).

## 4. Pipeline (handler restructure)

Order matters — today the handler early-returns when the subject doesn't resolve to a KG
node, which is exactly the datname family (unresolvable **by design** since #34671). New
order:

1. **Window fetch first** (existing query in `annotateImpactedWithActiveAlerts`, widened):
   all non-config-change events for the account in `[root−2h, root+2h]`, selecting
   additionally `fingerprint, subject_type, aggregation_key, labels->>'_series'`.
   Config-change events fetched separately (step 4). LIMIT stays 1000; if hit, set
   `assembly.truncated=true`.
2. **Identity computation** (Go, §5) for every fetched row + the root.
3. **same_incident**: rows whose identity == root's identity. Then:
   - collapse occurrence chains: group by fingerprint, keep newest, carry
     `occurrence_count` (event_duplicates).
   - merge cross-source copies: same `(identity, aggregation_key)` starting within 90s →
     one item, union of sources.
4. **cause.config_changes**: `getConfigChangeEntries`-pattern query (timeline.go:477) over
   subjects = {root subject} ∪ depends_on names (when seed resolved), window
   `[root−7d, root+30m]`, latest-first, top 3. Label: `dt ≤ 2h → likely_cause`, else
   `context`. Summary via `extractConfigChangeSummary` (timeline.go:689).
5. **cause.upstream**: window rows whose identity ∈ depends_on identities AND
   `starts_at ≤ root.starts_at` (skew tolerance 0).
6. **Seed resolution + impact** (existing code, unchanged semantics): KG walk
   `GetImpactedServices` depth 2, same-namespace scope, annotate. Runs AFTER 1–5; on
   unresolved seed, steps 3–4(root-only)–5(empty) still return content.
7. **Chronic classification** (§6) over every candidate in same_incident, cause.upstream,
   and impacted alerts. Chronic items move to `chronic[]`; bursting ones stay put with
   `evidence.burst_factor`.

## 5. Subject identity (`triage/subject_identity.go`)

One production implementation; `replay.NormalizedGrouper` delegates to it (the harness
scores production logic — drift-proof).

```
SubjectKey(subjectType, name, namespace, owner string) string
```
1. If `owner != ""` → `namespace|owner` (lowercased).
2. Else strip one trailing K8s pod-template hash: regex `-[bcdfghjklmnpqrstvwxz0-9]{6,10}$`
   (the real K8s vowel-free alphabet — already used in replay.go) → `namespace|stripped`.
3. Else `namespace|name`.

**Datastore family grouping** (database-typed subjects — they have no workload identity):
```
GroupDatastoreAlerts(rows) — union-find within the fetched window
```
- Two database-subject rows join the same family when their `_series` datname sets
  **overlap** (≥1 common member). NOT exact-set equality: sets drift when a database is
  created/dropped or enrichment misses a run (adversary finding).
- Rows with no `_series` fall back to `ns|datname` singleton families. Known gap:
  PagerDuty-delivered datname alerts carry no `_series` → they won't join the family.
  Documented + golden-cased; the fix is data-side (recording rule / relay enrichment),
  not assembly logic.
- The family's identity for display = sorted union of member datnames.

## 6. Chronic classification

For the candidate set only (not the whole account), one indexed query:

```sql
SELECT fingerprint, count(*) FROM events
WHERE cloud_account_id = $1 AND fingerprint = ANY($2)   -- candidate fps from the window fetch
  AND starts_at > now() - interval '7 days'
GROUP BY fingerprint
```

Go-side: sum counts per `(SubjectKey, aggregation_key)` (fingerprints are stable
subject+rule post-#34681; summing across sibling fingerprints absorbs PD ReplicaSet-name
rotation — rotation causes *undercount*, which is the safe direction: less suppression).

```
rate      = sum_7d / 7 days
expected  = rate × window_length          (window the candidate was found in)
observed  = candidate-lane occurrences of this (identity, aggregation_key) in-window

chronic   ⇔ expected ≥ 1.0 AND observed ≤ 3.0 × expected
```

- `expected ≥ 1` = "was statistically going to be in this window anyway".
- `observed > 3×expected` = bursting → NOT demoted; keeps its tier, badge
  `chronic_bursting` via `evidence.burst_factor = observed/expected`. This protects the
  real-incident-on-noisy-subject shape (audit's OOM→latency case; golden-cased).
- Corroboration (shown as evidence, never gating): `triage_signal_class.
  recurrence_semantics = 'noise'` and `AlertQualityScore.Classification` when present.
- Constants `chronicExpectedMin = 1.0`, `chronicBurstFactor = 3.0` — package consts with
  rationale comments; tunable later via the harness, not per-tenant config.

## 7. What is reused (do not rebuild)

| Mechanism | Where | Used for |
|---|---|---|
| Window fetch + byKey/owner bucketing | actions_triage_impact.go `annotateImpactedWithActiveAlerts` | steps 1, 3, 5, 7 |
| KG blast radius | `knowledge_graph/core.GetImpactedServices` via same file | impact + depends_on |
| Config-change lookup + summary | timeline.go `getConfigChangeEntries` / `extractConfigChangeSummary` | cause.config_changes |
| Occurrence chains | `event_duplicates` (processor.go) | occurrence_count, chain collapse |
| Alert-quality labels | threshold_suggestion.go `AlertQualityScore` | chronic corroboration |
| LLM noise verdict | `triage_signal_class` | chronic corroboration |
| Identity normalizer reference | `triage/replay` NormalizedGrouper | delegates to §5 |

The evidence-embedded graph path (`service_map.go` + `correlation.go` scorer) is NOT used.

## 8. Single-answer cleanup (Slice B, hard exit criterion)

After Slice B ships, the investigate page has exactly one source of "related":
- timeline's `getCorrelatedEventEntries` (timeline.go:449) stops reading
  `event_correlations`; it renders assembly `same_incident`/`impacted` refs instead
  (or the lane is dropped — decide at implementation with a screenshot).
- `event_correlations` continues to be *written* and read ONLY by `ComputeScore`
  (processor.go:57-65) — documented in code; follow-up issue filed to migrate scoring
  input to assembly output. No UI surface reads `event_correlations` after B.
- The 'related' UI gate lifts (#34639 fixed by #34692).

## 9. Validation

**Harness gates (must pass in CI before merge):**
- Same-subject: recall ≥ 0.95, precision ≥ 0.99 (existing gate, assembly's core key
  replaces NormalizedGrouper as the scored production grouper).
- New golden cases added in Slice A, gating Slice B:
  1. cross-service deploy-wave incident (from the audit; recall REPORTED as baseline, not
     gated — the harness cannot replay the KG yet),
  2. PD-delivered datname alert without `_series` (documents the known gap),
  3. OOM→latency chronic-overlap (burst on chronic subject MUST stay visible).
- GoldenEvent schema += `ts_offset_s`, `is_config_change` (also resolves the #34689
  review comment).

**Live verification (dev, after each slice):** open the audited scenarios — llm-server
deploy cascade shows deploy as likely_cause + siblings as same_incident;
postgres datname family groups; services-server latency folded as chronic with rate
evidence; `make validate` clean.

## 10. Performance budget

Per page open: one widened window query (≤1000 rows, `[root-2h,root+2h]`, keeps
config-change rows so no separate cause query) + one chronic GROUP BY over
`fingerprint = ANY(candidate_fps)` + the existing KG walk. Noisy tenant (~3–4k
issues/day) ≈ 350 window rows; Go-side grouping is trivial. **No new index and no
migration** — the fingerprint rarity query is covered by the existing
`events_fingerprint_account_starts_idx (fingerprint, cloud_account_id, starts_at)`
(verified against live schema 2026-07-22; the adversary's "ship a composite subject
index" is moot). Measure p95 in dev before the frontend ships; if it breaks budget,
cache assembly per event with staleness-on-new-candidates — not preemptively.

> **Backend shipped (2026-07-22):** `event_get_impact` returns an additive
> `assembly` object `{root_identity, same_incident, cause:{config_changes,upstream},
> impact, chronic, window, truncated}`. The pure tiering is `triage.AssembleTiers`
> (unit-tested; the replay harness scores it directly — no reference/prod drift).
> Each tier is collapsed by `(SubjectKey, aggregation_key)` → one item with
> `occurrence_count` + unioned `sources` (§4.3a/b). `SubjectKey` is lower-cased/trimmed
> (matches the legacy `impactKey`); topology keys route through it. DB errors are
> logged; `truncated` surfaces the 1000-row LIMIT. Existing response fields are
> unchanged, so the current panel keeps working. Remaining = frontend (render the four
> sections, §8 single-answer cleanup, gate lift) — user-driven e2e.
>
> **Deviations from this spec accepted for this slice (golden corpus now encodes them):**
> 1. **Core never folds to chronic** (`incident_tiers.go`; corpus `sd-ss-lat`). You
>    opened this subject, so its own alerts always show; this also protects the
>    OOM→latency case more strongly than the burst rule. Consequence: a chronic
>    same-subject alert arrives in `same_incident` carrying rarity evidence — the panel
>    should badge it "routine". Collapse makes this low-volume.
> 2. **Cause lane is [root−2h, root] only** — no `likely_cause`/`context` split and no
>    `extractConfigChangeSummary` (spec §4.4 said −7d top-3 with labels). More
>    conservative (everything shown is genuinely in the likely-cause window); the
>    Timeline still surfaces older deploys. Summary enrichment + the label split come
>    with the panel work.
> 3. **Two window fetches per open** (legacy `annotateImpactedWithActiveAlerts` +
>    `buildIncidentAssembly`), and the response transiently carries two downstream
>    answers (`impacted[].alerting` vs `assembly.impact`/`chronic`). Accepted as a safe
>    additive increment — the panel slice must render **only** from `assembly`, and the
>    single-fetch consolidation lands with it (ties into §8 cleanup).
>
> `occurrence_count` is the **in-window** firing count (from the collapse), not the
> `event_duplicates` lifetime chain — more relevant to "this incident" and needs no
> extra query.

## 11. Slices & exit criteria

**A — story:** §5 identity promotion, §4 steps 1–6, additive API fields, corpus growth.
Exit: harness same-subject gate green; unresolved-seed events return populated
same_incident/cause; `make validate`.

**B — noise + one answer:** §6 chronic, §8 cleanup, panel renders 4 sections, gate lift.
Exit: OOM→latency golden case green; no UI reads event_correlations; p95 within budget;
screenshot in PR (app/src changes).

## 12. Risks (from the adversarial review — re-check at PR review)

1. `_series` is enrichment-dependent (eventrule playbook path) — absent when playbooks
   don't run; families fall back to singletons. Mitigated by overlap-union-find +
   golden case; not fully solvable in assembly.
2. Cause-label honesty: a 7-day config window on shared infra is almost always populated —
   the `likely_cause`/`context` split is load-bearing; UI must not flatten it.
3. Fingerprint rotation (PD RS-named subjects) undercounts chronic rates — conservative,
   but revisit if flappers leak into impact on live data.
4. Three-concurrent-answers regression: §8 is the guard; do not ship B without it.

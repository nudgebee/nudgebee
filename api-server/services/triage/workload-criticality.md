# Workload Criticality — design

> Status: implemented (PR #33538, `Fixes #30986`). Owner: triage / observability.
> Code: `criticality.go`, `criticality_api.go`, `criticality_discovery.go`, `criticality_llm.go`,
> `workload_facts.go`, `api/actions_criticality.go`, migration `V776_workload_criticality`.

## 1. Problem

Triage scoring needs to know **how important a workload is** — a crashloop on the payments API is a
different incident from the same crashloop on a demo pod. Previously this was encoded as **19
hard-coded name-regex rows** in `event_triage_rules` (`(?i)postgres` → +30, `(?i)gateway` → +40,
seeded by `V762`). That approach is brittle in two directions:

- **Misses domain services** it doesn't have a pattern for. A business-critical `checkout-service`
  matched nothing and got zero importance.
- **Can't tell environment/purpose apart.** Topology alone flags `demo/postgresql` (15 dependents)
  identically to a production database — it can't know `demo` is a throwaway namespace.

We want criticality to be a **first-class, persistent, operator-curatable attribute of the workload**
— inferred as a good default, corrected by humans, and read by scoring and the UI as one answer.

## 2. The model

Criticality is a named tier on a workload:

| Tier | Meaning |
|------|---------|
| `critical` | genuinely business/customer-critical (auth, primary user-facing API, primary prod datastore) |
| `high` | important shared/internal infra (ingress controllers, core internal APIs, shared DB/queue/cache) |
| `medium` | ordinary application service — **the default**; not stored as a row |
| `low` | non-production / non-business (demo, test, e2e, benchmarks, docs, tooling, monitoring) |

Stored in `workload_criticality`, one row per workload keyed on `cloud_resource_id`
(== `k8s_workloads.cloud_resource_id`). **Only non-`medium` tiers are persisted** — `medium` is the
implicit default (keeps the table small; the UI shows the full inventory via a `LEFT JOIN`).

Each row carries its **provenance**:

- `source = user` — an operator's explicit decision. **Authoritative and sticky**; the discovery job
  never overwrites it.
- `source = fact_signal` — derived from observed topology/labels (fallback when the LLM is down).
- `source = llm_inferred` — the LLM's semantic classification.

Plus `confidence`, `signals` (jsonb — the facts that produced it), `rationale` (one-line "why", shown
in the UI), `is_user_override`, and `updated_by` (the operator who last set it, NULL for auto rows).

The table is **resource-scoped only**: exactly one row per workload, keyed on `cloud_resource_id`
(`NOT NULL`), so every row joins the workload inventory directly. `namespace` is stored as
display/filter metadata. (Namespace/pattern/label fallback rows were considered and dropped — they
carry no `cloud_resource_id` and so can't participate in the inventory join; add them only if a real
need for account-wide default rules appears.)

## 3. How it's populated — the discovery job

`DiscoverWorkloadCriticality` (per account) runs in two stages:

1. **Deterministic recall** (`deriveCriticalityFromFacts`) — a cheap pass over every active workload
   that flags **candidates**: ingress/LB-backed (customer-facing), high dependency fan-in (≥10),
   or an operator-declared `tier=…` label. Topology facts for the whole account are computed in
   **one set-based query** against the knowledge graph (`fetchAccountGraphFacts`) so it scales.
   Auto-derivation deliberately **caps at `high`** — `critical` is reserved for a human or an explicit
   label.

2. **LLM precision review** (`classifyWorkloadsLLM`) — the candidates are sent to the `@llm` agent
   (batched, reusing `llm.ChatCompletion`) with their name/image/labels/namespace + the deterministic
   hints. The model's job is **precision: confirm the genuinely important ones and DEMOTE the false
   positives** topology can't recognize (demo/test/e2e/benchmark/docs/tooling → `low`/`medium`). It
   may promote to `critical`. The account's **global-context** (`llm_global_contexts`) is auto-injected,
   so operator-declared stack knowledge steers it. On LLM failure it falls back to the deterministic
   hint.

**Precedence when writing a row:** `user` override (never touched) > LLM verdict > deterministic hint.
Anything that resolves to `medium` is not stored. Stale non-`user` rows (dropped below threshold,
demoted, topology changed) are **pruned** each run, so re-runs are idempotent.

**Why candidates-only, not all-workloads?** An all-workloads pass (LLM classifies every workload)
was trialed and rejected: it was **~15× slower** (312 sequential classifications) and **over-tiered**
(one account tiered 62 of 77 — when everything is high/low nothing is prioritized). Candidates-only
is fast (~1 min/account) and precise. The cost: a genuinely-important-but-signalless workload
(no ingress, low fan-in, generic name) defaults to `medium` and relies on operator curation — which
is the intended human-in-the-loop.

**Triggers:** nightly cron `Workload Criticality Discovery` (in `cron_triggers.yaml`, scheduled after
`build_knowledge_graph` so the graph is fresh) + on account first-connect. The criticality inputs
(the dependency graph) refresh on a schedule, so criticality is derived downstream of that schedule.

## 4. How the data is **used** (consumers)

The table is a shared source of truth read by several places:

### 4a. Triage scoring (the primary consumer)
The existing LLM signal-class verdict (`ComputeScoreLLM` → `triage_signal_class`) classifies each
alert-class's `blast`/`intrinsic`. When a verdict is minted, `fetchWorkloadFacts` resolves the
workload's criticality (`resolveWorkloadCriticality`) and injects it — plus the observed facts
(ingress-backing, fan-in, labels) — into the classifier prompt (`buildEventContext` +
`groundingGuidance`). So the model reasons about severity **grounded in the workload's real
importance** instead of guessing "customer-facing" from a name. A `source=user` criticality is called
out as authoritative in the prompt.

> Note: this coupling is **advisory** (prompt grounding), not a hard band-override. A follow-up could
> make a user-declared `critical` *force* the score band.

**Resolution** (`resolveWorkloadCriticality`): a direct lookup by `(cloud_account_id,
cloud_resource_id)`. Events carry `cloud_resource_id` ~77% of the time; the rest resolve to `medium`
by default (a name-based fallback is a possible follow-up if that gap matters).

### 4b. Review screen ("Service Criticality" tab)
`app/src/components/criticality/WorkloadCriticalityManager.tsx`, mounted under Kubernetes detail →
Events. Lists the full workload inventory with resolved tier, source, and rationale; namespace /
workload-name / criticality filters; per-row **set/reset** (writes a `user` override). This is the
"party to training" surface — operators correct the AI's baseline, and their edits stick. Backed by
RPC actions `workload_list_criticality` / `workload_upsert_criticality` / `workload_delete_criticality`
(handlers in `api/actions_criticality.go`, routed via `/rpc/triage`).

### 4c. Applications page badge
`KubernetesWorkloads.jsx` fetches the criticality map and renders a tier badge (with a tooltip
explaining what the tier is, its rationale, and where to manage it) next to each workload, so
criticality is visible where operators already work.

### 4d. Future consumers
Because it's a persistent per-workload attribute (not buried in per-alert scoring), RCA/agents can
read it via the list action to weight incidents.

## 5. Key design decisions

- **Criticality is workload metadata, not a scoring rule.** It's browsable inventory; the tier→score
  effect lives in the verdict policy, not per-workload rules (avoids a rule-per-workload explosion).
- **Auto caps at `high`; `critical` is human/label-only.** Prevents the classifier from inflating the
  top tier; ingress-backed internal APIs are `high`, not `critical`, until a human says otherwise.
- **`medium` is default and unstored.** The table only holds exceptions.
- **Env ≈ namespace on the per-account screen.** A cloud account is one cluster with one
  `account_env`; namespace (e.g. `demo`, `*-test`) is the practical environment axis, and the LLM
  uses it to demote non-prod.
- **Manual-add was dropped.** Discovery sweeps every running workload, so pre-declaring is only useful
  for a not-yet-deployed service — a niche that didn't justify the name-scoped-row complexity. Left as
  a clean future follow-up.

## 6. Operational notes

- **LLM path:** `llm.ChatCompletion` → `POST /v1/completions/chat` (the same generic `@llm` agent as
  the verdict classifier). Free-text JSON out, parsed. No new endpoint/agent.
- **Cost/scale:** per account, one set-based graph query + one batched LLM call per ~40 candidates.
  Normal accounts have tens of candidates (1 call). The pathological 7,380-workload single-namespace
  account has no traces (0 candidates) so it's cheap; its inventory is the outlier to watch for the
  review screen's 500-row cap (pagination is a follow-up).
- **Idempotency:** each sweep prunes stale non-`user` rows, so schedule/threshold changes converge.

## 7. Follow-ups

- Hard band-override for `user`-declared `critical` (vs advisory grounding).
- Pagination on the review screen (500-row cap today).
- Optional pre-declare (manual-add) for not-yet-deployed services.
- Retire the legacy `V762` name-regex rows once criticality coverage is broad (kept as fallback today).

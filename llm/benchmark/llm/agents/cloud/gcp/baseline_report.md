# `gcp` baseline

**Date:** 2026-04-06
**Run ID:** `da48db0ddb3d`
**Sample size:** 10 of 10 fixtures (full coverage)
**LLM provider / model(s):** googleai / `gemini-2.5-pro` (reasoning), `gemini-2.5-flash-lite` (retrieval/summary)
**Wall time:** ~10m 17s

## Headline

| Metric | Value |
|---|---|
| Answer similarity (avg) | **60.0 %** |
| Answer relevancy (avg) | **20.0 %** |
| Avg per-query duration | 82.8 s |
| Avg per-query cost | $0.043 |
| Avg total tokens / query | 48,548 |

These scores are intentionally surfaced as-is. The GCP coverage is
narrow (10 fixtures across Cloud Trace API, GKE logs, Cloud Run latency,
HTTP errors) and the agent's accuracy on `gcloud` command construction
is weak compared to its AWS / Azure counterparts. **Treat this baseline
as a known-low starting point rather than a target.**

## Methodology

- Targets the runtime `gcp` agent (general GCP investigation, not a
  traces-only sub-agent — the dir was renamed from `gcp_traces/` during
  the capability-tier restructure to reflect the actual scope).
- Tool: `gcloud_execute`. No provider routing (gcp is its own agent).
- Sample matches the full fixture count (10/10).

## Per-run variance

Five earlier runs of the same 10-fixture set in 2026-04-02 (different
trial wall-clocks) produced sim/rel scores ranging from `0/0`
(infra-failure runs) up to `75/100` (best successful run). The
`da48db0ddb3d` headline above is the most recent stable run, not the
ceiling.

## Known caveats

- **Low relevancy (20%)** indicates the agent generates plausible
  `gcloud` commands that don't always match the expected query shape.
  Expected to improve with provider-specific tool-calling refinements
  rather than fixture rewrites.
- **Small fixture pool (10)** means individual fixture variance moves
  the headline by 10% per query. Multi-trial sampling
  (`--n-trials 3`) would give more stable numbers.
- Three of the ten fixtures are not strictly trace-scoped (GKE logs,
  Cloud Run latency, Cloud Run HTTP errors) — the dir name was
  refreshed from `gcp_traces/` to reflect the actual scope. Tag
  filtering by `trace` would isolate the trace-only subset.

## How to reproduce

```bash
curl -X POST http://<benchmark-server>/agent-benchmark/runs \
  -H 'Content-Type: application/json' \
  -d '{"agent": "gcp"}'
```

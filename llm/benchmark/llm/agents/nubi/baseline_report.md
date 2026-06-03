# `nubi` baseline

**Date:** 2026-05-19
**Run ID:** `ef638d6fc13d`
**Sample size:** 17 of 147 fixtures (parity subset, tagged `holmesgpt-parity`)
**LLM provider / model(s):** googleai / `gemini-3.1-pro-preview` (reasoning), `gemini-2.5-flash-lite` (retrieval/summary)
**Wall time:** ~1h 28m

## Headline

| Metric | Value |
|---|---|
| Answer similarity (avg) | **90.6 %** |
| Answer relevancy (avg) | **84.7 %** |
| Avg per-query duration | 470.4 s |
| Avg per-query cost | $0.89 |
| Avg total tokens / query | 565,605 (incl. 233,293 cache-read) |

This is the headline number for the curated 17-fixture parity subset
used as the recurring smoke baseline. Five consecutive runs of the same
subset between 2026-04-30 and 2026-05-18 landed between **80.0–100.0
sim** and **67.1–100.0 rel**, with the 2026-05-18 run sitting near the
median.

## Methodology

- Subset: fixtures tagged `holmesgpt-parity` (17 of 147) — runs in
  ~90 min vs ~10h for the full suite.
- Concurrency: `--n-concurrent-trials 1` (sequential). Parallel runs
  surfaced transient provider-side 500s under our local llm-server
  setup; sequential is the supported configuration.
- RAGAS: answer_similarity + answer_relevancy via the default gemini
  embeddings (see `benchmark_server/common/llm.py`).

## Known caveats

- **Sample is curated, not random.** The 17 fixtures were originally
  picked across failure-mode buckets (near-pass, latency-bound,
  multi-step) during a prior triage. Numbers don't extrapolate to the
  full 147-fixture suite — a fair cross-version comparison needs a
  full re-baseline.
- **High per-query duration (~7-8 min)** is dominated by the
  reasoning model on multi-step investigations. Latency-sensitive
  consumers should benchmark the smaller per-capability suites
  (`logs/`, `metrics/`, etc.) instead.
- **Cost variance is wide.** Across the 5 recent parity runs costs
  ranged $0.30–$0.89 per query depending on how many tool calls each
  investigation made.
- The full 147-fixture re-baseline is a known follow-up.

## How to reproduce

```bash
curl -X POST http://<benchmark-server>/agent-benchmark/runs \
  -H 'Content-Type: application/json' \
  -d '{"agent": "nubi", "tag_filter": "holmesgpt-parity"}'
```

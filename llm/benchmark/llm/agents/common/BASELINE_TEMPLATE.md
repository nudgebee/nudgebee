# `<agent>` baseline — TEMPLATE

> Copy this template to `llm/agents/<agent>/baseline_report.md` (or
> `llm/agents/cloud/<agent>/baseline_report.md`) when a credible run is
> available. Baselines should reflect a single specific run against a
> single model configuration — not best-ever-numbers across many runs.

**Date:** YYYY-MM-DD
**Run ID:** `<short-run-id>`
**Sample size:** N of M fixtures (full / curated / failure-mode subset)
**LLM provider / model(s):** `<provider>` / `<model-name>`
**Wall time:** `<HHhMMm>` for the full run

## Headline

| Metric | Value |
|---|---|
| Answer similarity (avg) | NN.N % |
| Answer relevancy (avg) | NN.N % |
| Avg per-query duration | NN.N s |
| Avg per-query cost | $0.NNNN |
| Avg total tokens / query | NN,NNN |

## Methodology

- Provider configured at run time (e.g. log provider = Loki / Signoz /
  Datadog) — note which one was selected for this baseline.
- Concurrency: `--n-concurrent-trials N` (sequential vs parallel — note
  which one was used since concurrency affects flake rate).
- RAGAS scoring: answer_similarity + answer_relevancy via gemini
  embeddings (default). Note if a custom scorer / enricher was active.

## Per-tag (optional)

| Tag | Queries | Sim % | Rel % |
|---|---|---|---|
| ... | N | NN.N | NN.N |

## Known caveats

- Note known flake sources (e.g. ragas crashes on multi-line outputs,
  flaky network for cloud-provider CLIs).
- Note any fixture-level skip-markers that weren't actually honored at
  run time and may have inflated / deflated the headline.
- Note model variability if the run used a model with non-deterministic
  output (most do).

## How to reproduce

```bash
# Trigger via dashboard:
#   Run Benchmark -> agent: <agent> -> Submit

# Or programmatically:
curl -X POST http://<benchmark-server>/agent-benchmark/runs \
  -H 'Content-Type: application/json' \
  -d '{"agent": "<agent>", "tag_filter": "<optional>"}'
```

Output JSON lands in DB (`llm_benchmark_test_results`) and is summarized
via the dashboard's "View Report" panel.

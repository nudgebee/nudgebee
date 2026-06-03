# `aws` baseline

**Date:** 2026-04-15
**Run ID:** `c0c73a8739ff`
**Sample size:** 37 of 372 fixtures (~10 %, curated)
**LLM provider / model(s):** googleai / `gemini-3.1-pro-preview` (reasoning), `gemini-2.5-flash-lite` (retrieval/summary)
**Wall time:** ~5h 18m

## Headline

| Metric | Value |
|---|---|
| Answer similarity (avg) | **93.9 %** |
| Answer relevancy (avg) | **87.8 %** |
| Avg per-query duration | 451.7 s |
| Avg per-query cost | $3.15 |
| Avg total tokens / query | 2,899,003 (heavy: AWS CLI investigations chain many tool calls) |

A second run on the same day (`fa3ad531c301`, same 37-fixture subset)
landed at sim **90.5 %** / rel **83.1 %** with 4× shorter wall time
(2h 16m) — variance under concurrency. Use the `c0c73a8739ff` numbers
above as the sequential reference.

## Methodology

- Subset of 37 fixtures from the 372-fixture `aws/` corpus —
  selected for broad coverage across the AWS CLI surface
  (EC2 / RDS / S3 / Lambda / IAM / CloudWatch).
- Targets the runtime `aws` agent which dispatches `aws_execute` and
  pulls in `aws_observability` confirmations.
- Per-query duration is high because AWS investigations frequently
  chain 5–15 CLI calls; the 451.7 s average includes tool execution
  wall time, not just LLM latency.

## Known caveats

- **Sample is curated**, not a random draw — biased toward the more
  common AWS investigation patterns. Numbers don't extrapolate
  uniformly across the full 372-fixture suite.
- **Cost is non-trivial** ($3.15/query × 37 = ~$117 for one full
  run of this subset). A 372-fixture full run would land near $1k
  at current rates — budget accordingly.
- The most recent full-fixture-set baseline run is older than this
  one; if you re-baseline, plan for ~24–48h wall time at sequential
  concurrency.

## How to reproduce

```bash
curl -X POST http://<benchmark-server>/agent-benchmark/runs \
  -H 'Content-Type: application/json' \
  -d '{"agent": "aws"}'
```

Curated-subset selection is by fixture-index slice or tag filter —
see the source run name `aws-20260415-c0c73a87` for the exact
configuration.

# Correlation replay harness (#34660)

Measures how well an alert-grouping algorithm recovers real incidents, so a
change to grouping can be scored with a number instead of hand-audited.

## What it does

1. **Answer key** — `testdata/golden_audit.json` is a set of real recorded alerts
   from the correlation audit, each tagged with the incident a human says it
   belongs to.
2. **Replay** — a `Grouper` assigns each alert a grouping key; alerts sharing a
   key form one group.
3. **Score** — `Score` compares the produced groups against the labels using
   pairwise precision/recall (recall penalises fragmenting one incident;
   precision penalises merging unrelated alerts).

Run it:

```bash
cd api-server/services
go test ./triage/replay/ -run TestReplayHarness -v
```

Current baseline on the seed corpus (reproduces the audit):

```
baseline    precision=1.00 recall=0.17   # today: raw subject names fragment incidents
normalized  precision=1.00 recall=1.00   # owner / hash-strip / shared datastore signal recovers them
```

## Growing the answer key

Add labelled events to `golden_audit.json`. Each event carries the fields a
grouper keys on plus its `incident` label; events with the same `incident` are
one ground-truth group. Keep it real — copy the identity fields from actual
`events` rows (subject type/name/namespace/owner, aggregation key, and the
`_series` sibling-datname set for datastore alerts). No hostnames, IPs, incident
URLs, or secrets — only the identity fields grouping keys on.

## Scope today, and where it grows

The two reference groupers model **today's behaviour** (`BaselineGrouper` — raw
subject, which fragments) vs the **intended subject normalization**
(`NormalizedGrouper`). They exist so the harness can quantify the improvement
before the real machinery lands.

When the deterministic incident assembly (Phase 3, #34658) exists, it implements
the same `Grouper` interface and is scored against this same corpus — and the
cause / impact / chronic tiers get their own per-tier scoring on top of the
pairwise score here.

### Known blind spot — same-subject only

Every incident in the current answer key is recoverable from **subject identity
alone** (owner / hash-strip / shared `_series`). The audit's hardest case — the
deploy-wave cascade, where the events that belong together have *different*
subjects (llm-server failing because of the runbook server, relay-server erroring
on a client's auth loop) — is deliberately absent, because the reference groupers
here are subject-based. A Phase 3 assembly could therefore score 1.00 on this
corpus while still failing at the thing that matters most: cross-service grouping.

**Before Phase 3 is scored against this corpus, cross-service incidents — members
with distinct `subject_owner` — MUST be added**, along with the per-event timing
and config-change fields those tiers key on (see the "Future fields" note on
`GoldenEvent`). Until then, this harness gates subject normalization and Step-2
dedup only.

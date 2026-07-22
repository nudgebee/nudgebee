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

## Assembly scoring (Step 3, #34658)

The flat `Grouper` above partitions events by one key — right for the "same
incident" tier, but it cannot express **cause** and **impact**, which are
*directional and relative to the alert a viewer opened*: the same alert is impact
of one incident and core of another. `assembly.go` scores that.

- `testdata/golden_assembly.json` — a seed-relative corpus. Each incident names a
  canonical `seed` (the alert a viewer would open), a fixture `depends_on`
  call-graph, and fixture `rates` (`"SubjectKey|aggregation_key" -> {expected,
  observed}`) standing in for the KG walk and the rarity query the production path
  runs. Events carry `ts_offset_s` (seconds from the incident root) and
  `is_config_change`.
- An `Assembler` sorts each event into a tier (`core | cause | impact | chronic`)
  relative to the seed. `ScoreAssembly` reports per-tier recall plus how many
  in-window distractors (`tier: "none"`) it wrongly pulled in.
- `BaselineAssembler` models today's raw same-name grouping; `IntendedAssembler`
  is the executable spec of the four-tier rules. Production `triage.AssembleTiers`
  will implement the same rules over real rows and be held to this gate.

```bash
cd api-server/services
go test ./triage/replay/ -run TestAssemblyHarness -v
```

Current reference scores:

```
baseline  core=0.80 cause=0.00 impact=0.00 chronic_fold=0.00   # raw same-name grouping: blind to cross-service
intended  core=1.00 cause=1.00 impact=1.00 chronic_fold=1.00   # tiering recovers cause/impact, folds noise
```

### Scope: workload subjects, not databases

The assembly corpus is **workload subjects only** (deployments/services), where
subject identity is solid (owner / hash-strip, shipped in #34671). Grouping the
shared-database family — 62% of the audit's alert volume — is deliberately a
follow-up: the signal it would key on (`_series`) is present on only ~38% of live
datname alerts and is stored unparseably, so a robust fix is data-side
(enrichment stamping a stable server identity), not assembly logic. The chronic
tier folds the constant datname chatter regardless.

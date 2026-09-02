import { BACKLOG_THRESHOLD, FLAGGED_CAP, INCIDENT_P1_THRESHOLD, resolveBriefing, type BriefingPayload } from '../resolveBriefing';

const NOW = Date.parse('2026-08-05T00:00:00Z');
const daysAgo = (days: number) => new Date(NOW - days * 86400000).toISOString();

/**
 * The approved prototype's dataset, reconstructed. Every assertion below that
 * quotes a literal string is checking we still reproduce that screenshot.
 */
const prototypePayload = (): BriefingPayload => ({
  // event_count is the only field the model reads off this block; the dashboard
  // layout has no correlations or signal-class tile.
  windowTotals: { event_count: 1457 },
  windowIssues: 795,
  byRank: [
    { computed_priority: 'P1', event_count: 2 },
    { computed_priority: 'P2', event_count: 34 },
    { computed_priority: 'P3', event_count: 759 },
  ],
  byRank30d: [{ computed_priority: 'P0', event_count: 54 }],
  disagreement: [
    { priority: 'HIGH', computed_priority: 'P1', event_count: 14 }, // agreed
    { priority: 'HIGH', computed_priority: 'P3', event_count: 237 }, // below — the biggest
    { priority: 'MEDIUM', computed_priority: 'P3', event_count: 321 }, // below
    { priority: 'MEDIUM', computed_priority: 'P2', event_count: 100 }, // agreed
    { priority: 'LOW', computed_priority: 'P3', event_count: 61 }, // agreed
    { priority: 'INFO', computed_priority: 'P3', event_count: 50 }, // agreed
    { priority: 'INFO', computed_priority: 'P2', event_count: 10 }, // above
    { priority: 'LOW', computed_priority: 'P2', event_count: 2 }, // above
  ],
  bySignalClass: [
    { aggregation_key: 'image_pull_backoff_reporter', event_count: 358 },
    { aggregation_key: 'OtelDemoHighLatency', event_count: 160 },
    { aggregation_key: 'PostgreSQLCacheHitRatio', event_count: 136 },
    { aggregation_key: 'SLOViolation', event_count: 91 },
  ],
  firingNow: 32,
  stuckFiring: [
    { created_at: daysAgo(40), event_count: 10 },
    { created_at: daysAgo(25), event_count: 61 },
    { created_at: daysAgo(10), event_count: 10 },
  ],
  thresholdSuggestions: [
    {
      alert_name: 'OtelDemoHighLatency',
      firing_analysis: { total_firings: 1656 },
      alert_quality: { verdict: 'false_positive' },
      apply_status: 'pending',
    },
  ],
  investigations: { total: 54, completed: 48 },
  windowStartMs: NOW - 86400000,
  windowEndMs: NOW,
  nowMs: NOW,
});

const byKey = (items: { key: string }[], key: string) => items.find((item) => item.key === key) as any;
const callout = (model: ReturnType<typeof resolveBriefing>, key: string) => byKey(model.callouts, key);

describe('resolveBriefing — reproduces the approved prototype', () => {
  const model = resolveBriefing(prototypePayload());

  it('resolves the header funnel', () => {
    expect(model.header.ingested).toBe(1457);
    expect(model.header.issues).toBe(795);
    expect(model.header.aboveP3).toBe(36);
    expect(model.header.p1).toBe(2);
    expect(model.header.p2).toBe(34);
  });

  it('renders column A tiles', () => {
    expect(model.intake.map((tile) => tile.key)).toEqual(['ingested', 'collapsed', 'issues', 'investigations']);
    expect(byKey(model.intake, 'ingested').value).toBe('1,457');
    expect(byKey(model.intake, 'collapsed').value).toBe('−662');
    expect(byKey(model.intake, 'issues').value).toBe('795');
    expect(byKey(model.intake, 'investigations').value).toBe('54');
    expect(byKey(model.intake, 'investigations').secondary).toBe('6 running');
  });

  it('renders column B tiles, colouring only P1 and coverage', () => {
    expect(model.ranking.map((tile) => tile.key)).toEqual(['p1', 'p2', 'p3', 'p0', 'coverage', 'firing']);

    const p1 = byKey(model.ranking, 'p1');
    expect(p1.value).toBe('2');
    expect(p1.secondary).toBe('of 795');
    expect(p1.tone).toBe('critical');

    expect(byKey(model.ranking, 'p2').secondary).toBe('4.3%');
    expect(byKey(model.ranking, 'p3').secondary).toBe('95.5%');
    expect(byKey(model.ranking, 'p0').value).toBe('0');
    expect(byKey(model.ranking, 'p0').tone).toBe('default');
    expect(byKey(model.ranking, 'p0').secondary).toBe('54 in 30d');
    expect(byKey(model.ranking, 'firing').value).toBe('32');
    expect(byKey(model.ranking, 'firing').tone).toBeUndefined();

    const coverage = byKey(model.ranking, 'coverage');
    expect(coverage.value).toBe('100%');
    expect(coverage.secondary).toBe('795 of 795');
    expect(coverage.tone).toBe('positive');
  });

  it('renders column C — the three verdicts sum to the total row', () => {
    expect(model.comparison.map((row) => row.key)).toEqual(['below', 'agreed', 'above', 'judged']);

    expect(byKey(model.comparison, 'below').value).toBe('558');
    expect(byKey(model.comparison, 'below').secondary).toBe('70.2%');
    expect(byKey(model.comparison, 'agreed').value).toBe('225');
    expect(byKey(model.comparison, 'agreed').secondary).toBe('28.3%');
    expect(byKey(model.comparison, 'above').value).toBe('12');
    expect(byKey(model.comparison, 'above').secondary).toBe('1.5%');

    const judged = byKey(model.comparison, 'judged');
    expect(judged.value).toBe('795');
    expect(judged.emphasis).toBe(true);
    expect(558 + 225 + 12).toBe(795);
    expect(model.comparisonMeta).toBe('sums to 795');
  });

  it('names the biggest disagreement and the escalations as tiles', () => {
    const biggest = byKey(model.disagreementTiles, 'biggest');
    expect(biggest.value).toBe('237');
    expect(biggest.secondary).toBe('HIGH → P3');
    expect(biggest.tooltip).toBe('94% of the 251 HIGH alerts were put in the background.');
    // Constrains both axes at once — arrived as HIGH, ranked P3.
    expect(biggest.drill).toEqual({ status: 'ALL', eventPriority: 'HIGH', eventComputedPriority: 'P3' });

    // Only the largest transition sits on the value line — the rest would wrap
    // the tile onto three rows and drag column C taller than its neighbours.
    // Nothing reached P0/P1 in this fixture, so it falls back to naming the
    // largest transition. No "+N more" tail — the count already states the total.
    const raised = byKey(model.disagreementTiles, 'raised');
    expect(raised.value).toBe('12');
    expect(raised.secondary).toBe('10 INFO→P2');
    // The breakdown hangs off the number, not off the info icon.
    expect(raised.valueTooltip).toBe('10 INFO→P2 · 2 LOW→P2');
    expect(raised.tooltip).toContain('suppression engine');
  });

  it('leads the escalation line with what reached the queue, not the biggest cell', () => {
    const payload = prototypePayload();
    payload.disagreement = [
      ...payload.disagreement,
      { priority: 'LOW', computed_priority: 'P1', event_count: 2 },
      { priority: 'INFO', computed_priority: 'P0', event_count: 1 },
    ];
    const raised = byKey(resolveBriefing(payload).disagreementTiles, 'raised');

    // 10 INFO→P2 is still the largest cell, but P2 is not the queue: the three
    // issues that landed at P0/P1 lead, grouped by where they landed.
    expect(raised.value).toBe('15');
    expect(raised.secondary).toBe('1 → P0 · 2 → P1');
    expect(raised.valueTooltip).toContain('10 INFO→P2');
  });

  it('picks the biggest disagreement by rank distance, not raw volume', () => {
    // MEDIUM → P3 (321) outweighs HIGH → P3 (237) on count, but is only a
    // one-step departure. The two-step drop is the finding.
    expect(byKey(model.disagreementTiles, 'biggest').secondary).toBe('HIGH → P3');

    const payload = prototypePayload();
    payload.disagreement = payload.disagreement.filter((row) => !(row.priority === 'HIGH' && row.computed_priority === 'P3'));
    expect(byKey(resolveBriefing(payload).disagreementTiles, 'biggest').secondary).toBe('MEDIUM → P3');
  });

  it('renders column D — kind, entity and measurement per finding', () => {
    expect(model.findings.map((finding) => finding.kind)).toEqual(['NOISE SOURCE', 'TUNING AVAILABLE', 'DATA INTEGRITY']);
    expect(model.callouts.map((entry) => entry.key)).toEqual(['noise-source', 'tuning', 'data-integrity']);
    expect(model.flaggedMeta).toBe('3 fired');

    const noise = callout(model, 'noise-source');
    expect(noise.kind).toBe('NOISE SOURCE');
    expect(noise.title).toBe('image_pull_backoff_reporter');
    expect(noise.detail).toBe('358 events · 24.6% of all volume');
    expect(noise.tone).toBe('warning');

    expect(callout(model, 'tuning').title).toBe('OtelDemoHighLatency');
    expect(callout(model, 'tuning').detail).toBe('1,656 firings, scored false positive');

    // Each finding carries the one thing to do about it.
    expect(noise.action).toEqual({
      text: 'see the events →',
      drill: { status: 'ALL', eventAggregationKey: 'image_pull_backoff_reporter' },
    });
    expect(callout(model, 'tuning').action).toEqual({ text: 'review the fix →', href: '/troubleshoot#all-events/threshold-suggestions' });

    // Data integrity is a danger-toned finding, so it reads red rather than amber.
    expect(callout(model, 'data-integrity').title).toBe('81 events stuck firing');
    expect(callout(model, 'data-integrity').detail).toBe('median age 25 days — never closed');
    expect(callout(model, 'data-integrity').tone).toBe('critical');
  });

  it('notes the trigger that did not fire, without turning it into a finding', () => {
    // A muted line under the boxes, not a box of its own — nothing fired.
    expect(model.flaggedNote).toBe('Top-3 classes = 44.9% of volume — below the 50% concentration trigger, so no finding raised.');
    expect(model.callouts.map((entry) => entry.key)).not.toContain('concentration');
  });

  it('keeps the mapping assumption on column C, not in the flagged column', () => {
    expect(model.mappingCaveat).toContain('HIGH = P1 · MEDIUM = P2 · LOW/INFO = P3');
    expect(model.callouts.map((entry) => entry.key)).not.toContain('mapping-assumption');
  });

  it('resolves NORMAL mode', () => {
    expect(model.mode).toBe('NORMAL');
    expect(model.flags).toEqual({ degraded: true, coverageGap: false, backlog: false });
  });
});

describe('resolveBriefing — drill-downs', () => {
  const model = resolveBriefing(prototypePayload());

  it('opens the unfiltered list from the population tiles', () => {
    expect(byKey(model.intake, 'ingested').drill).toEqual({ status: 'ALL' });
    expect(byKey(model.intake, 'issues').drill).toEqual({ status: 'ALL' });
  });

  it('filters by Nubi rank from each rank tile', () => {
    expect(byKey(model.ranking, 'p0').drill).toEqual({ status: 'ALL', eventComputedPriority: 'P0' });
    expect(byKey(model.ranking, 'p1').drill).toEqual({ status: 'ALL', eventComputedPriority: 'P1' });
    expect(byKey(model.ranking, 'p2').drill).toEqual({ status: 'ALL', eventComputedPriority: 'P2' });
    expect(byKey(model.ranking, 'p3').drill).toEqual({ status: 'ALL', eventComputedPriority: 'P3' });
  });

  it('filters by alert state from the firing tile', () => {
    expect(byKey(model.ranking, 'firing').drill).toEqual({ status: 'ALL', eventStatus: 'FIRING' });
  });

  it('leaves tiles without a matching list filter unclickable', () => {
    expect(byKey(model.intake, 'collapsed').drill).toBeUndefined();
    expect(byKey(model.ranking, 'coverage').drill).toBeUndefined();
    expect(byKey(model.disagreementTiles, 'raised').drill).toBeUndefined();
  });
});

describe('resolveBriefing — determinism', () => {
  it('produces byte-identical output for the same payload', () => {
    const first = resolveBriefing(prototypePayload());
    const second = resolveBriefing(prototypePayload());
    expect(JSON.stringify(first)).toBe(JSON.stringify(second));
  });

  it('does not depend on the order rows arrive in', () => {
    const payload = prototypePayload();
    const shuffled = { ...payload, disagreement: [...payload.disagreement].reverse() };
    expect(resolveBriefing(shuffled).comparison).toEqual(resolveBriefing(payload).comparison);
  });
});

describe('resolveBriefing — mode resolution is exclusive', () => {
  it('enters INCIDENT on a P0, whatever the volume', () => {
    const payload = prototypePayload();
    payload.byRank = [...payload.byRank, { computed_priority: 'P0', event_count: 1 }];
    expect(resolveBriefing(payload).mode).toBe('INCIDENT');
  });

  it('enters INCIDENT at the P1 threshold, not below it', () => {
    const below = prototypePayload();
    below.byRank = [{ computed_priority: 'P1', event_count: INCIDENT_P1_THRESHOLD - 1 }];
    expect(resolveBriefing(below).mode).not.toBe('INCIDENT');

    const at = prototypePayload();
    at.byRank = [{ computed_priority: 'P1', event_count: INCIDENT_P1_THRESHOLD }];
    expect(resolveBriefing(at).mode).toBe('INCIDENT');
  });

  it('keeps showing the findings during an incident', () => {
    const payload = prototypePayload();
    payload.byRank = [...payload.byRank, { computed_priority: 'P0', event_count: 1 }];
    const model = resolveBriefing(payload);

    expect(model.mode).toBe('INCIDENT');
    expect(model.findings.map((finding) => finding.kind)).toEqual(['NOISE SOURCE', 'TUNING AVAILABLE', 'DATA INTEGRITY']);
    expect(model.flaggedMeta).toBe('3 fired');
    expect(model.callouts.map((entry) => entry.key)).not.toContain('flagged-status');
  });

  it('shows the same findings however wide the account scope is', () => {
    // The regression this guards: every "incident" trigger is a count, and counts
    // grow with the account filter — so a tenant-wide view tripped the threshold
    // and rendered an empty flagged column, while one account showed the same
    // findings fine. Scope must not decide what is flagged.
    const narrow = resolveBriefing(prototypePayload());

    const wide = prototypePayload();
    wide.byRank = [{ computed_priority: 'P1', event_count: 40 }, ...wide.byRank.slice(1)];
    const wideModel = resolveBriefing(wide);

    expect(wideModel.findings.map((finding) => finding.kind)).toEqual(narrow.findings.map((finding) => finding.kind));
    expect(wideModel.flaggedMeta).toBe(narrow.flaggedMeta);
  });
});

describe('resolveBriefing — flags fire independently', () => {
  it('raises coverageGap and backlog from unscored issues', () => {
    const payload = prototypePayload();
    payload.byRank = [...payload.byRank, { computed_priority: null, event_count: BACKLOG_THRESHOLD + 1 }];
    const model = resolveBriefing(payload);

    expect(model.flags.coverageGap).toBe(true);
    expect(model.flags.backlog).toBe(true);
    expect(model.findings.map((finding) => finding.kind).slice(0, 2)).toEqual(['COVERAGE GAP', 'BACKLOG']);

    const coverage = byKey(model.ranking, 'coverage');
    expect(coverage.tone).toBe('default');
    expect(coverage.tooltip).toBe('26 issues carry no ranking yet — the numbers above are incomplete.');
  });

  it('raises coverageGap without backlog below the threshold', () => {
    const payload = prototypePayload();
    payload.byRank = [...payload.byRank, { computed_priority: null, event_count: 1 }];
    const model = resolveBriefing(payload);
    expect(model.flags.coverageGap).toBe(true);
    expect(model.flags.backlog).toBe(false);
  });

  it('clears degraded when nothing is stuck', () => {
    const payload = prototypePayload();
    payload.stuckFiring = [];
    const model = resolveBriefing(payload);
    expect(model.flags.degraded).toBe(false);
    expect(model.findings.map((finding) => finding.kind)).not.toContain('DATA INTEGRITY');
  });
});

describe('resolveBriefing — findings cap and omissions', () => {
  it('caps the findings and keeps the highest-ranked ones', () => {
    const payload = prototypePayload();
    payload.byRank = [...payload.byRank, { computed_priority: null, event_count: BACKLOG_THRESHOLD + 1 }];
    const model = resolveBriefing(payload);

    expect(model.findings).toHaveLength(FLAGGED_CAP);
    expect(model.findings.map((finding) => finding.rank)).toEqual([1, 2, 3, 4]);
  });

  it('omits the noise finding below its trigger', () => {
    const payload = prototypePayload();
    payload.bySignalClass = [
      { aggregation_key: 'a', event_count: 100 },
      { aggregation_key: 'b', event_count: 90 },
    ];
    expect(resolveBriefing(payload).findings.map((finding) => finding.kind)).not.toContain('NOISE SOURCE');
  });

  it('ignores threshold suggestions that are already applied', () => {
    const payload = prototypePayload();
    payload.thresholdSuggestions = [{ alert_name: 'Applied', apply_status: 'applied' }];
    expect(resolveBriefing(payload).findings.map((finding) => finding.kind)).not.toContain('TUNING AVAILABLE');
  });

  it('omits the investigations tile rather than rendering a zero', () => {
    const payload = prototypePayload();
    payload.investigations = null;
    expect(byKey(resolveBriefing(payload).intake, 'investigations')).toBeUndefined();
  });
});

describe('resolveBriefing — degenerate inputs', () => {
  const empty: BriefingPayload = {
    windowTotals: {},
    windowIssues: 0,
    byRank: [],
    byRank30d: [],
    disagreement: [],
    bySignalClass: [],
    firingNow: 0,
    stuckFiring: [],
    thresholdSuggestions: [],
    investigations: null,
    windowStartMs: NOW - 86400000,
    windowEndMs: NOW,
    nowMs: NOW,
  };

  it('renders an empty window without dividing by zero', () => {
    const model = resolveBriefing(empty);
    expect(model.header.ingested).toBe(0);
    expect(byKey(model.comparison, 'judged').value).toBe('0');
    expect(byKey(model.ranking, 'coverage').value).toBe('0%');
    expect(model.findings).toEqual([]);
    // Nothing to report, and the column says so rather than sitting blank.
    expect(callout(model, 'flagged-status').kind).toBe('NOTHING FLAGGED');
    expect(callout(model, 'flagged-status').detail).toBe('No trigger fired this window.');
    // The assumption survives even an empty window — it is about the method.
    expect(model.mappingCaveat).toBeTruthy();
  });

  it('drops the disagreement tiles when there is nothing to report', () => {
    expect(resolveBriefing(empty).disagreementTiles).toEqual([]);
  });

  it('never reports negative collapsed occurrences when issues exceed events', () => {
    const model = resolveBriefing({ ...empty, windowTotals: { event_count: 5 }, windowIssues: 10 });
    expect(byKey(model.intake, 'collapsed').value).toBe('−0');
  });

  it('excludes unscored rows from the disagreement percentages', () => {
    const model = resolveBriefing({
      ...empty,
      windowIssues: 10,
      disagreement: [
        { priority: 'HIGH', computed_priority: 'P1', event_count: 4 },
        { priority: 'HIGH', computed_priority: null, event_count: 6 },
      ],
    });
    expect(byKey(model.comparison, 'judged').value).toBe('4');
    expect(byKey(model.comparison, 'agreed').value).toBe('4');
  });
});

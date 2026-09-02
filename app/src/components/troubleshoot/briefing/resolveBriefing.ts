import { formatCount, formatDays, formatPercent, formatShare, weightedMedian } from './format';
import { classifyDisagreement, MAPPING_DISCLOSURE, RANK_ORDER, SOURCE_SEVERITY_TO_RANK, type NubiRank, type SourceSeverity } from './severityRank';

export const INCIDENT_P1_THRESHOLD = 3;
export const BACKLOG_THRESHOLD = 25;
export const NOISE_TOP1_TRIGGER_PCT = 20;
export const NOISE_TOP3_TRIGGER_PCT = 50;
export const FLAGGED_CAP = 4;

export interface RankRow {
  computed_priority?: string | null;
  event_count?: number | null;
}

export interface DisagreementRow {
  priority?: string | null;
  computed_priority?: string | null;
  event_count?: number | null;
}

export interface BucketRow {
  created_at?: string | null;
  event_count?: number | null;
}

export interface SignalClassRow {
  aggregation_key?: string | null;
  event_count?: number | null;
}

export interface ThresholdSuggestionLike {
  alert_name?: string | null;
  event_aggregation_key?: string | null;
  alert_quality?: any;
  firing_analysis?: any;
  apply_status?: string | null;
}

export interface BriefingPayload {
  windowTotals: Record<string, number | null | undefined>;
  windowIssues: number;
  byRank: RankRow[];
  byRank30d: RankRow[];
  disagreement: DisagreementRow[];
  bySignalClass: SignalClassRow[];
  firingNow: number;
  stuckFiring: BucketRow[];
  thresholdSuggestions: ThresholdSuggestionLike[];
  investigations: { total: number; completed: number } | null;
  windowStartMs: number;
  windowEndMs: number;
  nowMs: number;
}

export type TileTone = 'default' | 'critical' | 'positive';

export interface BriefingTile {
  key: string;
  label: string;
  tooltip?: string;
  value: string;
  valueTooltip?: string;
  secondary?: string;
  tone?: TileTone;
  drill?: Record<string, string>;
}

export interface ComparisonRow {
  key: string;
  label: string;
  tooltip?: string;
  value: string;
  secondary?: string;
  emphasis?: boolean;
  drill?: Record<string, string>;
}

export interface BriefingCallout {
  key: string;
  tone: 'warning' | 'critical' | 'info';
  kind: string;
  title?: string;
  detail: string;
  action?: { text: string; drill?: Record<string, string>; href?: string };
}

export type FlaggedKind = 'COVERAGE GAP' | 'BACKLOG' | 'NOISE SOURCE' | 'TUNING AVAILABLE' | 'DATA INTEGRITY';

export interface FlaggedFinding {
  key: string;
  kind: FlaggedKind;
  tone: 'danger' | 'warning' | 'info';
  title: string;
  detail: string;
  rank: number;
  action?: { text: string; drill?: Record<string, string>; href?: string };
}

export type BriefingMode = 'INCIDENT' | 'NORMAL';

export interface BriefingModel {
  mode: BriefingMode;
  flags: { degraded: boolean; coverageGap: boolean; backlog: boolean };
  header: {
    ingested: number;
    issues: number;
    aboveP3: number;
    p1: number;
    p2: number;
  };
  intake: BriefingTile[];
  ranking: BriefingTile[];
  comparison: ComparisonRow[];
  disagreementTiles: BriefingTile[];
  comparisonMeta: string;
  callouts: BriefingCallout[];
  mappingCaveat: string;
  flaggedMeta: string;
  flaggedNote: string | null;
  findings: FlaggedFinding[];
}

const ALL_EVENTS_DRILL = { status: 'ALL' };

const rankDrill = (rank: NubiRank) => ({ ...ALL_EVENTS_DRILL, eventComputedPriority: rank });

const num = (value: unknown): number => (typeof value === 'number' && Number.isFinite(value) ? value : 0);

const sumRank = (rows: RankRow[], rank: NubiRank): number =>
  rows.filter((row) => (row.computed_priority || '').toUpperCase() === rank).reduce((total, row) => total + num(row.event_count), 0);

const sumUnscored = (rows: RankRow[]): number => rows.filter((row) => !row.computed_priority).reduce((total, row) => total + num(row.event_count), 0);

export const resolveBriefing = (payload: BriefingPayload): BriefingModel => {
  const ingested = num(payload.windowTotals?.event_count);
  const issues = payload.windowIssues;
  const collapsed = Math.max(0, ingested - issues);

  const intake: BriefingTile[] = [
    {
      key: 'ingested',
      label: 'Events ingested',
      tooltip: 'Raw events received in this window, before any deduplication.',
      value: formatCount(ingested),
      drill: ALL_EVENTS_DRILL,
    },
    {
      key: 'collapsed',
      label: 'Repeats collapsed',
      tooltip: 'Repeat occurrences of an issue already counted — folded into one issue rather than shown again.',
      value: `−${formatCount(collapsed)}`,
    },
    {
      key: 'issues',
      label: 'Issues you could see',
      tooltip: 'Distinct issues after repeats were collapsed — what the list below shows.',
      value: formatCount(issues),
      drill: ALL_EVENTS_DRILL,
    },
  ];

  if (payload.investigations) {
    const running = Math.max(0, payload.investigations.total - payload.investigations.completed);
    intake.push({
      key: 'investigations',
      label: 'Investigations run',
      tooltip: 'Investigations opened against issues in this window.',
      value: formatCount(payload.investigations.total),
      secondary: running > 0 ? `${formatCount(running)} running` : undefined,
    });
  }

  const p0 = sumRank(payload.byRank, 'P0');
  const p1 = sumRank(payload.byRank, 'P1');
  const p2 = sumRank(payload.byRank, 'P2');
  const p3 = sumRank(payload.byRank, 'P3');
  const unscored = sumUnscored(payload.byRank);
  const scored = Math.max(0, issues - unscored);
  const p0Last30d = sumRank(payload.byRank30d, 'P0');

  const ranking: BriefingTile[] = [
    {
      key: 'p1',
      label: 'Ranked P1',
      tooltip: 'Issues ranked P1 — the ones asking for your attention first.',
      value: formatCount(p1),
      secondary: `of ${formatCount(issues)}`,
      tone: p1 > 0 ? 'critical' : 'default',
      drill: rankDrill('P1'),
    },
    { key: 'p2', label: 'Ranked P2', value: formatCount(p2), secondary: formatShare(p2, issues), drill: rankDrill('P2') },
    {
      key: 'p3',
      label: 'Ranked P3',
      tooltip: 'Ranked low enough to stay out of the queue — visible, but not asking for action.',
      value: formatCount(p3),
      secondary: formatShare(p3, issues),
      drill: rankDrill('P3'),
    },
    {
      key: 'p0',
      label: 'Ranked P0',
      tooltip: 'P0 is reserved for the highest-urgency issues. The trailing figure is the last 30 days.',
      value: formatCount(p0),
      secondary: p0Last30d > 0 ? `${formatCount(p0Last30d)} in 30d` : undefined,
      tone: p0 > 0 ? 'critical' : 'default',
      drill: rankDrill('P0'),
    },
    {
      key: 'coverage',
      label: 'Coverage',
      tooltip:
        unscored === 0
          ? 'Every issue carries a ranking, so nothing above is missing a verdict.'
          : `${formatCount(unscored)} issues carry no ranking yet — the numbers above are incomplete.`,
      value: formatShare(scored, issues),
      secondary: `${formatCount(scored)} of ${formatCount(issues)}`,
      tone: unscored === 0 ? 'positive' : 'default',
    },
    {
      key: 'firing',
      label: 'Still firing now',
      tooltip: 'Issues whose underlying alert has not cleared.',
      value: formatCount(payload.firingNow),
      drill: { ...ALL_EVENTS_DRILL, eventStatus: 'FIRING' },
    },
  ];

  const totals = { below: 0, agreed: 0, above: 0, scored: 0 };
  const perSourceSeverity = new Map<string, number>();
  const downgrades: { count: number; from: string; to: string; distance: number }[] = [];
  const raised: { count: number; from: string; to: string; distance: number }[] = [];

  payload.disagreement.forEach((row) => {
    const count = num(row.event_count);
    if (count === 0) return;
    const verdict = classifyDisagreement(row.priority, row.computed_priority);
    if (verdict === 'unscored') return;

    const from = (row.priority || '').toUpperCase();
    const to = (row.computed_priority || '').toUpperCase();
    totals[verdict] += count;
    totals.scored += count;
    perSourceSeverity.set(from, (perSourceSeverity.get(from) || 0) + count);

    const distance = Math.abs(RANK_ORDER.indexOf(to as NubiRank) - RANK_ORDER.indexOf(SOURCE_SEVERITY_TO_RANK[from as SourceSeverity]));
    if (verdict === 'below') downgrades.push({ count, from, to, distance });
    if (verdict === 'above') raised.push({ count, from, to, distance });
  });

  const byCountThenLabel = (a: { count: number; from: string; to: string }, b: { count: number; from: string; to: string }) =>
    b.count - a.count || `${a.from}${a.to}`.localeCompare(`${b.from}${b.to}`);

  downgrades.sort((a, b) => b.distance - a.distance || byCountThenLabel(a, b));
  raised.sort(byCountThenLabel);

  const judged = totals.scored;
  const top = downgrades[0];

  const comparison: ComparisonRow[] = [
    {
      key: 'below',
      label: 'Ranked below the source severity',
      tooltip: 'The source system called these more urgent than the ranking did.',
      value: formatCount(totals.below),
      secondary: formatShare(totals.below, judged),
    },
    { key: 'agreed', label: 'Agreed with the source', value: formatCount(totals.agreed), secondary: formatShare(totals.agreed, judged) },
    {
      key: 'above',
      label: 'Ranked above the source severity',
      tooltip: 'Ranked more urgent than the source system claimed.',
      value: formatCount(totals.above),
      secondary: formatShare(totals.above, judged),
    },
    {
      key: 'judged',
      label: 'Total issues',
      tooltip: 'Issues carrying both a source severity and a rank. The three rows above sum to this.',
      value: formatCount(judged),
      secondary: formatShare(judged, judged),
      emphasis: true,
    },
  ];

  const disagreementTiles: BriefingTile[] = [];

  if (top) {
    disagreementTiles.push({
      key: 'biggest',
      label: 'Biggest disagreement',
      tooltip: `${formatPercent(top.count, perSourceSeverity.get(top.from) || top.count)} of the ${formatCount(
        perSourceSeverity.get(top.from) || top.count
      )} ${top.from} alerts ${top.to === 'P3' ? 'were put in the background' : `were ranked ${top.to}`}.`,
      value: formatCount(top.count),
      secondary: `${top.from} → ${top.to}`,
      drill: { ...ALL_EVENTS_DRILL, eventPriority: top.from, eventComputedPriority: top.to },
    });
  }

  if (raised.length > 0) {
    const asTransition = (entry: { count: number; from: string; to: string }) => `${formatCount(entry.count)} ${entry.from}→${entry.to}`;

    const intoQueue = (['P0', 'P1'] as NubiRank[])
      .map((rank) => ({ rank, count: raised.filter((entry) => entry.to === rank).reduce((sum, entry) => sum + entry.count, 0) }))
      .filter((group) => group.count > 0);

    const parts = intoQueue.length ? intoQueue.map((group) => `${formatCount(group.count)} → ${group.rank}`) : [asTransition(raised[0])];

    disagreementTiles.push({
      key: 'raised',
      label: 'Raised above source',
      tooltip: 'A system that only ever downgrades is a suppression engine, so upgrades stay visible even at one.',
      value: formatCount(totals.above),
      valueTooltip: raised.map(asTransition).join(' · '),
      secondary: parts.filter(Boolean).join(' · '),
    });
  }

  const signalClasses = payload.bySignalClass.map((row) => ({ key: row.aggregation_key || 'unknown', count: num(row.event_count) }));
  const worst = signalClasses[0];
  const top3Share = signalClasses.slice(0, 3).reduce((total, row) => total + row.count, 0);
  const top3Pct = ingested > 0 ? (top3Share / ingested) * 100 : 0;
  const worstPct = worst && ingested > 0 ? (worst.count / ingested) * 100 : 0;

  const stuckCount = payload.stuckFiring.reduce((total, row) => total + num(row.event_count), 0);
  const stuckMedianDays = weightedMedian(
    payload.stuckFiring
      .filter((row) => row.created_at)
      .map((row) => ({
        value: Math.max(0, Math.floor((payload.nowMs - new Date(row.created_at as string).getTime()) / 86400000)),
        weight: num(row.event_count),
      }))
  );

  const openSuggestions = payload.thresholdSuggestions.filter((suggestion) => suggestion.apply_status !== 'applied');
  const topSuggestion = openSuggestions[0];

  const coverageGap = unscored > 0;
  const backlog = unscored > BACKLOG_THRESHOLD;
  const degraded = stuckCount > 0;

  const flagged: FlaggedFinding[] = [];

  if (coverageGap) {
    flagged.push({
      key: 'coverage-gap',
      kind: 'COVERAGE GAP',
      tone: 'danger',
      title: `${formatCount(unscored)} issues unscored`,
      detail: `${formatShare(scored, issues)} coverage — every number above is incomplete`,
      rank: 1,
    });
  }

  if (backlog) {
    flagged.push({
      key: 'backlog',
      kind: 'BACKLOG',
      tone: 'danger',
      title: `${formatCount(unscored)} untriaged`,
      detail: `above the ${BACKLOG_THRESHOLD} threshold — Nubi is behind`,
      rank: 2,
    });
  }

  if (worst && worstPct >= NOISE_TOP1_TRIGGER_PCT) {
    flagged.push({
      key: 'noise-source',
      kind: 'NOISE SOURCE',
      tone: 'warning',
      title: worst.key,
      detail: `${formatCount(worst.count)} events · ${formatShare(worst.count, ingested)} of all volume`,
      rank: 3,
      action: { text: 'see the events →', drill: { ...ALL_EVENTS_DRILL, eventAggregationKey: worst.key } },
    });
  }

  if (topSuggestion) {
    const firings = num(topSuggestion.firing_analysis?.total_firings);
    const quality = topSuggestion.alert_quality?.verdict || topSuggestion.alert_quality?.classification;
    flagged.push({
      key: 'tuning',
      kind: 'TUNING AVAILABLE',
      tone: 'warning',
      title: topSuggestion.alert_name || topSuggestion.event_aggregation_key || 'Threshold suggestion',
      detail: [firings > 0 ? `${formatCount(firings)} firings` : null, quality ? `scored ${String(quality).replace(/_/g, ' ')}` : null]
        .filter(Boolean)
        .join(', '),
      rank: 4,
      action: { text: 'review the fix →', href: '/troubleshoot#all-events/threshold-suggestions' },
    });
  }

  if (degraded) {
    flagged.push({
      key: 'data-integrity',
      kind: 'DATA INTEGRITY',
      tone: 'danger',
      title: `${formatCount(stuckCount)} events stuck firing`,
      detail: `median age ${formatDays(stuckMedianDays)} — never closed`,
      rank: 5,
    });
  }

  flagged.sort((a, b) => a.rank - b.rank);

  const concentrationNote =
    signalClasses.length > 0 && top3Pct < NOISE_TOP3_TRIGGER_PCT
      ? `Top-3 classes = ${formatShare(
          top3Share,
          ingested
        )} of volume — below the ${NOISE_TOP3_TRIGGER_PCT}% concentration trigger, so no finding raised.`
      : null;

  let mode: BriefingMode = 'NORMAL';
  if (p0 > 0 || p1 >= INCIDENT_P1_THRESHOLD) mode = 'INCIDENT';

  const visibleFindings = flagged.slice(0, FLAGGED_CAP);

  const callouts: BriefingCallout[] = visibleFindings.map((finding) => ({
    key: finding.key,
    tone: finding.tone === 'danger' ? 'critical' : 'warning',
    kind: finding.kind,
    title: finding.title,
    detail: finding.detail,
    action: finding.action,
  }));

  if (visibleFindings.length === 0) {
    callouts.push({
      key: 'flagged-status',
      tone: 'warning',
      kind: 'NOTHING FLAGGED',
      detail: 'No trigger fired this window.',
    });
  }

  return {
    mode,
    flags: { degraded, coverageGap, backlog },
    header: { ingested, issues, aboveP3: p0 + p1 + p2, p1, p2 },
    intake,
    ranking,
    comparison,
    disagreementTiles,
    comparisonMeta: `sums to ${formatCount(judged)}`,
    callouts,
    mappingCaveat: MAPPING_DISCLOSURE,
    flaggedMeta: `${formatCount(visibleFindings.length)} fired`,
    flaggedNote: concentrationNote,
    findings: visibleFindings,
  };
};

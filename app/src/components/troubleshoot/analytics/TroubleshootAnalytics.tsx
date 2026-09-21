import { useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import apiKubernetes1 from '@api1/kubernetes1';
import apiTriage from '@api1/triage';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { digestsList, digestGet, type Digest } from '@api1/digests';
import { Skeleton } from '@ui/Skeleton';
import { Banner } from '@ui/Banner';
import { Stat, type DeltaTone } from '@ui/Stat';
import WidgetCard from '@ui/WidgetCard';
import TimeSeriesChart from '@components/common/charts/TimeSeriesChart';
import { useBriefingWindow } from '@components/troubleshoot/briefing/useBriefingData';
import { useRouter } from 'next/router';
import { ds } from 'src/utils/colors';

// Troubleshoot > Analytics.
//
// The briefing above this tab already reports what came in and how Nubi ranked
// it. Repeating those counts here would add nothing, so this tab only answers
// what the briefing structurally cannot:
//
//   1. Are we improving?   -> this window against the one before it
//   2. What keeps coming back? -> named chains, not a recurrence percentage
//
// A percentage ("87% of issues recur") tells a reader there is a problem. A
// named list tells them which one to go fix, so the list is the centrepiece and
// the rate is a supporting stat.

interface Props {
  /**
   * Same handler the briefing uses — clears sticky filters, pins the window,
   * switches to the Events tab.
   *
   * Required rather than optional: every tile, chart column and finding on this
   * tab renders with role="button" and a pointer cursor, so without a handler
   * the whole surface looks interactive and does nothing.
   */
  onDrillDown: (query: Record<string, string>) => void;
  /**
   * Account/date filter bar, rendered on the "Overview" heading row inside the
   * panel. Passed in rather than owned here so the same BriefingFilters instance
   * the All Events tab uses drives this tab too.
   */
  filters?: React.ReactNode;
}

interface Row {
  created_at?: string;
  count_analysed_issues?: number;
  minutes_to_first_analysis?: number;
  computed_priority?: string;
  latest_computed_priority?: string;
  count_subject_name?: number;
  distinct_subject_name?: string;
  aggregation_key?: string;
  subject_name?: string;
  fingerprint?: string;
  fingerprint_event_count?: number;
  fingerprint_first_seen_at?: string;
  event_count?: number;
}

const rowsOf = (block: any): Row[] => block?.rows ?? [];
const num = (value: unknown): number => (typeof value === 'number' && Number.isFinite(value) ? value : 0);
const first = (block: any): number => num(rowsOf(block)[0]?.event_count);

const dayKey = (value?: string): string => (value ? String(value).slice(0, 10) : '');

const dayLabel = (key: string): string => {
  const parsed = new Date(`${key}T00:00:00Z`);
  if (Number.isNaN(parsed.getTime())) return key;
  return parsed.toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' });
};

const dayBounds = (key: string): { start: string; end: string } => {
  const start = Date.parse(`${key}T00:00:00Z`);
  return { start: String(start), end: String(start + 86399999) };
};

/**
 * The single workload behind a chain, when there is exactly one.
 *
 * distinct_subject_name comes back as a JSON array rendered to text by the query
 * engine, so it needs parsing rather than reading. Returns '' when the chain
 * spans several workloads — the caller shows a count instead.
 */
const firstSubject = (value?: string): string => {
  if (!value) return '';
  try {
    const parsed = JSON.parse(value);
    return Array.isArray(parsed) && parsed.length === 1 && typeof parsed[0] === 'string' ? parsed[0] : '';
  } catch {
    return '';
  }
};

/** Whole days between a first-seen timestamp and now. Used to age a recurring chain. */
const ageInDays = (value?: string): number | null => {
  if (!value) return null;
  const seen = Date.parse(value);
  if (Number.isNaN(seen)) return null;
  return Math.max(0, Math.floor((Date.now() - seen) / 86400000));
};

const ageLabel = (days: number | null): string => {
  if (days === null) return 'age unknown';
  if (days < 1) return 'first seen today';
  if (days === 1) return 'first seen yesterday';
  if (days < 14) return `recurring for ${days} days`;
  return `recurring for ${Math.floor(days / 7)} weeks`;
};

// Every card sits inside one white panel now. WidgetCard's default elevation is
// a big diffuse shadow that reads as "white card" against a grey page but casts
// grey onto a white panel — so it's swapped for a light crisp shadow that gives
// each tile just enough lift to read as a tile without dirtying the surface.
// mt:0 cancels WidgetCard's default top margin in the grids.
const CARD_SX = { mt: 0, boxShadow: '0 1px 3px rgba(16, 24, 40, 0.08)' } as const;

// `action` is an optional right-aligned slot on the heading row — used to sit the
// Viewing/Account/date filters parallel to the "Overview" title inside the panel.
const SectionHeading = ({ children, action }: { children: React.ReactNode; action?: React.ReactNode }) => (
  <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-3)', marginBottom: 'var(--ds-space-3)' }}>
    <Typography
      sx={{
        fontSize: 'var(--ds-text-small)',
        fontWeight: 'var(--ds-font-weight-semibold)',
        letterSpacing: '0.08em',
        textTransform: 'uppercase',
        color: ds.gray[600],
      }}
    >
      {children}
    </Typography>
    {action}
  </Box>
);

const Panel = ({ title, definition, children }: { title: string; definition: string; children: React.ReactNode }) => (
  <WidgetCard sx={{ ...CARD_SX, minWidth: 0 }}>
    <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>{title}</Typography>
    {/* Inline rather than behind a tooltip: this tab is read by people who do not
        know our internals, and a metric whose meaning is one hover away is a
        metric that gets misread. */}
    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600], marginBottom: 'var(--ds-space-3)' }}>{definition}</Typography>
    {children}
  </WidgetCard>
);

/**
 * One period-over-period stat.
 *
 * `betterWhenDown` decides the colour, not the sign: fewer issues is good,
 * fewer investigations is not, and a single "green means up" rule would tell
 * the reader the opposite of the truth on half these tiles.
 */
const DeltaStat = ({
  label,
  value,
  previous,
  betterWhenDown = true,
  onClick,
}: {
  label: string;
  value: number;
  previous: number;
  betterWhenDown?: boolean;
  onClick?: () => void;
}) => {
  const delta = value - previous;
  const pct = previous > 0 ? Math.round((delta / previous) * 100) : null;
  const flat = delta === 0 || pct === null;
  const improving = betterWhenDown ? delta < 0 : delta > 0;
  // Stat's delta owns the colour (via cost-axis tone) and the arrow (via
  // direction), so the "fewer-is-good" judgement maps to a tone rather than a
  // hand-picked green/red — the DS "don't pick delta colour manually" rule.
  const tone: DeltaTone = flat ? 'neutral' : improving ? 'savings' : 'waste';
  const direction: 'up' | 'down' | 'flat' = flat ? 'flat' : delta > 0 ? 'up' : 'down';
  const period = `vs previous ${previous.toLocaleString()}`;

  return (
    <WidgetCard
      {...(onClick
        ? {
            role: 'button',
            tabIndex: 0,
            onClick,
            onKeyDown: (event: React.KeyboardEvent) => {
              if (event.key === 'Enter' || event.key === ' ') onClick();
            },
          }
        : {})}
      sx={{
        ...CARD_SX,
        cursor: onClick ? 'pointer' : 'default',
        '&:hover': onClick ? { borderColor: ds.blue[500] } : {},
      }}
    >
      <Stat
        size='md'
        label={label}
        value={value.toLocaleString()}
        delta={{
          value: flat ? 'no change' : `${Math.abs(delta).toLocaleString()} (${Math.abs(pct)}%)`,
          period,
          tone,
          direction,
        }}
      />
    </WidgetCard>
  );
};

export default function TroubleshootAnalytics({ onDrillDown, filters }: Props) {
  const router = useRouter();
  const window = useBriefingWindow();
  const accountIdParam = router.query.accountIds;
  const accountIds = useMemo(() => (accountIdParam ? String(accountIdParam).split(',').filter(Boolean) : []), [accountIdParam]);

  // Analytics defaults to 7 days, not the app-wide 24h. Daily buckets over 24h
  // give two columns, which is not a trend — and unlike the All Events tab, this
  // one exists to show change over time. Written into the URL so the range
  // picker above reflects it and a copied link reproduces the same view.
  useEffect(() => {
    if (!router.isReady || router.query.start_time || router.query.end_time) return;
    const end = Date.now();
    router.replace(
      {
        pathname: router.pathname,
        query: { ...router.query, start_time: String(end - 7 * 86400000), end_time: String(end) },
        hash: router.asPath.split('#')[1],
      },
      undefined,
      { shallow: true }
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [router.isReady, router.query.start_time, router.query.end_time]);

  const [state, setState] = useState<{ loading: boolean; error: boolean; data: any; suggestions: any[] }>({
    loading: true,
    error: false,
    data: null,
    suggestions: [],
  });

  // The weekly digest is fetched separately and rendered in its own section.
  // It is TENANT-wide and WEEKLY, while everything else on this tab follows the
  // page's account filter and range picker — so it cannot be merged into the
  // other panels without the numbers quietly disagreeing. Its own section, with
  // its own period label, keeps that boundary visible to the reader.
  const [digest, setDigest] = useState<Digest | null>(null);
  const [expandedFinding, setExpandedFinding] = useState<string | null>(null);
  const [showAllFindings, setShowAllFindings] = useState(false);

  // Investigation effort for the same window. The backend already returns the
  // measured agent time AND the baseline/rate it wants compared against, so the
  // assumptions are server-side config rather than numbers invented in the UI —
  // which is the difference between a value claim you can defend and one you
  // cannot.
  const [effort, setEffort] = useState<any>(null);

  useEffect(() => {
    let cancelled = false;
    // account_id is a single value on this endpoint, and an EMPTY string means
    // tenant-wide — which is exactly the default state of this page, so bailing
    // out on "no account selected" left the whole Overview blank. Three cases:
    // none selected -> tenant-wide; one -> that account; several -> fetch each
    // and sum, because passing only the first silently under-reports and passing
    // '' would pull in accounts the reader has filtered out.
    const targets = accountIds.length === 0 ? [''] : accountIds;
    const startDate = new Date(window.startMs).toISOString();
    const endDate = new Date(window.endMs).toISOString();

    Promise.all(
      targets.map((accountId) =>
        apiAskNudgebee
          .getConversationTimeAggregates({ accountId, startDate, endDate, sources: ['Investigation'], eventScoped: true })
          .catch(() => null)
      )
    )
      .then((results: any[]) => {
        if (cancelled) return;
        const rows = results.filter(Boolean);
        if (rows.length === 0) {
          setEffort(null);
          return;
        }
        setEffort({
          completed_count: rows.reduce((total, row) => total + num(row.completed_count), 0),
          total_count: rows.reduce((total, row) => total + num(row.total_count), 0),
          total_agent_active_time_seconds: rows.reduce((total, row) => total + num(row.total_agent_active_time_seconds), 0),
          // Baseline and rate are tenant config, identical across accounts —
          // take the first rather than summing them into nonsense.
          manual_baseline_minutes: num(rows[0].manual_baseline_minutes),
          engineer_hourly_rate_usd: num(rows[0].engineer_hourly_rate_usd),
        });
      })
      .catch(() => {
        if (!cancelled) setEffort(null);
      });

    return () => {
      cancelled = true;
    };
  }, [window.startMs, window.endMs, accountIds]);

  useEffect(() => {
    let cancelled = false;
    // Two calls on purpose: the list endpoint returns period/status/metrics but
    // NOT the briefing, so the narrative only arrives from the per-week detail
    // call. Filtering the list on `briefing` would therefore always come up
    // empty, which is exactly how this first went wrong.
    digestsList({ limit: 4 })
      .then(async (response) => {
        if (cancelled) return;
        const latest = (response.data ?? []).find((entry) => entry.status === 'generated');
        if (!latest?.period_start) {
          setDigest(null);
          return;
        }
        // period_start comes back as a full timestamp; the detail call keys on
        // the week's date alone.
        const detail = await digestGet({ periodStart: String(latest.period_start).slice(0, 10) });
        if (cancelled) return;
        setDigest(detail.data?.briefing ? detail.data : null);
      })
      .catch(() => {
        // Guard on `cancelled` like every other effect here: without it an
        // unmounted component still takes a state update when the request loses.
        if (!cancelled) setDigest(null);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    setState((previous) => ({ ...previous, loading: true, error: false }));

    // Previous window = same length, immediately before. A fixed lookback would
    // compare a 24h window against 7 days and call the difference a trend.
    const span = window.endMs - window.startMs;

    // Threshold suggestions are fetched alongside the aggregates so a recurring
    // issue can be told apart from a recurring issue we already know how to fix.
    // Failing soft: a findings list minus its tuning hints is still useful, an
    // empty tab is not.
    const suggestions = apiTriage
      .listThresholdSuggestions({ cloud_account_ids: accountIds, limit: 50, offset: 0 })
      .then((response: any) => response?.suggestions ?? [])
      .catch(() => []);

    Promise.all([
      apiKubernetes1.analyticsAggregates({
        startDate: new Date(window.startMs).toISOString(),
        endDate: new Date(window.endMs).toISOString(),
        prevStartDate: new Date(window.startMs - span).toISOString(),
        prevEndDate: new Date(window.startMs).toISOString(),
        accountId: accountIds,
      }),
      suggestions,
    ])
      .then(([response, thresholdSuggestions]: any[]) => {
        if (cancelled) return;
        setState({ loading: false, error: false, data: response?.data?.data ?? {}, suggestions: thresholdSuggestions });
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        console.error('Failed to load troubleshoot analytics:', error);
        setState({ loading: false, error: true, data: null, suggestions: [] });
      });

    return () => {
      cancelled = true;
    };
  }, [window.startMs, window.endMs, accountIds]);

  // applyWidgetFilter deliberately clears sticky filters and pushes a clean URL
  // so the list matches the clicked number — but that also drops the page's
  // account scope, and every count here IS account-scoped. Without this the
  // reader clicks "119 issues for k8s-dev" and gets every account's rows.
  // The digest reviews the whole tenant while the rest of the tab follows the
  // account filter, so state how many accounts it actually spans rather than
  // leaving the reader to infer it from whichever rows made the cut.
  const digestAccountCount = useMemo(
    () => new Set((digest?.class_summaries ?? []).map((finding) => finding.account_name).filter(Boolean)).size,
    [digest]
  );

  const scope: Record<string, string> = useMemo(
    () => (accountIds.length > 0 ? ({ accountIds: accountIds.join(',') } as Record<string, string>) : ({} as Record<string, string>)),
    [accountIds]
  );

  const model = useMemo(() => {
    const data = state.data;
    if (!data) return null;

    const sumRanks = (block: any, ranks: string[]) =>
      rowsOf(block)
        .filter((row) => ranks.includes((row.computed_priority || '').toUpperCase()))
        .reduce((total, row) => total + num(row.event_count), 0);

    const issues = first(data.window_issues);
    // Coverage numerator and denominator come from the SAME aggregate row, over
    // the same distinct-fingerprint unit — the pairing is what stops the
    // per-event-over-per-chain mismatch that produced a 1.4% figure earlier
    // where the real one was 55%.
    const analysedIssues = num(rowsOf(data.window_issues)[0]?.count_analysed_issues);
    // Coverage is reported over the population auto-analysis actually targets,
    // not every problem seen. Both come back so the tile can name the narrower
    // denominator instead of implying it covers everything.
    const eligibleIssues = num(rowsOf(data.eligible_issues)[0]?.event_count);
    const eligibleAnalysed = num(rowsOf(data.eligible_issues)[0]?.count_analysed_issues);
    const mttuMinutes = num(rowsOf(data.window_issues)[0]?.minutes_to_first_analysis);
    const prevIssues = first(data.prev_issues);
    const urgent = sumRanks(data.by_rank, ['P0', 'P1']);
    const prevUrgent = sumRanks(data.prev_by_rank, ['P0', 'P1']);

    const volumeRows = rowsOf(data.daily_volume)
      .map((row) => ({ key: dayKey(row.created_at), value: num(row.event_count) }))
      .filter((row) => row.key)
      .sort((a, b) => a.key.localeCompare(b.key));

    // fingerprint_event_count is max(occurrence_number) over the chain, so >1
    // means this issue has fired before rather than being new.
    const chains = rowsOf(data.chains)
      .map((row) => ({
        fingerprint: row.fingerprint || '',
        aggregationKey: row.aggregation_key || '(unlabelled)',
        // Name the workload when the chain only touched one, and count them when
        // it touched several. Dropping the name entirely made distinct chains
        // that share an alert name — three separate PostgreSQLCacheHitRatio
        // problems on different databases — render as identical rows.
        workloads: num(row.count_subject_name),
        subject: firstSubject(row.distinct_subject_name),
        priority: (row.latest_computed_priority || '').toUpperCase(),
        occurrences: num(row.fingerprint_event_count),
        ageDays: ageInDays(row.fingerprint_first_seen_at),
      }))
      .filter((chain) => chain.fingerprint);

    const recurring = chains.filter((chain) => chain.occurrences > 1);
    // Rate over the capped sample, not the whole window — labelled as such in
    // the UI, because quoting it as a window-wide rate would be a lie.
    const recurrenceRate = chains.length > 0 ? Math.round((recurring.length / chains.length) * 100) : 0;

    const worst = [...recurring].sort((a, b) => b.occurrences - a.occurrences || (b.ageDays ?? 0) - (a.ageDays ?? 0)).slice(0, 10);

    // ── Findings ────────────────────────────────────────────────────────────
    //
    // Deterministic rules, not a model. Each one states its own evidence and
    // threshold so a reader can disagree with it — the same standard the
    // briefing sets with "below the 50% concentration trigger, so no finding
    // raised". A rule that fires without showing its arithmetic is just an
    // opinion with a badge on it.
    //
    // Rules are ordered by how much of the reader's week they would give back,
    // and each carries exactly one action, because a finding the reader cannot
    // act on is a statistic.
    const suggestionByKey = new Map<string, any>();
    (state.suggestions || []).forEach((suggestion: any) => {
      const key = suggestion?.event_aggregation_key || suggestion?.alert_name;
      if (key && !suggestionByKey.has(key)) suggestionByKey.set(key, suggestion);
    });

    const findings: {
      key: string;
      headline: string;
      evidence: string;
      actionText: string;
      action: () => void;
    }[] = [];

    // 1. Tuning already computed for something that keeps firing. The cheapest
    //    possible win: the fix exists and is one tab away.
    const tunable = recurring.filter((chain) => suggestionByKey.has(chain.aggregationKey)).sort((a, b) => b.occurrences - a.occurrences)[0];
    if (tunable) {
      const suggestion = suggestionByKey.get(tunable.aggregationKey);
      const reduction = num(suggestion?.estimated_reduction);
      findings.push({
        key: 'tuning',
        headline: `${tunable.aggregationKey} fired ${tunable.occurrences.toLocaleString()} times — and we already have a threshold for it`,
        evidence:
          reduction > 0
            ? `Applying the suggested threshold is estimated to cut ${Math.round(reduction)}% of its firings.`
            : 'A tuned threshold is ready to review.',
        actionText: 'Review in Alert Tuning',
        action: () => router.push({ hash: 'all-events/threshold-suggestions' }),
      });
    }

    // 2. Volume concentration. One class dominating means one rule buys back a
    //    disproportionate share of the noise.
    const byClass = new Map<string, number>();
    chains.forEach((chain) => byClass.set(chain.aggregationKey, (byClass.get(chain.aggregationKey) ?? 0) + chain.occurrences));
    const totalFirings = [...byClass.values()].reduce((sum, value) => sum + value, 0);
    const [topClass, topFirings] = [...byClass.entries()].sort((a, b) => b[1] - a[1])[0] ?? ['', 0];
    const topShare = totalFirings > 0 ? Math.round((topFirings / totalFirings) * 100) : 0;
    if (topClass && topShare >= 20) {
      findings.push({
        key: 'concentration',
        headline: `${topClass} is ${topShare}% of everything that fired`,
        evidence: `${topFirings.toLocaleString()} of ${totalFirings.toLocaleString()} firings across the issues sampled. One triage rule covers all of them.`,
        actionText: 'See these issues',
        action: () => onDrillDown({ status: 'ALL', ...scope, eventAggregationKey: topClass }),
      });
    }

    // 3. Long-running but rated low. Either the score is wrong or the issue
    //    should be suppressed — both are decisions someone has to make, and
    //    neither happens while it sits at P3 firing every day.
    const STALE_DAYS = 28;
    const staleLowRated = recurring.filter((chain) => (chain.ageDays ?? 0) >= STALE_DAYS && (chain.priority === 'P3' || chain.priority === ''));
    if (staleLowRated.length > 0) {
      const worstStale = staleLowRated.sort((a, b) => b.occurrences - a.occurrences)[0];
      findings.push({
        key: 'stale',
        headline: `${staleLowRated.length} issue${staleLowRated.length === 1 ? '' : 's'} recurring for over a month, still rated P3`,
        evidence: `Worst: ${worstStale.aggregationKey}${
          worstStale.workloads > 1 ? ` across ${worstStale.workloads} workloads` : worstStale.subject ? ` on ${worstStale.subject}` : ''
        }, ${worstStale.occurrences.toLocaleString()} firings over ${Math.floor(
          (worstStale.ageDays ?? 0) / 7
        )} weeks. Either it matters and the score is wrong, or it should be suppressed.`,
        actionText: 'See these issues',
        action: () => onDrillDown({ status: 'ALL', ...scope, eventAggregationKey: worstStale.aggregationKey }),
      });
    }

    // 4. Urgent load moving the wrong way. Volume going up is noise; P0/P1
    //    going up is the thing that costs someone a night.
    if (prevUrgent > 0 && urgent > prevUrgent) {
      findings.push({
        key: 'urgent',
        headline: `Urgent issues rose from ${prevUrgent} to ${urgent}`,
        evidence: 'Compared against the previous window of the same length.',
        actionText: 'See urgent issues',
        action: () => onDrillDown({ status: 'ALL', ...scope, eventComputedPriority: 'P1' }),
      });
    }

    return {
      issues,
      analysedIssues,
      eligibleIssues,
      eligibleAnalysed,
      mttuMinutes,
      prevIssues,
      urgent,
      prevUrgent,
      volumeRows,
      chains,
      recurring,
      recurrenceRate,
      worst,
      findings,
    };
  }, [state.data, state.suggestions, onDrillDown, router, scope]);

  // Keep the filter bar reachable in every state — a reader who lands on an
  // error or a slow load can still change the account/range to recover.
  const filtersRow = filters ? <Box sx={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 'var(--ds-space-3)' }}>{filters}</Box> : null;

  if (state.error) {
    return (
      <Box>
        {filtersRow}
        <Banner
          tone='critical'
          surface='section'
          title='Analytics unavailable'
          message="Couldn't load event aggregates for this window. The briefing above and the events list are unaffected."
        />
      </Box>
    );
  }

  if (state.loading || !model) {
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
        {filtersRow}
        <Skeleton shape='rect' height={120} />
        <Skeleton shape='rect' height={280} />
      </Box>
    );
  }

  const {
    issues,
    analysedIssues,
    eligibleIssues,
    eligibleAnalysed,
    mttuMinutes,
    prevIssues,
    urgent,
    prevUrgent,
    volumeRows,
    chains,
    recurring,
    recurrenceRate,
    worst,
    findings,
  } = model;

  // Writes the same URL params the range picker owns, so the whole page — this
  // tab and the briefing above it — moves together and the control still
  // reflects reality.
  const switchToSevenDays = () => {
    const end = Date.now();
    const start = end - 7 * 86400000;
    router.replace(
      { pathname: router.pathname, query: { ...router.query, start_time: String(start), end_time: String(end) }, hash: router.asPath.split('#')[1] },
      undefined,
      { shallow: true }
    );
  };

  const drillToDay = (index: number) => {
    const row = volumeRows[index];
    if (!row || !onDrillDown) return;
    const bounds = dayBounds(row.key);
    onDrillDown({ status: 'ALL', ...scope, start_time: bounds.start, end_time: bounds.end });
  };

  // A trend needs enough points to be a trend. At the 24h default this chart is
  // two bars and an average line drawn through them, which reads as a finding
  // and is not one — so say so instead of drawing it.
  const enoughHistory = volumeRows.length >= 4;

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-6)' }}>
      {/* Overview — the compact scoreboard.
          Deliberately four numbers, not six. An "RCA coverage" percentage is the
          obvious fifth, and it is left off on purpose: computing it correctly
          needs analysed-chains over ELIGIBLE-chains, and the only ratio derivable
          in the browser today is investigations over all issues, which understates
          it badly (35/2,146 reads 1.6% where the chain-level figure on prod is
          55%). A wrong coverage number is worse than none — see the MTTU column
          note in the analytics groundwork. */}
      <Box>
        <SectionHeading action={filters}>Overview</SectionHeading>
        <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr 1fr', lg: 'repeat(4, 1fr)' }, gap: 'var(--ds-space-3)' }}>
          <WidgetCard sx={CARD_SX}>
            <Stat
              size='md'
              label='Investigations finished'
              value={effort?.completed_count ? num(effort.completed_count).toLocaleString() : '—'}
              // Deliberately NOT "x of y". The backend's total_count windows on
              // updated_at rather than created_at, so it sweeps in conversations
              // created up to a year earlier that a cleanup job happened to touch —
              // on dev that turned a ~93% in-window completion rate into a reported
              // 8%. Until the DAO windows on created_at there is no trustworthy
              // denominator, so show none.
              sub='finished in this window'
            />
          </WidgetCard>
          {/* Coverage and MTTU come from the same aggregate row, so the numerator,
              the denominator and the clock all share one unit. Shown together on
              purpose: a fast MTTU next to low coverage says "quick on the few we
              do", which is the honest reading — either number alone flatters. */}
          <WidgetCard sx={CARD_SX}>
            <Stat
              size='md'
              label='Problems we explained'
              value={eligibleIssues > 0 ? `${Math.round((eligibleAnalysed / eligibleIssues) * 100)}%` : '—'}
              sub={`${eligibleAnalysed.toLocaleString()} of ${eligibleIssues.toLocaleString()} we try to explain automatically`}
              // Guidance names an operator setting, not a page: automatic
              // explanations are toggled per environment and per account in
              // configuration the product has no screen for, so pointing at a
              // button that does not exist would be worse than saying who to ask.
              info={{
                tooltip:
                  eligibleIssues > 0 && eligibleAnalysed / eligibleIssues < 0.5
                    ? 'Counts problems important enough for us to investigate on our own — repeat alerts and low-severity noise are left out. ' +
                      'A low number usually means automatic explanations are switched off for this environment or account, which an admin controls; ' +
                      'it can also mean problems are arriving below the severity we investigate without being asked. Ask an admin to turn on ' +
                      'automatic event explanations for this account.'
                    : 'Counts problems important enough for us to investigate on our own — repeat alerts and low-severity noise are left out.',
              }}
            />
          </WidgetCard>
          <WidgetCard sx={CARD_SX}>
            <Stat
              size='md'
              label='Time to explain a problem'
              // One or two samples is not a median. Below that the tile says so
              // rather than printing a number a reader would take as typical.
              value={analysedIssues >= 5 && mttuMinutes > 0 ? `${mttuMinutes.toFixed(1)} min` : '—'}
              sub={
                analysedIssues >= 5 && mttuMinutes > 0
                  ? `typical wait, across ${analysedIssues.toLocaleString()} problems`
                  : 'too few explained to give a typical time'
              }
            />
          </WidgetCard>
          {(() => {
            const completed = num(effort?.completed_count);
            const baselineMin = num(effort?.manual_baseline_minutes);
            const rate = num(effort?.engineer_hourly_rate_usd);
            const agentHours = num(effort?.total_agent_active_time_seconds) / 3600;
            const savedHours = Math.max(0, (completed * baselineMin) / 60 - agentHours);
            const savedCost = Math.round(savedHours * rate);
            return (
              <WidgetCard sx={CARD_SX}>
                <Stat
                  size='md'
                  label='Engineer time saved'
                  value={completed > 0 ? `${savedHours.toFixed(1)} hrs` : '—'}
                  sub={completed > 0 && rate > 0 ? `≈$${savedCost.toLocaleString()} vs doing this by hand` : 'vs doing the same first pass by hand'}
                  // No rate, no baseline, no arithmetic — anywhere, including the
                  // tooltip. Those are internal assumptions, and exposing them turns
                  // every reading of this tile into an argument about our model
                  // rather than the work it represents. The tooltip says what the
                  // number means; it does not show its inputs.
                  info={
                    completed > 0
                      ? {
                          tooltip: `An estimate of the first-pass investigation time your team did not have to spend, across the ${completed.toLocaleString()} problems the agents explained on their own.`,
                        }
                      : undefined
                  }
                />
              </WidgetCard>
            );
          })()}
        </Box>
      </Box>

      <Box>
        <SectionHeading>Are we improving?</SectionHeading>
        <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: '1fr 1fr', lg: 'repeat(3, 1fr)' }, gap: 'var(--ds-space-3)' }}>
          <DeltaStat label='Distinct problems' value={issues} previous={prevIssues} onClick={() => onDrillDown({ status: 'ALL', ...scope })} />
          <DeltaStat
            label='Problems we rated urgent'
            value={urgent}
            previous={prevUrgent}
            onClick={() => onDrillDown({ status: 'ALL', ...scope, eventComputedPriority: 'P1' })}
          />
          {/* No previous-period comparison, so no delta — but it sits in a row with
              two that do, so the same WidgetCard + Stat keeps its sizing aligned. */}
          <WidgetCard sx={CARD_SX}>
            <Stat
              size='md'
              label='Problems we have seen before'
              value={`${recurrenceRate}%`}
              sub={`${recurring.length.toLocaleString()} of the ${chains.length.toLocaleString()} busiest had happened before`}
            />
          </WidgetCard>
        </Box>
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600], marginTop: 'var(--ds-space-2)' }}>
          Compared against the {Math.max(1, Math.round((window.endMs - window.startMs) / 3600000))} hours immediately before this window.
        </Typography>
      </Box>

      {digest?.briefing && (
        <Box>
          <SectionHeading>The longer view — week of {String(digest.period_start).slice(0, 10)}</SectionHeading>
          <Panel
            title={digest.briefing.what_broke_lede || 'Weekly review'}
            definition={`From the weekly digest, which reviews the whole tenant rather than the account filter above. Covers ${String(
              digest.period_start
            ).slice(0, 10)} to ${String(digest.period_end).slice(0, 10)}.`}
          >
            {/* class_summaries is the digest's per-class verdict: a human label, the
                mechanism, the fix, a blast radius and how many weeks it has been
                carried. It is the richest thing we produce anywhere, and until now
                it only existed inside the b-Cortex Digests tab. Rendered as a list
                rather than prose because each row is independently actionable. */}
            {(digest.class_summaries ?? []).length > 0 && (
              <Box sx={{ marginBottom: 'var(--ds-space-4)' }}>
                {(showAllFindings ? (digest.class_summaries ?? []).filter(Boolean) : (digest.class_summaries ?? []).filter(Boolean).slice(0, 6)).map(
                  (finding) => {
                    const open = expandedFinding === finding.aggregation_key;
                    const periodStart = String(Date.parse(String(digest.period_start).slice(0, 10)));
                    const periodEnd = String(Date.parse(String(digest.period_end).slice(0, 10)));
                    // Evidence is pinned to the digest's OWN week and account —
                    // drilling with the page's current range would land the reader
                    // on a different population than the finding describes.
                    const seeEvidence = () =>
                      onDrillDown({
                        status: 'ALL',
                        accountIds: finding.cloud_account_id,
                        eventAggregationKey: finding.aggregation_key,
                        start_time: periodStart,
                        end_time: periodEnd,
                      });
                    return (
                      <Box
                        key={`${finding.aggregation_key}-${finding.cloud_account_id}`}
                        sx={{ borderBottom: `1px solid ${ds.gray[100]}`, padding: 'var(--ds-space-2) 0' }}
                      >
                        <Box
                          role='button'
                          tabIndex={0}
                          onClick={() => setExpandedFinding(open ? null : finding.aggregation_key)}
                          onKeyDown={(event: React.KeyboardEvent) => {
                            if (event.key === 'Enter' || event.key === ' ') setExpandedFinding(open ? null : finding.aggregation_key);
                          }}
                          sx={{ cursor: 'pointer', display: 'flex', justifyContent: 'space-between', gap: 'var(--ds-space-3)' }}
                        >
                          <Box sx={{ minWidth: 0 }}>
                            <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
                              {open ? '▾' : '▸'} {finding.label || finding.aggregation_key}
                            </Typography>
                            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600] }}>{finding.headline}</Typography>
                            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600] }}>
                              {finding.account_name} · {finding.events} events · {finding.recurrences} recurrences
                              {finding.carried_over_weeks ? ` · carried ${finding.carried_over_weeks}w` : ' · new this week'}
                              {finding.cause ? ` · ${finding.cause}` : ''}
                              {finding.confidence ? ` · ${finding.confidence} confidence` : ''}
                            </Typography>
                          </Box>
                          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600], whiteSpace: 'nowrap' }}>
                            {finding.priority}
                          </Typography>
                        </Box>

                        {open && (
                          <Box sx={{ padding: 'var(--ds-space-2) 0 var(--ds-space-2) var(--ds-space-4)' }}>
                            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[700] }}>{finding.problem}</Typography>
                            {finding.blast_radius && (
                              <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600], marginTop: 'var(--ds-space-1)' }}>
                                Affects: {finding.blast_radius}
                              </Typography>
                            )}
                            {finding.fix && (
                              <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[700], marginTop: 'var(--ds-space-2)' }}>
                                <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-semibold)' }}>
                                  Fix:{' '}
                                </Box>
                                {finding.fix}
                              </Typography>
                            )}
                            <Box
                              role='button'
                              tabIndex={0}
                              onClick={seeEvidence}
                              onKeyDown={(event: React.KeyboardEvent) => {
                                if (event.key === 'Enter' || event.key === ' ') seeEvidence();
                              }}
                              sx={{
                                display: 'inline-block',
                                marginTop: 'var(--ds-space-2)',
                                cursor: 'pointer',
                                color: ds.blue[500],
                                fontSize: 'var(--ds-text-small)',
                                fontWeight: 'var(--ds-font-weight-medium)',
                                '&:hover': { textDecoration: 'underline' },
                              }}
                            >
                              See the {finding.events} events behind this →
                            </Box>
                          </Box>
                        )}
                      </Box>
                    );
                  }
                )}
                {/* Never truncate silently: a capped list reads as "this is
                    everything", and here the top rows are dominated by one
                    account so the cap also hides whole accounts. */}
                {(digest.class_summaries ?? []).length > 6 && (
                  <Box
                    role='button'
                    tabIndex={0}
                    onClick={() => setShowAllFindings((previous) => !previous)}
                    onKeyDown={(event: React.KeyboardEvent) => {
                      if (event.key === 'Enter' || event.key === ' ') setShowAllFindings((previous) => !previous);
                    }}
                    sx={{
                      marginTop: 'var(--ds-space-2)',
                      cursor: 'pointer',
                      color: ds.blue[500],
                      fontSize: 'var(--ds-text-small)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      '&:hover': { textDecoration: 'underline' },
                    }}
                  >
                    {showAllFindings
                      ? 'Show fewer'
                      : `Showing 6 of ${(digest.class_summaries ?? []).length} findings across ${digestAccountCount} account${
                          digestAccountCount === 1 ? '' : 's'
                        } — show all →`}
                  </Box>
                )}
              </Box>
            )}

            <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '1fr 1fr' }, gap: 'var(--ds-space-4)' }}>
              {/* Carried-over and resolved are the only multi-week signals we
                  compute anywhere. Everything else on this tab is bounded by the
                  range picker, so "still here after 4 weeks" and "stopped after
                  N" cannot be derived from it — which is exactly why they are
                  worth borrowing rather than recomputing. */}
              <Box>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
                  Keeps coming back
                </Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600], marginBottom: 'var(--ds-space-1)' }}>
                  Named again in this week&apos;s review. The number is how many weekly reviews in a row have flagged it.
                </Typography>
                {(digest.briefing.carried_over ?? []).length === 0 ? (
                  <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600] }}>Nothing carried over.</Typography>
                ) : (
                  (digest.briefing.carried_over ?? [])
                    .filter(Boolean)
                    .slice(0, 6)
                    .map((item, index) => (
                      <Box
                        key={`${item.aggregation_key}-${item.account_name ?? index}`}
                        role='button'
                        tabIndex={0}
                        onClick={() => onDrillDown({ status: 'ALL', eventAggregationKey: item.aggregation_key })}
                        onKeyDown={(event: React.KeyboardEvent) => {
                          if (event.key === 'Enter' || event.key === ' ') onDrillDown({ status: 'ALL', eventAggregationKey: item.aggregation_key });
                        }}
                        sx={{
                          display: 'flex',
                          justifyContent: 'space-between',
                          gap: 'var(--ds-space-2)',
                          padding: 'var(--ds-space-1)',
                          borderRadius: 'var(--ds-radius-sm)',
                          cursor: 'pointer',
                          '&:hover': { background: ds.gray[100] },
                        }}
                      >
                        <Typography
                          sx={{
                            fontSize: 'var(--ds-text-small)',
                            color: ds.gray[700],
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                            whiteSpace: 'nowrap',
                          }}
                          title={item.aggregation_key}
                        >
                          {item.aggregation_key}
                          {item.account_name ? ` · ${item.account_name}` : ''}
                        </Typography>
                        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.red[500], whiteSpace: 'nowrap' }}>
                          {item.weeks} weeks running
                        </Typography>
                      </Box>
                    ))
                )}
              </Box>

              <Box>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
                  Stopped happening
                </Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600], marginBottom: 'var(--ds-space-1)' }}>
                  Flagged in earlier reviews but absent from this one, so it went quiet in the past week. The number is how long it had been running
                  before it stopped.
                </Typography>
                {(digest.briefing.resolved ?? []).length === 0 ? (
                  <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600] }}>Nothing dropped off this week.</Typography>
                ) : (
                  (digest.briefing.resolved ?? [])
                    .filter(Boolean)
                    .slice(0, 6)
                    .map((item, index) => (
                      <Box
                        key={`${item.aggregation_key}-${item.account_name ?? index}`}
                        sx={{ display: 'flex', justifyContent: 'space-between', gap: 'var(--ds-space-2)', padding: 'var(--ds-space-1)' }}
                      >
                        <Typography
                          sx={{
                            fontSize: 'var(--ds-text-small)',
                            color: ds.gray[700],
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                            whiteSpace: 'nowrap',
                          }}
                          title={item.aggregation_key}
                        >
                          {item.aggregation_key}
                          {item.account_name ? ` · ${item.account_name}` : ''}
                        </Typography>
                        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.green[500], whiteSpace: 'nowrap' }}>
                          gone (ran {item.weeks}w)
                        </Typography>
                      </Box>
                    ))
                )}
              </Box>
            </Box>

            {(digest.briefing.patterns ?? []).length > 0 && (
              <Box sx={{ marginTop: 'var(--ds-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
                {(digest.briefing.patterns ?? []).slice(0, 3).map((pattern) => (
                  <Box key={pattern.title}>
                    <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
                      {pattern.title}
                      <Box component='span' sx={{ color: ds.gray[600], fontWeight: 'var(--ds-font-weight-regular)' }}>
                        {' '}
                        · {pattern.stance}
                      </Box>
                    </Typography>
                    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600] }}>{pattern.body}</Typography>
                  </Box>
                ))}
              </Box>
            )}
          </Panel>
        </Box>
      )}

      {findings.length > 0 && (
        <Box>
          <SectionHeading>What should we do this week?</SectionHeading>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
            {findings.map((finding) => (
              // WidgetCard (not Card variant='accent') keeps the same elevation as
              // every other card on this tab; the blue left-edge stays as an sx
              // accent so the whole surface reads as one shadow system.
              <WidgetCard
                key={finding.key}
                sx={{
                  ...CARD_SX,
                  borderLeft: `3px solid ${ds.blue[500]}`,
                  display: 'flex',
                  alignItems: 'flex-start',
                  justifyContent: 'space-between',
                  gap: 'var(--ds-space-4)',
                }}
              >
                <Box sx={{ minWidth: 0 }}>
                  <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
                    {finding.headline}
                  </Typography>
                  {/* Every finding shows the arithmetic behind it so a reader can
                      disagree with the rule rather than take it on faith. */}
                  <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600] }}>{finding.evidence}</Typography>
                </Box>
                <Box
                  role='button'
                  tabIndex={0}
                  onClick={finding.action}
                  onKeyDown={(event: React.KeyboardEvent) => {
                    if (event.key === 'Enter' || event.key === ' ') finding.action();
                  }}
                  sx={{
                    flexShrink: 0,
                    cursor: 'pointer',
                    color: ds.blue[500],
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    whiteSpace: 'nowrap',
                    '&:hover': { textDecoration: 'underline' },
                  }}
                >
                  {finding.actionText} →
                </Box>
              </WidgetCard>
            ))}
          </Box>
        </Box>
      )}

      <Box>
        <SectionHeading>What keeps coming back?</SectionHeading>
        <Panel
          title='Issues that will fire again unless something changes'
          definition='Ranked by how many times each has fired. These are single issues, not alert volume — the count is how often this same problem recurred.'
        >
          {worst.length === 0 ? (
            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600] }}>
              Nothing in this window has fired more than once. Widen the range to see recurring problems.
            </Typography>
          ) : (
            <Box sx={{ display: 'flex', flexDirection: 'column' }}>
              {worst.map((chain) => (
                <Box
                  key={chain.fingerprint}
                  role='button'
                  tabIndex={0}
                  onClick={() => onDrillDown({ status: 'ALL', ...scope, eventAggregationKey: chain.aggregationKey })}
                  onKeyDown={(event: React.KeyboardEvent) => {
                    if (event.key === 'Enter' || event.key === ' ')
                      onDrillDown({ status: 'ALL', ...scope, eventAggregationKey: chain.aggregationKey });
                  }}
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    gap: 'var(--ds-space-3)',
                    padding: 'var(--ds-space-2)',
                    borderRadius: 'var(--ds-radius-sm)',
                    borderBottom: `1px solid ${ds.gray[100]}`,
                    cursor: 'pointer',
                    '&:hover': { background: ds.gray[100] },
                  }}
                >
                  <Box sx={{ minWidth: 0 }}>
                    <Typography
                      sx={{
                        fontSize: 'var(--ds-text-small)',
                        color: ds.gray[700],
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                      }}
                      title={chain.aggregationKey}
                    >
                      {chain.aggregationKey}
                    </Typography>
                    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600] }}>
                      {chain.workloads > 1 ? `${chain.workloads} workloads · ` : chain.subject ? `${chain.subject} · ` : ''}
                      {ageLabel(chain.ageDays)}
                      {chain.priority ? ` · ${chain.priority}` : ''}
                    </Typography>
                  </Box>
                  <Typography
                    sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700], whiteSpace: 'nowrap' }}
                  >
                    {chain.occurrences.toLocaleString()}×
                  </Typography>
                </Box>
              ))}
            </Box>
          )}
        </Panel>
      </Box>

      <Box>
        <SectionHeading>Issue volume</SectionHeading>
        <Panel title='New issues per day' definition={`Counted once per recurring alert. ${issues.toLocaleString()} issues over this window.`}>
          {enoughHistory ? (
            <TimeSeriesChart
              id='analytics-issue-volume'
              labels={volumeRows.map((row) => dayLabel(row.key))}
              series={[{ key: 'Issues', data: volumeRows.map((row) => row.value) }]}
              shape='bar'
              integerY
              showLegend={false}
              height={220}
              format={(value) => Math.round(value).toLocaleString()}
              compactFormat={(value) => Math.round(value).toLocaleString()}
              onSelectPoint={(_label, index) => drillToDay(index)}
            />
          ) : (
            <Box>
              <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600] }}>
                Only {volumeRows.length} {volumeRows.length === 1 ? 'day' : 'days'} in this window — not enough to show a trend.
              </Typography>
              {/* The page range drives this tab and defaults to 24h, which is two
                  day-buckets. Telling the reader to "widen the range" and leaving
                  them to find the control is a worse answer than doing it for
                  them; this writes the same start_time/end_time the picker does,
                  so the picker stays the single source of truth. */}
              <Box
                role='button'
                tabIndex={0}
                onClick={switchToSevenDays}
                onKeyDown={(event: React.KeyboardEvent) => {
                  if (event.key === 'Enter' || event.key === ' ') switchToSevenDays();
                }}
                sx={{
                  display: 'inline-block',
                  marginTop: 'var(--ds-space-2)',
                  cursor: 'pointer',
                  color: ds.blue[500],
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  '&:hover': { textDecoration: 'underline' },
                }}
              >
                Switch to the last 7 days →
              </Box>
            </Box>
          )}
        </Panel>
      </Box>
    </Box>
  );
}

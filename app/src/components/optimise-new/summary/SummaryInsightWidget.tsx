import { useMemo, type ReactNode } from 'react';
import { Box, Typography } from '@mui/material';
import { ds } from 'src/utils/colors';
import { Card } from '@ui/Card';
import { Label } from '@ui/Label';
import Tooltip from '@ui/Tooltip';
import { subtotal, formatDollars, formatWholeCurrency, type InsightItem, type MainCategory } from './insights';
import { resourceAnchorName } from './ResourceLabel';

// Findings open longer than this are surfaced in the "Flagged" column. Not an
// SLA — there is no SLA field, config or policy anywhere in the recommendation
// service — just a threshold this page draws the line at, so the label says
// what it measures ("open over 14 days") rather than implying a commitment.
const AGING_DAYS = 14;

const truncate = (s: string, max: number): string => (s.length > max ? `${s.slice(0, max)}…` : s);

// ─── Derived data from real InsightItem[] ──────────────────────────────────

const CATEGORY_LABELS: Record<MainCategory, string> = {
  cost: 'Cost recommendations',
  performance: 'Performance',
  security_config: 'Security & config',
};

interface InsightWidgetData {
  criticalCount: number;
  criticalDollars: number;
  newFindingsCount: number;
  newFindingsDollars: number;
  biggestOpportunity: { id: string; title: string; dollars: number } | null;
  /** Findings open longer than AGING_DAYS, with their savings and share of the shown total. */
  aging: { count: number; dollars: number; sharePct: number };
  /** The single longest-standing finding — "flagged N days ago, still open". */
  longestStanding: { id: string; days: number; title: string; resource: string; dollars: number } | null;
}

// Critical findings, new findings, and biggest opportunity are the only things
// this widget still derives from the curated top-100-by-urgency ∪
// top-100-by-impact list. Worst account and the category breakdown used to be
// computed the same way, but that undercounts once a tenant has more open
// findings than the curation pulls — they're now true tenant-wide aggregates
// passed in as props (`worstAccount`, `categoryBreakdown`), sourced the same
// way `totalSavings` already was.
const buildInsightWidgetData = (items: InsightItem[]): InsightWidgetData => {
  const criticalItems = items.filter((i) => i.severity === 'critical');
  const criticalDollars = subtotal(criticalItems);

  // "New" = detected in the last 24h. `ageDays` is already on every curated
  // item, so this needs no new fetch — it's just narrower than "of N total"
  // framing elsewhere, since it only sees new findings within the curated pool.
  const newItems = items.filter((i) => i.ageDays < 1);
  const newFindingsDollars = subtotal(newItems);

  const topItem = [...items].sort((a, b) => b.dollarImpact - a.dollarImpact)[0];
  const biggestOpportunity =
    topItem && topItem.dollarImpact > 0 ? { id: topItem.id, title: topItem.title || topItem.summary, dollars: topItem.dollarImpact } : null;

  // Age comes from `created_at` (see transformRecommendation), which is stable —
  // unlike `updated_at`, which the finops-score cron bumps every ~6h. Both of
  // these are computed over the curated pool for the same reason `newFindings`
  // is: recommendation_groupings_v2 has no `created_at` column, so there is no
  // full-set aggregate to ask (verified: the query engine rejects it).
  const agingItems = items.filter((i) => i.ageDays >= AGING_DAYS);
  const agingDollars = subtotal(agingItems);
  const shownDollars = subtotal(items);

  const oldest = [...items].sort((a, b) => b.ageDays - a.ageDays)[0];
  const longestStanding =
    oldest && oldest.ageDays >= AGING_DAYS
      ? {
          id: oldest.id,
          days: Math.floor(oldest.ageDays),
          title: oldest.title || oldest.summary,
          resource: oldest.resourceId,
          dollars: oldest.dollarImpact,
        }
      : null;

  return {
    criticalCount: criticalItems.length,
    criticalDollars,
    newFindingsCount: newItems.length,
    newFindingsDollars,
    biggestOpportunity,
    aging: {
      count: agingItems.length,
      dollars: agingDollars,
      sharePct: shownDollars > 0 ? Math.round((agingDollars / shownDollars) * 100) : 0,
    },
    longestStanding,
  };
};

// ─── Presentation — mirrors the Troubleshooting page's NubiBriefing widget
// (components/troubleshoot/briefing/{BriefingTile,BriefingCallout}.tsx): tinted
// bordered tiles stacked vertically, colour-accented callouts for flagged
// items, and a row-based table for the dollar breakdown — instead of bare
// stacked text or a cramped 2-up grid.

type TileTone = 'default' | 'critical' | 'positive';

const TILE_TONE: Record<TileTone, { background: string; border: string; value: string }> = {
  default: { background: ds.background[200], border: ds.gray[100], value: ds.brand[600] },
  critical: { background: ds.red[100], border: ds.red[200], value: ds.red[600] },
  positive: { background: ds.green[100], border: ds.green[200], value: ds.green[600] },
};

interface TileProps {
  label: string;
  value: string;
  secondary?: string;
  tone?: TileTone;
  /** Tags this one tile "Sample" — for a column that mixes real and placeholder tiles. */
  sample?: boolean;
  /** Overrides the value's font size — e.g. a long name that needs to read smaller than a number. */
  valueFontSize?: string;
  /** Full text shown on hover — pair with a truncated `value`. */
  tooltip?: string;
  onClick?: () => void;
}

const Tile = ({ label, value, secondary, tone = 'default', sample, valueFontSize, tooltip, onClick }: TileProps) => {
  const t = TILE_TONE[tone];
  const clickable = Boolean(onClick);

  const valueNode = (
    <Typography
      sx={{ fontSize: valueFontSize ?? ds.text.title, fontWeight: ds.weight.semibold, color: t.value, lineHeight: 1.2, whiteSpace: 'nowrap' }}
    >
      {value}
    </Typography>
  );

  return (
    <Box
      sx={{
        padding: `${ds.space[2]} ${ds.space[3]}`,
        borderRadius: ds.radius.sm,
        border: `1px solid ${t.border}`,
        backgroundColor: t.background,
        minWidth: 0,
        ...(clickable
          ? {
              cursor: 'pointer',
              transition: 'border-color 120ms ease',
              '&:hover': { borderColor: ds.gray[300] },
              '&:focus-visible': { outline: `2px solid ${ds.blue[300]}`, outlineOffset: '1px' },
            }
          : {}),
      }}
      {...(clickable
        ? {
            role: 'button',
            tabIndex: 0,
            onClick,
            onKeyDown: (e: React.KeyboardEvent) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                onClick?.();
              }
            },
          }
        : {})}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: ds.space[1] }}>
        <Typography sx={{ fontSize: '10px', fontWeight: ds.weight.semibold, color: ds.gray[700], letterSpacing: '0.02em', lineHeight: 1.3 }}>
          {label}
        </Typography>
        {sample && (
          <Label size='sm' tone='neutral'>
            Sample
          </Label>
        )}
      </Box>
      <Box sx={{ display: 'flex', alignItems: 'baseline', gap: ds.space[1], mt: '2px', minWidth: 0 }}>
        {tooltip ? <Tooltip title={tooltip}>{valueNode}</Tooltip> : valueNode}
        {secondary && (
          <Typography
            sx={{ fontSize: ds.text.caption, color: ds.gray[600], whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', minWidth: 0 }}
          >
            {secondary}
          </Typography>
        )}
      </Box>
    </Box>
  );
};

// Every column stacks its tiles in a single vertical list — no 2-up grid.
// 10px row gap matches TileGrid's row gap on the Troubleshooting briefing widget.
const TileStack = ({ children }: { children: ReactNode }) => <Box sx={{ display: 'flex', flexDirection: 'column', gap: '10px' }}>{children}</Box>;

// Ordered as a traffic light where the column renders them: critical (red) at
// the top, warning (amber) next, success (green) last.
type CalloutTone = 'critical' | 'warning' | 'success' | 'info';

const CALLOUT_TONE: Record<CalloutTone, { background: string; accent: string; kind: string }> = {
  critical: { background: ds.red[100], accent: ds.red[400], kind: ds.red[700] },
  warning: { background: ds.amber[100], accent: ds.amber[400], kind: ds.amber[700] },
  success: { background: ds.green[100], accent: ds.green[400], kind: ds.green[700] },
  info: { background: ds.blue[100], accent: ds.blue[400], kind: ds.blue[700] },
};

const Callout = ({
  tone,
  kind,
  title,
  detail,
  onClick,
}: {
  tone: CalloutTone;
  kind: string;
  title: string;
  detail: string;
  onClick?: () => void;
}) => {
  const t = CALLOUT_TONE[tone];
  return (
    <Box
      component={onClick ? 'button' : 'div'}
      type={onClick ? 'button' : undefined}
      onClick={onClick}
      sx={{
        display: 'block',
        width: '100%',
        textAlign: 'left',
        font: 'inherit',
        border: 'none',
        padding: ds.space[2],
        borderRadius: ds.radius.sm,
        borderLeft: `3px solid ${t.accent}`,
        backgroundColor: t.background,
        ...(onClick
          ? {
              cursor: 'pointer',
              '&:hover': { filter: 'brightness(0.97)' },
              '&:focus-visible': { outline: `2px solid ${ds.blue[500]}`, outlineOffset: '1px' },
            }
          : {}),
      }}
    >
      <Typography
        sx={{ fontSize: '9px', fontWeight: ds.weight.semibold, color: t.kind, letterSpacing: '0.06em', textTransform: 'uppercase', lineHeight: 1.6 }}
      >
        {kind}
      </Typography>
      <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.semibold, color: ds.brand[600], lineHeight: 1.25 }}>{title}</Typography>
      <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[600], lineHeight: 1.35 }}>{detail}</Typography>
    </Box>
  );
};

interface BreakdownRow {
  category: MainCategory;
  label: string;
  dollars: number;
  count: number;
}

const BreakdownTable = ({
  rows,
  total,
  totalCount,
  symbol,
  onSelectCategory,
  onSelectAll,
}: {
  rows: BreakdownRow[];
  total: number;
  totalCount: number;
  symbol: string;
  onSelectCategory: (category: MainCategory) => void;
  onSelectAll: () => void;
}) => (
  <Box sx={{ border: `1px solid ${ds.gray[100]}`, borderRadius: ds.radius.sm, overflow: 'hidden' }}>
    {rows.map((row, index) => (
      <Box
        key={row.category}
        component='button'
        type='button'
        onClick={() => onSelectCategory(row.category)}
        aria-label={`Show ${row.label} findings`}
        sx={{
          appearance: 'none',
          font: 'inherit',
          textAlign: 'left',
          width: '100%',
          cursor: 'pointer',
          border: 'none',
          display: 'grid',
          gridTemplateColumns: '1fr auto 44px',
          gap: ds.space[2],
          alignItems: 'center',
          padding: '8px 10px',
          borderBottom: `1px solid ${ds.gray[100]}`,
          backgroundColor: index % 2 === 1 ? ds.background[200] : 'transparent',
          '&:hover': { backgroundColor: ds.gray[100] },
          '&:focus-visible': { outline: `2px solid ${ds.blue[500]}`, outlineOffset: '-2px' },
        }}
      >
        <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[700] }}>{row.label}</Typography>
        <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.semibold, color: ds.brand[600], fontVariantNumeric: 'tabular-nums' }}>
          {row.dollars > 0 ? (
            formatDollars(row.dollars, symbol)
          ) : (
            <Box component='span' sx={{ color: ds.gray[300] }}>
              -
            </Box>
          )}
        </Typography>
        <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[600], textAlign: 'right', fontVariantNumeric: 'tabular-nums' }}>
          {row.count}
        </Typography>
      </Box>
    ))}
    <Box
      component='button'
      type='button'
      onClick={onSelectAll}
      aria-label='Show all findings'
      sx={{
        appearance: 'none',
        font: 'inherit',
        textAlign: 'left',
        width: '100%',
        cursor: 'pointer',
        border: 'none',
        display: 'grid',
        gridTemplateColumns: '1fr auto 44px',
        gap: ds.space[2],
        alignItems: 'center',
        padding: '5px 10px',
        backgroundColor: ds.brand[100],
        '&:hover': { filter: 'brightness(0.97)' },
        '&:focus-visible': { outline: `2px solid ${ds.blue[500]}`, outlineOffset: '-2px' },
      }}
    >
      <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.semibold, color: ds.brand[600] }}>Total at stake</Typography>
      <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.semibold, color: ds.brand[600], fontVariantNumeric: 'tabular-nums' }}>
        {formatDollars(total, symbol)}
      </Typography>
      <Typography
        sx={{ fontSize: ds.text.caption, color: ds.gray[700], fontWeight: ds.weight.medium, textAlign: 'right', fontVariantNumeric: 'tabular-nums' }}
      >
        {totalCount}
      </Typography>
    </Box>
  </Box>
);

const ColumnShell = ({ title, meta, sample, children }: { title: string; meta?: string; sample?: boolean; children: ReactNode }) => (
  <Box
    sx={{
      display: 'flex',
      flexDirection: 'column',
      minWidth: 0,
      padding: `${ds.space[3]} ${ds.space[4]}`,
      '&:not(:first-of-type)': { borderLeft: `1px solid ${ds.gray[200]}` },
    }}
  >
    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: ds.space[1], marginBottom: '10px' }}>
      <Typography
        sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.semibold, letterSpacing: '0.04em', textTransform: 'uppercase', color: ds.brand[600] }}
      >
        {title}
      </Typography>
      {sample ? (
        <Label size='sm' tone='neutral'>
          Sample data
        </Label>
      ) : (
        meta && <Typography sx={{ fontSize: '10px', color: ds.gray[400], letterSpacing: '0.03em', whiteSpace: 'nowrap' }}>{meta}</Typography>
      )}
    </Box>
    {children}
  </Box>
);

interface SummaryInsightWidgetProps {
  items: InsightItem[];
  totalSavings: number;
  totalFindingsCount: number;
  savingsSymbol: string;
  savingsCurrency: string;
  /** Total $ across the "Do this first" top-3 queue — moved down from the old headline metrics row. */
  rankedDollars: number;
  /** True tenant-wide (or account-scoped, when filtered) category split — not derived from `items`. */
  categoryBreakdown: Record<MainCategory, { dollars: number; count: number }>;
  /** True cross-account ranking; null when a single account is already selected or none has $ at stake. */
  worstAccount: { accountId: string; name: string; dollars: number } | null;
  /** True count of in-progress resolutions (see `getRecommendationResolutionStatusCounts`) — real, not sample. */
  wipCount: number;
  /** True count of resolutions that completed successfully, same aggregate as `wipCount`. */
  resolvedCount: number;
  /** Full-set blast-radius safety from the safety-band groupings; null when nothing is assessed safe. */
  safeToApply: { count: number; dollars: number } | null;
  /** Deep-links into the Resolutions tab, InProgress-filtered and carrying the current account scope. */
  onViewWorkInProgress: () => void;
  /** Deep-links into the Resolutions tab, Success-filtered and carrying the current account scope. */
  onViewResolved: () => void;
  /** Deep-links into the Recommendations tab, severity=Critical, carrying the current account scope. */
  onViewCritical: () => void;
  /** Narrows the "Top findings" table below to one category and scrolls to it. */
  onFocusCategory: (category: MainCategory) => void;
  /** Re-sorts "Top findings" oldest-first and scrolls to it. */
  onFocusAging: () => void;
  /** Clears the table's own category/provider chips and scrolls to it. */
  onFocusAllFindings: () => void;
  /** Applies the page-wide account filter. */
  onSelectAccount: (accountId: string) => void;
  /** Deep-links into the Recommendations tab, safety=safe, carrying the current account scope. */
  onViewSafeToApply: () => void;
  /** Opens the given recommendation's detail panel — same handler the "Top findings" table row click used to use. */
  onOpenResource: (id: string) => void;
}

// Scrolls to the "Do this first" queue further down this same page — those are
// the same top-3 items this tile totals, so there's nowhere else to navigate to.
const scrollToDoThisFirst = () => {
  document.getElementById('summary-do-this-first')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
};

const SummaryInsightWidget = ({
  items,
  totalSavings,
  totalFindingsCount,
  savingsSymbol,
  savingsCurrency,
  rankedDollars,
  categoryBreakdown,
  worstAccount,
  wipCount,
  resolvedCount,
  safeToApply,
  onViewWorkInProgress,
  onViewResolved,
  onFocusCategory,
  onFocusAging,
  onFocusAllFindings,
  onSelectAccount,
  onViewSafeToApply,
  onViewCritical,
  onOpenResource,
}: SummaryInsightWidgetProps) => {
  const data = useMemo(() => buildInsightWidgetData(items), [items]);

  const breakdownRows: BreakdownRow[] = useMemo(
    () =>
      (Object.keys(CATEGORY_LABELS) as MainCategory[])
        .map((category) => ({ category, label: CATEGORY_LABELS[category], ...categoryBreakdown[category] }))
        .sort((a, b) => b.dollars - a.dollars),
    [categoryBreakdown]
  );
  const breakdownTotalCount = breakdownRows.reduce((sum, row) => sum + row.count, 0);

  return (
    <Card
      id='summary-insight-widget'
      variant='outlined'
      size='sm'
      sx={{
        padding: 0,
        overflow: 'hidden',
        // Butts directly against the Nubi briefing card above (which squares
        // off its own bottom corners) — no gap, no doubled border line.
        borderTop: 'none',
        borderTopLeftRadius: 0,
        borderTopRightRadius: 0,
      }}
    >
      <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '1fr 1fr', lg: '0.9fr 1fr 1.15fr 0.95fr' }, alignItems: 'stretch' }}>
        {/* Lifecycle order, which is also the traffic light: open work first,
            green (done) last. */}
        <ColumnShell title='What changed'>
          <TileStack>
            {/* Not clickable: the count here is by `created_at` (genuinely new),
                but the Recommendations tab only filters by `updated_at`
                ("last seen") — a background cron bumps that on nearly every
                open row every ~6h, so a "last seen 24h" link would land on a
                page showing hundreds of rows for a tile that says "1". No
                accurate destination exists yet. */}
            <Tile
              label='New findings'
              value={data.newFindingsCount.toLocaleString()}
              secondary={data.newFindingsDollars > 0 ? `${formatDollars(data.newFindingsDollars, savingsSymbol)}/mo` : 'in the last 24h'}
              tone='default'
            />
            <Tile label='Work in progress' value={wipCount.toLocaleString()} secondary='resolutions' onClick={onViewWorkInProgress} />
            {/* Count is real (resolution status aggregate). No $ pairing: that table
                exposes `count` only — no savings sum to roll up. */}
            <Tile label='Resolved' value={resolvedCount.toLocaleString()} secondary='resolutions' tone='positive' onClick={onViewResolved} />
          </TileStack>
        </ColumnShell>

        <ColumnShell title='What Nubi found'>
          <TileStack>
            <Tile
              label='Potential savings'
              value={formatWholeCurrency(totalSavings, savingsCurrency)}
              secondary='/mo'
              tone={totalSavings > 0 ? 'positive' : 'default'}
              onClick={onFocusAllFindings}
            />
            <Tile
              label='Critical findings'
              value={data.criticalCount.toLocaleString()}
              secondary={
                data.criticalDollars > 0
                  ? `of ${totalFindingsCount || items.length} · ${formatDollars(data.criticalDollars, savingsSymbol)}/mo at stake`
                  : `of ${totalFindingsCount || items.length}`
              }
              tone={data.criticalCount > 0 ? 'critical' : 'default'}
              onClick={onViewCritical}
            />
            <Tile label='Top 3 impact' value={`${formatDollars(rankedDollars, savingsSymbol)}/mo`} onClick={scrollToDoThisFirst} />
            {worstAccount && (
              <Tile
                label='Worst account'
                value={truncate(worstAccount.name, 20)}
                valueFontSize={ds.text.body}
                tooltip={worstAccount.name}
                secondary={`${formatDollars(worstAccount.dollars, savingsSymbol)}/mo`}
                onClick={() => onSelectAccount(worstAccount.accountId)}
              />
            )}
          </TileStack>
        </ColumnShell>

        <ColumnShell title='Where the opportunity lives' meta={`sums to ${formatDollars(totalSavings, savingsSymbol)}`}>
          <TileStack>
            <BreakdownTable
              rows={breakdownRows}
              total={totalSavings}
              totalCount={breakdownTotalCount}
              symbol={savingsSymbol}
              onSelectCategory={onFocusCategory}
              onSelectAll={onFocusAllFindings}
            />
            {data.biggestOpportunity && (
              <Tile
                label='Biggest single opportunity'
                value={`${formatDollars(data.biggestOpportunity.dollars, savingsSymbol)}/mo`}
                secondary={data.biggestOpportunity.title}
                onClick={() => onOpenResource(data.biggestOpportunity!.id)}
              />
            )}
          </TileStack>
        </ColumnShell>

        <ColumnShell title='Flagged' meta={data.aging.count > 0 ? 'of top findings' : undefined}>
          {/* Traffic light, worst first: red (longest standing) → amber (aging) →
              green (safe to apply). */}
          <TileStack>
            {data.longestStanding && (
              <Callout
                onClick={() => onOpenResource(data.longestStanding!.id)}
                tone='critical'
                kind='Longest standing'
                title={`Open ${data.longestStanding.days} days`}
                detail={`${truncate(resourceAnchorName(data.longestStanding.resource), 30)}${
                  data.longestStanding.dollars > 0 ? ` · ${formatDollars(data.longestStanding.dollars, savingsSymbol)}/mo` : ''
                }`}
              />
            )}
            {data.aging.count > 0 && (
              <Callout
                onClick={onFocusAging}
                tone='warning'
                kind={`Open over ${AGING_DAYS} days`}
                title={`${data.aging.count.toLocaleString()} findings`}
                detail={
                  data.aging.dollars > 0
                    ? `${formatDollars(data.aging.dollars, savingsSymbol)}/mo · ${data.aging.sharePct}% of the savings shown`
                    : 'no monetised savings attached'
                }
              />
            )}
            {safeToApply && safeToApply.count > 0 && (
              <Callout
                onClick={onViewSafeToApply}
                tone='success'
                kind='Safe to apply'
                title={`${safeToApply.count.toLocaleString()} findings`}
                detail={`${
                  safeToApply.dollars > 0 ? `${formatDollars(safeToApply.dollars, savingsSymbol)}/mo · ` : ''
                }no dependents in the blast radius`}
              />
            )}
          </TileStack>
        </ColumnShell>
      </Box>
    </Card>
  );
};

export default SummaryInsightWidget;

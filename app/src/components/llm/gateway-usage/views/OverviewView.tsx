/**
 * OverviewView — the AI Gateway's Overview sub-tab.
 *
 * The original single-page GatewayUsage content: a KPI strip (`@ui/Stat` in
 * `@ui/Card`, incl. avg latency), a requests-over-time bar chart
 * (`@ui/Chart.TimeSeries`) fed from `time_series.by_dimension.overall`, and three
 * compact breakdown tables (`@shared/tables/CustomTable`) — By provider, By model,
 * By user. Purely presentational: it receives the loaded `metrics` / loading /
 * error from the shell and does no fetching of its own.
 */
import * as React from 'react';
import { Box, CircularProgress } from '@mui/material';
import ShowChartIcon from '@mui/icons-material/ShowChart';
import HubOutlinedIcon from '@mui/icons-material/HubOutlined';
import AutoAwesomeOutlinedIcon from '@mui/icons-material/AutoAwesomeOutlined';
import PeopleOutlineIcon from '@mui/icons-material/PeopleOutline';
import dayjs from 'dayjs';
import { Stat } from '@ui/Stat';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import Chart from '@ui/Chart';
import { ToggleGroup } from '@ui/ToggleGroup';
import CustomTable2 from '@shared/tables/CustomTable';
import { fmtCost, fmtTokens, fmtDuration } from '@components/llm/cost-analyser/format';
import type { GatewayFilters } from '../useGatewayData';
import type { GatewayGranularity, GatewayGroupRow, GatewayTimeSeriesRow, GatewayUsageMetrics } from '@api1/gateway-usage';

interface OverviewViewProps {
  metrics: GatewayUsageMetrics | null;
  filters: GatewayFilters;
  loading: boolean;
  error: string | null;
}

function fmtCount(n: number | null | undefined): string {
  if (n === null || n === undefined) return '—';
  return n.toLocaleString('en-US');
}

/** Compact request-count formatter for chart axis ticks + on-bar totals (e.g. 1.2K,
 * 980), matching the uppercase-K style of fmtTokens/fmtCost. TimeSeriesChart's
 * `compactFormat` defaults to compactCurrency ($…), which is wrong for a request
 * count — pass this so the count chart never renders a dollar sign. */
function fmtCountCompact(v: number | null | undefined): string {
  if (v === null || v === undefined) return '—';
  const abs = Math.abs(v);
  if (abs >= 1_000_000) return `${(v / 1_000_000).toFixed(abs >= 10_000_000 ? 0 : 1)}M`;
  if (abs >= 1_000) return `${(v / 1_000).toFixed(abs >= 10_000 ? 0 : 1)}K`;
  return `${Math.round(v)}`;
}

const numCell = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

// ─── Usage-over-time (bar) ──────────────────────────────────────────────────────

/** The metric plotted on the over-time chart. Cost is the default (governance view). */
type Metric = 'cost' | 'requests' | 'tokens';

const METRIC_OPTIONS: { value: Metric; label: string }[] = [
  { value: 'cost', label: 'Cost' },
  { value: 'requests', label: 'Requests' },
  { value: 'tokens', label: 'Tokens' },
];

const METRIC_LABEL: Record<Metric, string> = { cost: 'Cost', requests: 'Requests', tokens: 'Tokens' };

/** Fold the `overall` series into the generic `Chart.TimeSeries` `{labels, series}` shape
 * for the chosen metric. */
function overallToSeries(
  rows: GatewayTimeSeriesRow[],
  granularity: GatewayGranularity,
  metric: Metric
): { labels: string[]; series: { key: string; data: number[] }[] } {
  const sorted = [...rows].sort((a, b) => a.bucket.localeCompare(b.bucket));
  const fmt = granularity === 'hour' ? 'DD MMM HH:00' : 'DD MMM';
  const labels = sorted.map((r) => dayjs(r.bucket).format(fmt));
  const pick =
    metric === 'cost'
      ? (r: GatewayTimeSeriesRow) => r.cost_usd
      : metric === 'tokens'
      ? (r: GatewayTimeSeriesRow) => r.tokens
      : (r: GatewayTimeSeriesRow) => r.requests;
  return { labels, series: [{ key: METRIC_LABEL[metric], data: sorted.map(pick) }] };
}

// ─── Breakdown table ────────────────────────────────────────────────────────────

interface BreakdownTableProps {
  keyHeader: string;
  rows: GatewayGroupRow[];
  id: string;
}

function BreakdownTable({ keyHeader, rows, id }: BreakdownTableProps) {
  const headers = [
    { name: keyHeader, width: '28%' },
    { name: 'Requests', width: '13%' },
    { name: 'Input tokens', width: '14%' },
    { name: 'Output tokens', width: '14%' },
    { name: 'Avg latency', width: '13%' },
    { name: 'Cost', width: '13%' },
  ];
  const tableData = [...rows]
    .sort((a, b) => b.cost_usd - a.cost_usd)
    .map((r) => [
      { component: <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)' }}>{r.key || '—'}</Box> },
      { component: <Box sx={numCell}>{fmtCount(r.requests)}</Box> },
      { component: <Box sx={numCell}>{fmtTokens(r.input_tokens)}</Box> },
      { component: <Box sx={numCell}>{fmtTokens(r.output_tokens)}</Box> },
      { component: <Box sx={numCell}>{fmtDuration((r.avg_latency_seconds ?? 0) * 1000)}</Box> },
      { component: <Box sx={{ ...numCell, fontWeight: 'var(--ds-font-weight-semibold)' }}>{fmtCost(r.cost_usd)}</Box> },
    ]);
  return <CustomTable2 id={id} headers={headers} tableData={tableData} />;
}

// ─── Section header (inline, matches the analyser's card-header rhythm) ─────────

function SectionHeader({ title, icon }: { title: string; icon: React.ReactNode }) {
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', mb: 'var(--ds-space-3)' }}>
      <Box sx={{ display: 'inline-flex', alignItems: 'center', color: 'var(--ds-gray-500)', '& svg': { fontSize: 18 } }}>{icon}</Box>
      <Box sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)', lineHeight: 1.2 }}>
        {title}
      </Box>
    </Box>
  );
}

export function OverviewView({ metrics, filters, loading, error }: OverviewViewProps) {
  const totals = metrics?.totals;
  const overallRows = metrics?.time_series?.by_dimension?.overall ?? [];
  const [metric, setMetric] = React.useState<Metric>('cost');
  const chart = React.useMemo(() => overallToSeries(overallRows, filters.granularity, metric), [overallRows, filters.granularity, metric]);

  if (error) return <Banner tone='critical' title='Could not load gateway usage' message={error} />;

  if (loading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 240 }}>
        <CircularProgress size={28} />
      </Box>
    );
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-5)' }}>
      {/* KPI strip. */}
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-3)' }} id='gateway-kpi-row'>
        <Card sx={{ flex: '1 1 180px', minWidth: 160 }}>
          <Stat label='Total requests' value={fmtCount(totals?.total_requests)} />
        </Card>
        <Card sx={{ flex: '1 1 180px', minWidth: 160 }}>
          <Stat label='Tokens (in / out)' value={`${fmtTokens(totals?.total_input_tokens)} / ${fmtTokens(totals?.total_output_tokens)}`} />
        </Card>
        <Card sx={{ flex: '1 1 180px', minWidth: 160 }}>
          <Stat label='Total cost' value={fmtCost(totals?.total_cost_usd)} />
        </Card>
        <Card sx={{ flex: '1 1 180px', minWidth: 160 }}>
          <Stat label='Cache-hit rate' value={`${(totals?.cache_hit_rate_pct ?? 0).toFixed(1)}%`} />
        </Card>
        <Card sx={{ flex: '1 1 180px', minWidth: 160 }}>
          <Stat label='Avg latency' value={fmtDuration((totals?.avg_latency_seconds ?? 0) * 1000)} />
        </Card>
      </Box>

      {/* Usage over time — Cost (default) | Requests | Tokens. */}
      <Card>
        <Box sx={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
          <SectionHeader title={`${METRIC_LABEL[metric]} over time`} icon={<ShowChartIcon />} />
          <ToggleGroup selection='single' size='sm' ariaLabel='Chart metric' value={metric} onChange={setMetric} options={METRIC_OPTIONS} />
        </Box>
        {/* key={metric} forces a remount when the metric changes, so the chart's
            on-bar-total / axis plugins re-init with the right formatter — otherwise a
            reused instance keeps the previous metric's compactFormat (e.g. the cost $).
            Cost → default compactCurrency ($); requests/tokens → plain counts, no $. */}
        <Chart.TimeSeries
          key={metric}
          {...chart}
          shape='bar'
          format={metric === 'cost' ? fmtCost : fmtCount}
          compactFormat={metric === 'cost' ? undefined : fmtCountCompact}
          integerY={metric !== 'cost'}
          id='gateway-usage-over-time'
        />
      </Card>

      {/* Breakdown tables. */}
      <Card>
        <SectionHeader title='By provider' icon={<HubOutlinedIcon />} />
        <BreakdownTable keyHeader='Provider' rows={metrics?.breakdowns?.provider ?? []} id='gateway-provider-table' />
      </Card>
      <Card>
        <SectionHeader title='By model' icon={<AutoAwesomeOutlinedIcon />} />
        <BreakdownTable keyHeader='Model' rows={metrics?.breakdowns?.model ?? []} id='gateway-model-table' />
      </Card>
      <Card>
        <SectionHeader title='By user' icon={<PeopleOutlineIcon />} />
        <BreakdownTable keyHeader='User' rows={metrics?.breakdowns?.user ?? []} id='gateway-user-table' />
      </Card>
    </Box>
  );
}

export default OverviewView;

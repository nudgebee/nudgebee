/**
 * Screen 3 — Models (spec §7). Per-model economics from `ai_aggregate_usage_metrics`
 * (group_by: model) — real cost / calls / conversations / avg tokens / avg latency.
 *
 * The two over-time charts read the response's `time_series` block (stacked by
 * model). The multi-model filter in the bar re-scopes the real query.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import TimelineOutlinedIcon from '@mui/icons-material/TimelineOutlined';
import ShowChartIcon from '@mui/icons-material/ShowChart';
import CustomTable2 from '@shared/tables/CustomTable';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { CostCallout } from '@ui/CostCallout';
import { EmptyState } from '@ui/EmptyState';
import Chart from '@ui/Chart';
import SectionHeader from '../components/Section';
import { seriesColor } from '../components/palette';
import { fmtCost, fmtDuration, fmtTokens } from '../format';
import { makeSeverity, SeverityCell } from '../components/severity';
import { bucketsToSeries, timeSeriesToChart } from '../adapt';
import type { UsageGroupRow, UsageMetrics } from '@api1/ai-cost';
import type { CostFilters } from '../types';

interface ModelsViewProps {
  metrics: UsageMetrics | null;
  filters: CostFilters;
}

interface ModelRow {
  model: string;
  totalCost: number;
  calls: number;
  conversations: number;
  avgCostPerCall: number;
  avgInputTokens: number;
  avgOutputTokens: number;
  avgLatencyMs: number;
}

const numSx = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

const HEADER_TO_VALUE: Record<string, (m: ModelRow) => number> = {
  'Total cost': (m) => m.totalCost,
  Calls: (m) => m.calls,
  Convs: (m) => m.conversations,
  'Avg / call': (m) => m.avgCostPerCall,
  'Avg latency': (m) => m.avgLatencyMs,
};

function toModelRow(r: UsageGroupRow): ModelRow {
  const calls = r.requests ?? 0;
  return {
    model: r.key || '—',
    totalCost: r.cost_usd ?? 0,
    calls,
    conversations: r.conversations ?? 0,
    avgCostPerCall: calls ? (r.cost_usd ?? 0) / calls : 0,
    avgInputTokens: calls ? (r.input_tokens ?? 0) / calls : 0,
    avgOutputTokens: calls ? (r.output_tokens ?? 0) / calls : 0,
    avgLatencyMs: (r.avg_latency_seconds ?? 0) * 1000,
  };
}

export function ModelsView({ metrics, filters }: ModelsViewProps) {
  const [sort, setSort] = React.useState<{ name: string; order: 'asc' | 'desc' }>({ name: 'Total cost', order: 'desc' });

  const rows = React.useMemo(() => (metrics?.breakdowns?.model ?? []).map(toModelRow), [metrics]);

  const summaries = React.useMemo(() => {
    const valueOf = HEADER_TO_VALUE[sort.name] ?? HEADER_TO_VALUE['Total cost'];
    const sorted = [...rows].sort((a, b) => valueOf(a) - valueOf(b));
    return sort.order === 'desc' ? sorted.reverse() : sorted;
  }, [rows, sort]);

  // Over-time charts, stacked by model, from the real `time_series` block.
  const calls = React.useMemo(
    () => timeSeriesToChart(metrics?.time_series ?? null, 'model', 'calls', filters.startDate, filters.endDate, filters.granularity),
    [metrics, filters.startDate, filters.endDate, filters.granularity]
  );
  const share = React.useMemo(
    () => timeSeriesToChart(metrics?.time_series ?? null, 'model', 'cost', filters.startDate, filters.endDate, filters.granularity),
    [metrics, filters.startDate, filters.endDate, filters.granularity]
  );

  if (!rows.length) {
    return (
      <EmptyState
        size='section'
        illustration='no-results'
        title='No model activity for these filters'
        description='Widen the range or clear the model filter.'
      />
    );
  }

  const headers = [
    { name: 'Model', width: '22%' },
    { name: 'Total cost', width: '13%', sortEnabled: true },
    { name: 'Calls', width: '9%', sortEnabled: true },
    { name: 'Convs', width: '9%', sortEnabled: true },
    { name: 'Avg / call', width: '13%', sortEnabled: true },
    { name: 'Avg tok', width: '16%', secondryText: '(in/out)' },
    { name: 'Avg latency', width: '14%', sortEnabled: true },
  ];

  // Relative outlier highlighting: rank total cost / avg latency across models.
  const costSev = React.useMemo(() => makeSeverity(summaries.map((m) => m.totalCost)), [summaries]);
  const latSev = React.useMemo(() => makeSeverity(summaries.map((m) => m.avgLatencyMs)), [summaries]);

  const tableData = summaries.map((m) => [
    {
      component: (
        <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-medium)' }}>{m.model}</Box>
      ),
    },
    {
      component: (
        <SeverityCell severity={costSev(m.totalCost)} metric='cost'>
          <CostCallout value={m.totalCost} size='sm' tone='neutral' fractionDigits={2} />
        </SeverityCell>
      ),
    },
    { component: <Box sx={numSx}>{m.calls}</Box> },
    { component: <Box sx={numSx}>{m.conversations}</Box> },
    { component: <Box sx={numSx}>{fmtCost(m.avgCostPerCall)}</Box> },
    {
      component: (
        <Box sx={numSx}>
          {fmtTokens(m.avgInputTokens)} / {fmtTokens(m.avgOutputTokens)}
        </Box>
      ),
    },
    {
      component: (
        <SeverityCell severity={latSev(m.avgLatencyMs)} metric='latency'>
          <Box sx={numSx}>{fmtDuration(m.avgLatencyMs)}</Box>
        </SeverityCell>
      ),
    },
  ]);

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-5)' }}>
      <Banner
        tone='info'
        title='Multi-model filter'
        message='Select one or more models in the filter bar to re-scope the analyser to runs that used them.'
      />

      <Card>
        <CustomTable2
          id='models-table'
          headers={headers}
          tableData={tableData}
          sort={sort}
          onSortChange={(s: { name: string; order: 'asc' | 'desc' }) => setSort(s)}
        />
      </Card>

      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-5)' }}>
        <Card sx={{ flex: '1 1 360px', minWidth: 0 }}>
          <SectionHeader
            title='Calls per model over time'
            icon={<TimelineOutlinedIcon />}
            subtitle='Number of model calls in each period, by model'
          />
          <Chart.TimeSeries
            {...bucketsToSeries(calls.buckets, calls.keys)}
            shape='line'
            integerY
            format={(v) => String(Math.round(v))}
            colorFor={seriesColor}
            id='calls-per-model'
          />
        </Card>

        <Card sx={{ flex: '1 1 360px', minWidth: 0 }}>
          <SectionHeader title='Model cost-share over time' icon={<ShowChartIcon />} />
          <Chart.TimeSeries
            {...bucketsToSeries(share.buckets, share.keys)}
            shape='bar'
            format={fmtCost}
            colorFor={seriesColor}
            id='model-share-over-time'
          />
        </Card>
      </Box>
    </Box>
  );
}

export default ModelsView;

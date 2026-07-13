/** Overview sub-tab: KPI strip + accepted-vs-refined trend chart. Purely presentational. */
import * as React from 'react';
import { Box, CircularProgress } from '@mui/material';
import ShowChartIcon from '@mui/icons-material/ShowChart';
import dayjs from 'dayjs';
import { Stat } from '@ui/Stat';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import Chart from '@ui/Chart';
import { fmtPct } from '@components/llm/cost-analyser/format';
import type { CritiqueSummary, CritiqueTrend } from '@api1/critiques';

interface OverviewViewProps {
  summary: CritiqueSummary | null;
  trend: CritiqueTrend | null;
  loading: boolean;
  error: string | null;
}

function fmtCount(n: number | null | undefined): string {
  if (n === null || n === undefined) return '—';
  return n.toLocaleString('en-US');
}

function bucketFormat(granularity: string): string {
  if (granularity === 'month') return 'MMM YYYY';
  if (granularity === 'week') return '[W]w YYYY';
  return 'DD MMM';
}

/** Accepted-vs-refined series — additive halves of `judged`, unlike refined which is a subset. */
function trendToSeries(trend: CritiqueTrend | null): { labels: string[]; series: { key: string; data: number[] }[] } {
  const points = trend?.points ?? [];
  const fmt = bucketFormat(trend?.granularity ?? 'day');
  const labels = points.map((p) => dayjs(p.bucket).format(fmt));
  const accepted = points.map((p) => Math.max(0, p.judged - p.refined));
  const refined = points.map((p) => p.refined);
  return {
    labels,
    series: [
      { key: 'Accepted', data: accepted },
      { key: 'Refined', data: refined },
    ],
  };
}

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

export function OverviewView({ summary, trend, loading, error }: OverviewViewProps) {
  const totals = summary?.totals;
  const agentCount = summary?.by_agent?.length ?? 0;
  const chart = React.useMemo(() => trendToSeries(trend), [trend]);

  if (error) return <Banner tone='critical' title='Could not load critique data' message={error} />;

  if (loading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 240 }}>
        <CircularProgress size={28} />
      </Box>
    );
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-5)' }}>
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-3)' }} id='critique-kpi-row'>
        <Card sx={{ flex: '1 1 180px', minWidth: 160 }}>
          <Stat label='Judged answers' value={fmtCount(totals?.judged)} />
        </Card>
        <Card sx={{ flex: '1 1 180px', minWidth: 160 }}>
          <Stat label='Sent back for refinement' value={fmtCount(totals?.refined)} />
        </Card>
        <Card sx={{ flex: '1 1 180px', minWidth: 160 }}>
          <Stat label='Refine rate' value={fmtPct((totals?.refine_pct ?? 0) / 100, 1)} />
        </Card>
        <Card sx={{ flex: '1 1 180px', minWidth: 160 }}>
          <Stat label='Agents tracked' value={fmtCount(agentCount)} />
        </Card>
      </Box>

      <Card>
        <SectionHeader title='Accepted vs. refined over time' icon={<ShowChartIcon />} />
        <Chart.TimeSeries {...chart} shape='bar' integerY format={(v) => fmtCount(v)} compactFormat={(v) => fmtCount(v)} id='critique-trend-chart' />
      </Card>
    </Box>
  );
}

export default OverviewView;

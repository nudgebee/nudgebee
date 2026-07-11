/**
 * Screen 1 — Cost Overview (spec §4). How much are we spending, is it going up,
 * and where is it going.
 *
 * KPI strip + cost-by-model + cost-by-source + the cost-over-time trend all come
 * from `ai_aggregate_usage_metrics` (real); the trend reads the response's
 * `time_series` block.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import LeaderboardOutlinedIcon from '@mui/icons-material/LeaderboardOutlined';
import KpiRow from '../components/KpiRow';
import CostOverTime from '../components/CostOverTime';
import BreakdownWidgets from '../components/BreakdownWidgets';
import ConversationsTable from '../components/ConversationsTable';
import SectionHeader from '../components/Section';
import { Card } from '@ui/Card';
import { groupRowsToModelSlices, groupRowsToSlices, totalsToKpi } from '../adapt';
import type { UsageMetrics, UsageTotals } from '@api1/ai-cost';
import type { CostFilters, Run } from '../types';

interface OverviewViewProps {
  metrics: UsageMetrics | null;
  prevTotals: UsageTotals | null;
  /** Conversation rows (cost-desc) for the "top 10 most expensive" table. */
  runs: Run[];
  filters: CostFilters;
  onSelectRun: (sessionId: string) => void;
  /** account_id → display name, for the per-row account label (matches the main list). */
  accountNameById?: Record<string, string>;
  /** Open the detail modal on the Optimize tab and analyze. */
  onAnalyse?: (sessionId: string) => void;
  /** Click a model in the breakdown to filter the report to it. */
  onSelectModel?: (model: string) => void;
  /** Click a source in the breakdown to filter the report to it. */
  onSelectSource?: (source: string) => void;
}

export function OverviewView({
  metrics,
  prevTotals,
  runs,
  filters,
  onSelectRun,
  accountNameById,
  onAnalyse,
  onSelectModel,
  onSelectSource,
}: OverviewViewProps) {
  const current = totalsToKpi(metrics?.totals);
  const previous = totalsToKpi(prevTotals ?? undefined);

  const byModel = groupRowsToModelSlices(metrics?.breakdowns?.model, 7);
  const bySource = groupRowsToSlices(metrics?.breakdowns?.source);

  const topRuns = runs.slice(0, 10);

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-5)' }}>
      <KpiRow current={current} previous={previous} storageCost={metrics?.storage?.total_usd ?? 0} />

      <CostOverTime
        timeSeries={metrics?.time_series ?? null}
        granularity={filters.granularity}
        startDate={filters.startDate}
        endDate={filters.endDate}
      />

      <BreakdownWidgets byModel={byModel} bySource={bySource} onSelectModel={onSelectModel} onSelectSource={onSelectSource} />

      <Card>
        <SectionHeader title='Top 10 most expensive conversations' icon={<LeaderboardOutlinedIcon />} />
        <ConversationsTable
          runs={topRuns}
          accountNameById={accountNameById}
          onSelectRun={onSelectRun}
          onAnalyse={onAnalyse}
          defaultSort={{ key: 'cost', order: 'desc' }}
          id='top-conversations-table'
        />
      </Card>
    </Box>
  );
}

export default OverviewView;

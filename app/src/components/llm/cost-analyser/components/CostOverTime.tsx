/**
 * CostOverTime — the primary time-series card (spec §4b) with a Chart ↔ Table
 * toggle so the same data is viewable as actual numbers, plus a stack-by switch.
 *
 * Reads the backend over-time series (`ai_aggregate_usage_metrics` time_series),
 * which carries the series stacked by every dimension at once — so switching the
 * stack-by toggle re-pivots client-side with no refetch.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import ShowChartIcon from '@mui/icons-material/ShowChart';
import BarChartIcon from '@mui/icons-material/BarChart';
import StackedLineChartIcon from '@mui/icons-material/StackedLineChart';
import TableRowsOutlinedIcon from '@mui/icons-material/TableRowsOutlined';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Card } from '@ui/Card';
import Chart from '@ui/Chart';
import CustomTable2 from '@shared/tables/CustomTable';
import SectionHeader from './Section';
import { seriesColor } from './palette';
import { fmtCost } from '../format';
import { bucketsToSeries, timeSeriesToChart } from '../adapt';
import type { UsageStackDimension, UsageTimeSeries } from '@api1/ai-cost';
import type { Granularity, TimeBucket } from '../types';

interface CostOverTimeProps {
  timeSeries: UsageTimeSeries | null;
  granularity: Granularity;
  startDate: string;
  endDate: string;
}

const STACK_OPTIONS: { value: UsageStackDimension; label: string }[] = [
  { value: 'model', label: 'Model' },
  { value: 'source', label: 'Source' },
  { value: 'agent', label: 'Agent' },
];

const numSx = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

/** Actual-numbers view of the same data, via the canonical table. */
function NumberTable({ buckets, keys }: { buckets: TimeBucket[]; keys: string[] }) {
  const headers = [{ name: 'Period', width: '16%' }, ...keys.map((k) => ({ name: k })), { name: 'Total' }];
  const tableData = buckets.map((b) => [
    { component: <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)' }}>{b.label}</Box> },
    ...keys.map((k) => ({
      component: b.series[k] ? (
        <Box sx={numSx}>{fmtCost(b.series[k])}</Box>
      ) : (
        <Box component='span' sx={{ color: 'var(--ds-gray-300)' }}>
          —
        </Box>
      ),
    })),
    { component: <Box sx={{ ...numSx, fontWeight: 'var(--ds-font-weight-semibold)' }}>{fmtCost(b.total)}</Box> },
  ]);
  return <CustomTable2 headers={headers} tableData={tableData} />;
}

export function CostOverTime({ timeSeries, granularity, startDate, endDate }: CostOverTimeProps) {
  const [stackBy, setStackBy] = React.useState<UsageStackDimension>('model');
  const [view, setView] = React.useState<'chart' | 'table'>('chart');
  const [shape, setShape] = React.useState<'bar' | 'area'>('bar');
  const { buckets, keys } = React.useMemo(
    () => timeSeriesToChart(timeSeries, stackBy, 'cost', startDate, endDate, granularity),
    [timeSeries, stackBy, startDate, endDate, granularity]
  );

  return (
    <Card>
      <SectionHeader
        title='Cost over time'
        icon={<ShowChartIcon />}
        right={
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', alignItems: 'center' }}>
            <ToggleGroup
              selection='single'
              size='sm'
              ariaLabel='Stack by'
              value={stackBy}
              onChange={(v) => setStackBy(v as UsageStackDimension)}
              options={STACK_OPTIONS}
            />
            {view === 'chart' && (
              <ToggleGroup
                selection='single'
                size='sm'
                ariaLabel='Chart shape'
                value={shape}
                onChange={(v) => setShape(v as 'bar' | 'area')}
                options={[
                  { value: 'bar', label: 'Bars', icon: <BarChartIcon sx={{ fontSize: 14 }} />, ariaLabel: 'Bars' },
                  { value: 'area', label: 'Area', icon: <StackedLineChartIcon sx={{ fontSize: 14 }} />, ariaLabel: 'Area' },
                ]}
              />
            )}
            <ToggleGroup
              selection='single'
              size='sm'
              ariaLabel='View'
              value={view}
              onChange={(v) => setView(v as 'chart' | 'table')}
              options={[
                { value: 'chart', label: 'Chart', icon: <ShowChartIcon sx={{ fontSize: 14 }} />, ariaLabel: 'Chart' },
                { value: 'table', label: 'Table', icon: <TableRowsOutlinedIcon sx={{ fontSize: 14 }} />, ariaLabel: 'Table' },
              ]}
            />
          </Box>
        }
      />
      {view === 'chart' ? (
        <Chart.TimeSeries
          {...bucketsToSeries(buckets, keys)}
          shape={shape === 'area' ? 'area' : 'bar'}
          format={fmtCost}
          colorFor={seriesColor}
          id='cost-over-time'
        />
      ) : (
        <NumberTable buckets={buckets} keys={keys} />
      )}
    </Card>
  );
}

export default CostOverTime;

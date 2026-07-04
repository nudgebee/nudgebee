/**
 * BarSeries — stacked bar of cost over time (spec §4b). react-chartjs-2 directly
 * so we control the pastel palette and the white tabular tooltip (ds/Chart can't
 * customise either). Replaces the old StackedTimeSeries.
 *
 * Reads at a glance without hovering: the $ total sits atop each bar, a dashed
 * average line marks the baseline, and the legend carries each series' total.
 * Clicking a bar drills into that period (`onSelectBucket`).
 */
import * as React from 'react';
import { Box } from '@mui/material';
import { Bar } from 'react-chartjs-2';
import { averageLine, baseChartOptions, compactCurrency, stackedTotalLabels } from './chartKit';
import { seriesColor, withAlpha } from './palette';
import { fmtCost } from '../format';
import type { TimeBucket } from '../types';

/** Top→bottom gradient per bar (full colour → lighter) for depth. */
function barGradient(color: string) {
  return (context: { chart: { ctx: CanvasRenderingContext2D; chartArea?: { top: number; bottom: number } } }) => {
    const { ctx, chartArea } = context.chart;
    if (!chartArea) return color;
    const g = ctx.createLinearGradient(0, chartArea.top, 0, chartArea.bottom);
    g.addColorStop(0, color);
    g.addColorStop(1, withAlpha(color, 0.6));
    return g;
  };
}

interface BarSeriesProps {
  buckets: TimeBucket[];
  keys: string[];
  id?: string;
  /** Click a bar to drill into that period; receives the bucket label. */
  onSelectBucket?: (label: string) => void;
}

export function BarSeries({ buckets, keys, id, onSelectBucket }: BarSeriesProps) {
  const labels = buckets.map((b) => b.label);
  const datasets = keys.map((k, i) => {
    const color = seriesColor(k, i);
    return {
      label: k,
      data: buckets.map((b) => Number((b.series[k] ?? 0).toFixed(2))),
      backgroundColor: barGradient(color),
      borderColor: withAlpha(color, 0.9),
      borderWidth: { top: 1, right: 0, bottom: 0, left: 0 },
      borderRadius: 3,
      maxBarThickness: 40,
    };
  });

  // Per-series total across the window — surfaced in the legend so each series
  // carries its own number, not just a name.
  const seriesTotals = React.useMemo(() => {
    const totals: Record<string, number> = {};
    keys.forEach((k) => (totals[k] = buckets.reduce((s, b) => s + (b.series[k] ?? 0), 0)));
    return totals;
  }, [buckets, keys]);

  const options = {
    ...baseChartOptions((raw) => fmtCost(Number(raw)), { stacked: true }),
    onClick: (_e: unknown, els: { index: number }[]) => {
      if (onSelectBucket && els?.length) onSelectBucket(labels[els[0].index]);
    },
  };

  return (
    <Box id={id} sx={{ minWidth: 0 }}>
      <Box sx={{ position: 'relative', height: 264, width: '100%', minWidth: 0, overflow: 'hidden' }}>
        <Bar data={{ labels, datasets }} options={options as any} plugins={[stackedTotalLabels(compactCurrency), averageLine(compactCurrency)]} />
      </Box>
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-2) var(--ds-space-4)', mt: 'var(--ds-space-2)' }}>
        {keys.map((k, i) => (
          <Box
            key={k}
            sx={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 'var(--ds-space-1)',
              fontSize: 'var(--ds-text-caption)',
              color: 'var(--ds-gray-600)',
            }}
          >
            <Box sx={{ width: 9, height: 9, borderRadius: 2, backgroundColor: seriesColor(k, i), flexShrink: 0 }} />
            <Box component='span'>{k}</Box>
            <Box component='span' sx={{ color: 'var(--ds-gray-400)', fontVariantNumeric: 'tabular-nums' }}>
              {fmtCost(seriesTotals[k] ?? 0)}
            </Box>
          </Box>
        ))}
      </Box>
    </Box>
  );
}

export default BarSeries;

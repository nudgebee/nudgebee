/**
 * LineSeries — multi-line / area chart (react-chartjs-2) with the pastel palette
 * and the shared white tabular tooltip. Used for "calls per model over time" and
 * the area variant of cost-over-time.
 *
 * End-of-line value labels and per-series legend totals make it readable without
 * hovering; the y-axis is currency (or integer) via the shared chartKit options.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import { Line } from 'react-chartjs-2';
import { baseChartOptions } from './chartKit';
import { seriesColor, withAlpha } from './palette';
import { fmtCost } from '../format';
import type { TimeBucket } from '../types';

/** Soft top→bottom area fill under a line; falls back to a flat tint pre-layout. */
function areaFill(color: string, strong: boolean) {
  const topA = strong ? 0.42 : 0.3;
  return (context: { chart: { ctx: CanvasRenderingContext2D; chartArea?: { top: number; bottom: number } } }) => {
    const { ctx, chartArea } = context.chart;
    if (!chartArea) return withAlpha(color, 0.12);
    const g = ctx.createLinearGradient(0, chartArea.top, 0, chartArea.bottom);
    g.addColorStop(0, withAlpha(color, topA));
    g.addColorStop(1, withAlpha(color, 0.02));
    return g;
  };
}

interface LineSeriesProps {
  buckets: TimeBucket[];
  keys: string[];
  id?: string;
  /** Integer series (e.g. call counts) — formats values as plain integers. */
  integer?: boolean;
  /** Stacked area mode (composition over time) — fills every series and stacks them. */
  area?: boolean;
}

export function LineSeries({ buckets, keys, id, integer, area }: LineSeriesProps) {
  const labels = buckets.map((b) => b.label);
  // A single line with an area fill reads richer than many thin strokes; with
  // multiple series we keep fills subtle (gradient) so they layer without mud.
  // In explicit `area` mode every series fills and stacks.
  const fill = area || keys.length <= 3;
  const datasets = keys.map((k, i) => {
    const color = seriesColor(k, i);
    return {
      label: k,
      data: buckets.map((b) => b.series[k] ?? 0),
      borderColor: color,
      backgroundColor: fill ? areaFill(color, !!area) : color,
      fill: area ? (i === 0 ? 'origin' : '-1') : fill ? 'origin' : false,
      borderWidth: area ? 1.5 : 2.5,
      pointRadius: 0,
      pointHoverRadius: 4,
      pointBackgroundColor: color,
      tension: 0.35,
    };
  });
  const format = (raw: unknown) => (integer ? String(Math.round(Number(raw))) : fmtCost(Number(raw)));
  const options = baseChartOptions(format, { integerY: !!integer, stacked: !!area });

  // End-of-line value labels: the most recent point of each series, drawn just
  // past the last datum so the eye lands on "where each series ended up".
  const endLabels = React.useMemo(
    () => ({
      id: 'lineEndLabels',
      afterDatasetsDraw(chart: any) {
        const { ctx } = chart;
        ctx.save();
        ctx.font = '600 10px sans-serif';
        ctx.textAlign = 'left';
        ctx.textBaseline = 'middle';
        chart.data.datasets.forEach((ds: any, di: number) => {
          if (!chart.isDatasetVisible(di)) return;
          const meta = chart.getDatasetMeta(di);
          const last = meta?.data?.[meta.data.length - 1];
          const val = ds.data?.[ds.data.length - 1];
          if (!last || val == null) return;
          ctx.fillStyle = ds.borderColor || 'rgba(0,0,0,0.6)';
          ctx.fillText(format(val), Math.min(last.x + 6, chart.chartArea.right - 2), last.y);
        });
        ctx.restore();
      },
    }),
    [integer]
  );

  // Per-series total across the window — shown in the legend.
  const seriesTotals = React.useMemo(() => {
    const totals: Record<string, number> = {};
    keys.forEach((k) => (totals[k] = buckets.reduce((s, b) => s + (b.series[k] ?? 0), 0)));
    return totals;
  }, [buckets, keys]);

  return (
    <Box id={id} sx={{ minWidth: 0 }}>
      <Box sx={{ position: 'relative', height: 264, width: '100%', minWidth: 0, overflow: 'hidden' }}>
        <Line data={{ labels, datasets }} options={options as any} plugins={[endLabels]} />
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
            <Box sx={{ width: 9, height: 9, borderRadius: '50%', backgroundColor: seriesColor(k, i), flexShrink: 0 }} />
            <Box component='span'>{k}</Box>
            <Box component='span' sx={{ color: 'var(--ds-gray-400)', fontVariantNumeric: 'tabular-nums' }}>
              {integer ? Math.round(seriesTotals[k] ?? 0) : fmtCost(seriesTotals[k] ?? 0)}
            </Box>
          </Box>
        ))}
      </Box>
    </Box>
  );
}

export default LineSeries;

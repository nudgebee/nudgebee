/**
 * TimeSeriesChart — a generic multi-series time chart (stacked bar / stacked
 * area / multi-line) with a per-series totals legend, drawn through the shared
 * `BarChart` / `SeriesChart` primitives.
 *
 * It owns the cross-cutting "series chart" composition that features used to
 * re-roll: building gradient datasets from `{ labels, series }`, the white
 * tabular tooltip + base options, the on-bar stacked totals + average line (bar)
 * or end-of-line value labels (line/area), and the legend with per-series totals.
 *
 * Everything feature-specific is injected — `format`/`compactFormat` for value
 * display and `colorFor` for the palette — so it stays decoupled from any one
 * domain's data types or formatters.
 *
 * Don't: pass already-built Chart.js datasets here — use `BarChart`/`SeriesChart`
 * (`Chart.Bar` / `Chart.Series`) directly for that. This component is for the
 * `{ labels, series }` shape plus the totals legend.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import BarChart from './BarChart';
import SeriesChart from './SeriesChart';
import { averageLine, baseChartOptions, stackedTotalLabels, withAlpha, SERIES_PALETTE, type YTickFormat } from './chartPlugins';
import { compactCurrency } from './externalTooltip';

export interface SeriesDatum {
  key: string;
  data: number[];
}

export type TimeSeriesShape = 'bar' | 'area' | 'line';

export interface TimeSeriesChartProps {
  labels: string[];
  series: SeriesDatum[];
  /** Bar (stacked, default) · area (stacked fills) · line (multi-line). */
  shape?: TimeSeriesShape;
  /** Full value formatter — tooltip, legend totals, line end-labels. Default: rounded integer. */
  format?: (value: number) => string;
  /** Compact formatter — y-axis ticks, on-bar totals, average line. Default: compactCurrency. */
  compactFormat?: (value: number) => string;
  /** Integer y-axis (e.g. call counts). */
  integerY?: boolean;
  /** Colour per series key. Default: built-in categorical ramp by index. */
  colorFor?: (key: string, index: number) => string;
  /** Render the per-series totals legend below the chart (default true). */
  showLegend?: boolean;
  height?: number;
  id?: string;
  /** Click a column/point to drill; receives its label + index. */
  onSelectPoint?: (label: string, index: number) => void;
}

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

export function TimeSeriesChart({
  labels,
  series,
  shape = 'bar',
  format = (v) => String(Math.round(v)),
  compactFormat = compactCurrency,
  integerY = false,
  colorFor = (_k, i) => SERIES_PALETTE[i % SERIES_PALETTE.length],
  showLegend = true,
  height = 264,
  id,
  onSelectPoint,
}: TimeSeriesChartProps) {
  const isBar = shape === 'bar';
  const isArea = shape === 'area';
  const stacked = isBar || isArea;

  const datasets = series.map(({ key, data }, i) => {
    const color = colorFor(key, i);
    if (isBar) {
      return {
        label: key,
        data: data.map((v) => Number((v ?? 0).toFixed(2))),
        backgroundColor: barGradient(color),
        borderColor: withAlpha(color, 0.9),
        borderWidth: { top: 1, right: 0, bottom: 0, left: 0 },
        borderRadius: 3,
        maxBarThickness: 40,
      };
    }
    // line / area: a single line reads richer with a fill; many thin strokes
    // keep subtle gradient fills so they layer without mud. `area` forces fills.
    const fill = isArea || series.length <= 3;
    return {
      label: key,
      data,
      borderColor: color,
      backgroundColor: fill ? areaFill(color, isArea) : color,
      fill: isArea ? (i === 0 ? 'origin' : '-1') : fill ? 'origin' : false,
      borderWidth: isArea ? 1.5 : 2.5,
      pointRadius: 0,
      pointHoverRadius: 4,
      pointBackgroundColor: color,
      tension: 0.35,
    };
  });

  const seriesTotals = React.useMemo(() => {
    const totals: Record<string, number> = {};
    series.forEach(({ key, data }) => (totals[key] = data.reduce((s, v) => s + (v ?? 0), 0)));
    return totals;
  }, [series]);

  const yTickFormat: YTickFormat = integerY ? 'int' : compactFormat;
  const options = {
    ...baseChartOptions((raw) => format(Number(raw)), { stacked, integerY, yTickFormat }),
    ...(onSelectPoint
      ? { onClick: (_e: unknown, els: { index: number }[]) => els?.length && onSelectPoint(labels[els[0].index], els[0].index) }
      : {}),
  };

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
          ctx.fillText(format(Number(val)), Math.min(last.x + 6, chart.chartArea.right - 2), last.y);
        });
        ctx.restore();
      },
    }),
    // format is stable per render; intentionally recompute when it changes
    [format]
  );

  const customPlugins = isBar ? [stackedTotalLabels(compactFormat), averageLine(compactFormat)] : [endLabels];
  const swatchRadius = isBar ? 2 : '50%';

  return (
    <Box id={id} sx={{ minWidth: 0 }}>
      <Box sx={{ position: 'relative', height, width: '100%', minWidth: 0, overflow: 'hidden' }}>
        {isBar ? (
          <BarChart labels={labels} dataset={datasets} options={options} customPlugins={customPlugins} maxHeight={height} />
        ) : (
          <SeriesChart labels={labels} dataset={datasets} options={options} customPlugins={customPlugins} maxHeight={height} />
        )}
      </Box>
      {showLegend && (
        <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-2) var(--ds-space-4)', mt: 'var(--ds-space-2)' }}>
          {series.map(({ key }, i) => (
            <Box
              key={key}
              sx={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: 'var(--ds-space-1)',
                fontSize: 'var(--ds-text-caption)',
                color: 'var(--ds-gray-600)',
              }}
            >
              <Box sx={{ width: 9, height: 9, borderRadius: swatchRadius, backgroundColor: colorFor(key, i), flexShrink: 0 }} />
              <Box component='span'>{key}</Box>
              <Box component='span' sx={{ color: 'var(--ds-gray-400)', fontVariantNumeric: 'tabular-nums' }}>
                {format(seriesTotals[key] ?? 0)}
              </Box>
            </Box>
          ))}
        </Box>
      )}
    </Box>
  );
}

export default TimeSeriesChart;

/**
 * chartPlugins — shared Chart.js building blocks for the DS chart wrappers:
 *   - `baseChartOptions` — line/bar base options (currency/compact y-axis, muted
 *     grid, no built-in legend box, index-hover, the shared external tooltip);
 *   - `stackedTotalLabels` — draws the per-column stacked total above each bar;
 *   - `averageLine` — a dashed mean reference line.
 *
 * These are passed in explicitly (via `options` / `customPlugins`) — they don't
 * change any chart that doesn't opt in. The white tabular tooltip lives in
 * `externalTooltip.ts`; this module composes it into the base options.
 */
import { resolveColor } from '@utils/colors';
import { compactCurrency, makeExternalTooltip, type TooltipValueFormatter } from './externalTooltip';

/** Resolve a dataset colour value, leaving gradient/callback functions untouched. */
export function resolveDatasetColor(value: any): any {
  if (typeof value === 'function') return value; // chart.js gradient/scriptable colour
  return Array.isArray(value) ? value.map(resolveColor) : resolveColor(value);
}

/** `#rrggbb` → `rgba(r,g,b,alpha)`. Non-hex input is returned unchanged (no fade). */
export function withAlpha(hex: string, alpha: number): string {
  if (!/^#[0-9a-fA-F]{6}$/.test(hex)) return hex;
  const h = hex.replace('#', '');
  const r = parseInt(h.slice(0, 2), 16);
  const g = parseInt(h.slice(2, 4), 16);
  const b = parseInt(h.slice(4, 6), 16);
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

/** Default categorical ramp for series colours when the caller injects none. */
export const SERIES_PALETTE = ['#5B9BD5', '#70AD63', '#9B79D0', '#DB7C92', '#3FB8AE', '#A6B23C', '#C66BA6', '#6E86A8'];

export type YTickFormat = 'cost' | 'int' | ((v: number) => string);

function resolveYTick(fmt: YTickFormat | undefined): (v: number) => string {
  if (typeof fmt === 'function') return fmt;
  if (fmt === 'int') return (v) => `${Math.round(v)}`;
  return compactCurrency; // default: cost
}

/** Base options shared by line/bar charts (muted grid, no legend box, html tooltip, currency y-axis). */
export function baseChartOptions(formatValue: TooltipValueFormatter, opts?: { stacked?: boolean; integerY?: boolean; yTickFormat?: YTickFormat }) {
  const yTick = resolveYTick(opts?.yTickFormat ?? (opts?.integerY ? 'int' : 'cost'));
  return {
    responsive: true,
    maintainAspectRatio: false,
    animation: { duration: 550, easing: 'easeOutQuart' as const },
    interaction: { intersect: false, mode: 'index' as const },
    // Subtle hover affordance — the canvas reads as clickable/explorable.
    onHover: (e: any, els: unknown[]) => {
      const t = e?.native?.target;
      if (t && t.style) t.style.cursor = els && els.length ? 'pointer' : 'default';
    },
    plugins: {
      legend: { display: false },
      tooltip: { enabled: false, external: makeExternalTooltip(formatValue) },
    },
    scales: {
      x: {
        stacked: !!opts?.stacked,
        grid: { display: false, drawBorder: false },
        ticks: { color: 'rgba(0,0,0,0.6)', font: { size: 10 }, maxRotation: 0, autoSkip: true, maxTicksLimit: 8 },
      },
      y: {
        stacked: !!opts?.stacked,
        grid: { color: 'rgba(0,0,0,0.06)', drawBorder: false },
        ticks: {
          color: 'rgba(0,0,0,0.5)',
          font: { size: 10 },
          maxTicksLimit: 5,
          padding: 6,
          callback: (v: number | string) => yTick(Number(v)),
          ...(opts?.integerY ? { precision: 0 } : {}),
        },
      },
    },
  };
}

/**
 * stackedTotalLabels — draws the per-column stacked total above each bar, so a
 * reader sees the actual $ for each period without hovering. Vertical bars only.
 */
export function stackedTotalLabels(format: (v: number) => string) {
  return {
    id: 'stackedTotalLabels',
    afterDatasetsDraw(chart: any) {
      const { ctx, scales } = chart;
      const xScale = scales.x;
      const yScale = scales.y;
      if (!xScale || !yScale) return;
      const count = chart.data?.labels?.length ?? 0;
      const visible = chart.data.datasets.map((_: unknown, di: number) => chart.isDatasetVisible(di));
      ctx.save();
      ctx.font = '600 10px sans-serif';
      ctx.fillStyle = 'rgba(0,0,0,0.62)';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'bottom';
      for (let i = 0; i < count; i++) {
        let total = 0;
        chart.data.datasets.forEach((ds: any, di: number) => {
          if (visible[di]) total += Number(ds.data?.[i]) || 0;
        });
        if (total <= 0) continue;
        const x = xScale.getPixelForValue(i);
        const y = yScale.getPixelForValue(total);
        ctx.fillText(format(total), x, y - 3);
      }
      ctx.restore();
    },
  };
}

/**
 * averageLine — dashed horizontal line at the mean of per-column totals
 * (stacked) or of the single series, with a small right-aligned label. Gives
 * the eye a baseline so spikes/dips read instantly.
 */
export function averageLine(format: (v: number) => string, opts?: { color?: string }) {
  const color = opts?.color ?? 'rgba(0,0,0,0.38)';
  return {
    id: 'averageLine',
    afterDatasetsDraw(chart: any) {
      const { ctx, chartArea, scales } = chart;
      const yScale = scales.y;
      const count = chart.data?.labels?.length ?? 0;
      if (!yScale || !count) return;
      const visible = chart.data.datasets.map((_: unknown, di: number) => chart.isDatasetVisible(di));
      let sum = 0;
      let n = 0;
      for (let i = 0; i < count; i++) {
        let total = 0;
        chart.data.datasets.forEach((ds: any, di: number) => {
          if (visible[di]) total += Number(ds.data?.[i]) || 0;
        });
        sum += total;
        n += 1;
      }
      if (!n) return;
      const avg = sum / n;
      if (avg <= 0) return;
      const y = yScale.getPixelForValue(avg);
      if (y < chartArea.top || y > chartArea.bottom) return;
      ctx.save();
      ctx.strokeStyle = color;
      ctx.setLineDash([4, 4]);
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(chartArea.left, y);
      ctx.lineTo(chartArea.right, y);
      ctx.stroke();
      ctx.setLineDash([]);
      ctx.font = '10px sans-serif';
      ctx.fillStyle = color;
      ctx.textAlign = 'right';
      ctx.textBaseline = 'bottom';
      ctx.fillText(`avg ${format(avg)}`, chartArea.right - 2, y - 2);
      ctx.restore();
    },
  };
}

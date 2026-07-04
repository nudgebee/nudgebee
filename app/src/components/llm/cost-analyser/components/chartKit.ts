/**
 * chartKit — Chart.js registration + shared chart building blocks for the
 * analyser:
 *   - a white, tabular external tooltip (white bg, data in a table, no outer
 *     border, faint row/column dividers, a per-row %-share column and a bold
 *     Total row when a point stacks multiple series);
 *   - `baseChartOptions` with currency/compact y-axis ticks, muted grid, and
 *     index-hover interaction;
 *   - reusable canvas plugins: `stackedTotalLabels` (the $ total atop each
 *     stacked bar) and `averageLine` (a dashed mean reference line).
 */
import { Chart as ChartJS, ArcElement, BarElement, CategoryScale, Filler, Legend, LineElement, LinearScale, PointElement, Tooltip } from 'chart.js';

ChartJS.register(ArcElement, BarElement, CategoryScale, Filler, Legend, LineElement, LinearScale, PointElement, Tooltip);

const TOOLTIP_ID = 'ca-chart-tooltip';

/** Compact currency for axis ticks / labels: $1.2k, $980, $0.50, $0. */
export function compactCurrency(v: number): string {
  const abs = Math.abs(v);
  if (abs >= 1_000_000) return `$${(v / 1_000_000).toFixed(abs >= 10_000_000 ? 0 : 1)}M`;
  if (abs >= 1_000) return `$${(v / 1_000).toFixed(abs >= 10_000 ? 0 : 1)}k`;
  if (abs === 0) return '$0';
  if (abs >= 1) return `$${v.toFixed(0)}`;
  return `$${v.toFixed(2)}`;
}

function getTooltipEl(): HTMLDivElement {
  let el = document.getElementById(TOOLTIP_ID) as HTMLDivElement | null;
  if (!el) {
    el = document.createElement('div');
    el.id = TOOLTIP_ID;
    Object.assign(el.style, {
      position: 'absolute',
      pointerEvents: 'none',
      background: '#ffffff',
      color: 'var(--ds-gray-700)',
      borderRadius: 'var(--ds-radius-md)',
      boxShadow: '0 4px 16px rgba(0,0,0,0.12)',
      padding: '8px 10px',
      fontSize: '11px',
      fontFamily: 'inherit',
      zIndex: '9999',
      opacity: '0',
      transition: 'opacity 0.1s ease',
      whiteSpace: 'nowrap',
    } as CSSStyleDeclaration);
    document.body.appendChild(el);
  }
  return el;
}

export type TooltipValueFormatter = (raw: unknown, label: string) => string;

interface TooltipRow {
  color: string;
  label: string;
  value: string;
  /** 0..1 share of the point total; rendered as a faint %-column when present. */
  share?: number;
}

function renderTable(title: string, rows: TooltipRow[], total?: { label: string; value: string }): string {
  const head = title ? `<div style="font-weight:var(--ds-font-weight-semibold);color:var(--ds-gray-700);margin-bottom:6px">${title}</div>` : '';
  const showShare = rows.some((r) => r.share != null);
  const bodyRows = rows.map((r, i) => {
    const last = i === rows.length - 1 && !total;
    const rowBorder = last ? '' : 'border-bottom:1px solid rgba(0,0,0,0.06);';
    const shareCell = showShare
      ? `<td style="padding:3px 0 3px 10px;text-align:right;font-variant-numeric:tabular-nums;color:var(--ds-gray-400);${rowBorder}">${
          r.share != null ? `${Math.round(r.share * 100)}%` : ''
        }</td>`
      : '';
    return (
      `<tr>` +
      `<td style="padding:3px 10px 3px 0;border-right:1px solid rgba(0,0,0,0.04);${rowBorder}">` +
      `<span style="display:inline-block;width:8px;height:8px;border-radius:2px;background:${r.color};margin-right:6px;vertical-align:middle"></span>` +
      `<span style="color:var(--ds-gray-600)">${r.label}</span>` +
      `</td>` +
      `<td style="padding:3px 0 3px 10px;text-align:right;font-variant-numeric:tabular-nums;color:var(--ds-gray-700);${rowBorder}">${r.value}</td>` +
      shareCell +
      `</tr>`
    );
  });
  const totalRow = total
    ? `<tr>` +
      `<td style="padding:5px 10px 1px 0;border-right:1px solid rgba(0,0,0,0.04);color:var(--ds-gray-700);font-weight:var(--ds-font-weight-semibold)">${total.label}</td>` +
      `<td style="padding:5px 0 1px 10px;text-align:right;font-variant-numeric:tabular-nums;color:var(--ds-gray-700);font-weight:var(--ds-font-weight-semibold)">${total.value}</td>` +
      (showShare ? `<td></td>` : '') +
      `</tr>`
    : '';
  // border-collapse + no border on <table> => no overall boundary line.
  return `${head}<table style="border-collapse:collapse;border:none">${bodyRows.join('')}${totalRow}</table>`;
}

/**
 * Build an external-tooltip handler. When a hovered point stacks multiple
 * series, each row gets its %-share of the point total and a bold Total row is
 * appended (formatted with `formatValue`).
 */
export function makeExternalTooltip(formatValue: TooltipValueFormatter) {
  return (context: { chart: { canvas: HTMLCanvasElement }; tooltip: any }) => {
    const { chart, tooltip } = context;
    const el = getTooltipEl();
    if (!tooltip || tooltip.opacity === 0) {
      el.style.opacity = '0';
      return;
    }
    const title = tooltip.title?.[0] ?? '';
    const points = tooltip.dataPoints ?? [];
    const numericTotal = points.reduce((sum: number, dp: any) => sum + (Number(dp.raw) || 0), 0);
    const multi = points.length > 1 && numericTotal > 0;
    const rows: TooltipRow[] = points.map((dp: any) => {
      const ds = dp.dataset ?? {};
      const color = (Array.isArray(ds.backgroundColor) ? ds.backgroundColor[dp.dataIndex] : ds.backgroundColor) || ds.borderColor || '#ccc';
      const label = ds.label || dp.label || '';
      return { color, label, value: formatValue(dp.raw, label), share: multi ? (Number(dp.raw) || 0) / numericTotal : undefined };
    });
    const total = multi ? { label: 'Total', value: formatValue(numericTotal, 'Total') } : undefined;
    el.innerHTML = renderTable(title, rows, total);

    const rect = chart.canvas.getBoundingClientRect();
    const left = rect.left + window.pageXOffset + tooltip.caretX + 12;
    const top = rect.top + window.pageYOffset + tooltip.caretY + 12;
    el.style.left = `${left}px`;
    el.style.top = `${top}px`;
    el.style.opacity = '1';
  };
}

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

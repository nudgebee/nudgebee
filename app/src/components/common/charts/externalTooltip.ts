/**
 * externalTooltip — a white, tabular Chart.js external (HTML) tooltip shared by
 * the DS chart wrappers (`@shared/charts/*`).
 *
 * Why an external tooltip: Chart.js' built-in tooltip can't render a real table
 * (per-row colour swatch + value + %-share column + a bold Total row). This
 * helper builds that as a floating, pointer-events:none `<div>` positioned at
 * the caret, styled with `--ds-*` tokens so it matches every other surface.
 *
 * Opt-in only — pass `external: makeExternalTooltip(fmt)` under
 * `options.plugins.tooltip` and set `tooltip.enabled = false`. Charts that don't
 * opt in keep Chart.js' default tooltip untouched.
 */

const TOOLTIP_ID = 'ds-chart-tooltip';

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

/**
 * legendChips — the chart legend for a series count nobody wants to read. The default legend prints,
 * per series, a swatch, a full PromQL-ish name and `Max / Min / P99 / Avg` on a second line.
 */
import { ds, resolveColor } from '@utils/colors';

/** The four numbers the legend has always shown for a series. */
export interface SeriesStats {
  min: number;
  max: number;
  p99: number;
  avg: number;
}

/** Stats over a series' points, or null when it has none. */
export function seriesStats(values: readonly unknown[] | undefined): SeriesStats | null {
  const points: number[] = [];
  for (const v of values ?? []) {
    if (v === '' || v === null || v === undefined) continue;
    const n = Number(v);
    if (Number.isFinite(n)) points.push(n);
  }
  if (points.length === 0) return null;
  points.sort((a, b) => a - b);
  const sum = points.reduce((a, b) => a + b, 0);
  return {
    min: points[0],
    max: points[points.length - 1],
    p99: points[Math.min(points.length - 1, Math.floor(points.length * 0.99))],
    avg: sum / points.length,
  };
}

/** A stat as it reads in a 4-column strip. */
export function formatStat(value: number | undefined, unit?: string): string {
  if (value === undefined || !Number.isFinite(value)) return '—';
  const abs = Math.abs(value);
  let text: string;
  if (abs >= 1e9) text = `${(value / 1e9).toFixed(2)}B`;
  else if (abs >= 1e6) text = `${(value / 1e6).toFixed(2)}M`;
  else if (abs >= 1e3) text = `${(value / 1e3).toFixed(2)}K`;
  else if (abs >= 1) text = value.toFixed(2);
  else if (abs === 0) text = '0';
  else text = String(Number(value.toPrecision(3)));
  return unit ? `${text} ${unit}` : text;
}

export interface ChipsLegendOptions {
  /** Suffixed to every number in the detail strip, e.g. `cores`, `MB`. */
  unit?: string;
}

/** The columns of the detail strip, in the order the old legend printed them. */
const STAT_COLUMNS: { key: keyof SeriesStats; label: string }[] = [
  { key: 'max', label: 'Max' },
  { key: 'min', label: 'Min' },
  { key: 'p99', label: 'P99' },
  { key: 'avg', label: 'Avg' },
];

/** Two rows of chips before it scrolls — enough to see the shape of the series set. */
const CHIP_ROWS_VISIBLE = '58px';

interface LegendEntry {
  datasetIndex: number;
  text: string;
  color: string;
  hidden: boolean;
  stats: SeriesStats | null;
}

function style(el: HTMLElement, declarations: Record<string, string>): void {
  Object.assign(el.style, declarations);
}

/** The chips row and the detail strip, created once and reused across updates. */
function scaffold(container: HTMLElement): { chips: HTMLElement; detail: HTMLElement } {
  const existing = container.querySelector('[data-chips]');
  if (existing) {
    return {
      chips: existing as HTMLElement,
      detail: container.querySelector('[data-detail]') as HTMLElement,
    };
  }
  container.textContent = '';

  const chips = document.createElement('div');
  chips.setAttribute('data-chips', 'true');
  chips.setAttribute('data-testid', 'chart-legend-chips');
  style(chips, {
    display: 'flex',
    flexWrap: 'wrap',
    alignItems: 'center',
    gap: ds.space[1],
    maxHeight: CHIP_ROWS_VISIBLE,
    overflowY: 'auto',
    overflowX: 'hidden',
  });

  const detail = document.createElement('div');
  detail.setAttribute('data-detail', 'true');
  detail.setAttribute('data-testid', 'chart-legend-detail');
  style(detail, {
    display: 'flex',
    alignItems: 'center',
    gap: ds.space[3],
    marginTop: ds.space[2],
    padding: `${ds.space[1]} ${ds.space[2]}`,
    borderRadius: ds.radius.md,
    background: ds.background[200],
    minHeight: '30px',
  });

  container.appendChild(chips);
  container.appendChild(detail);
  return { chips, detail };
}

/** Fills the strip with one series' name and numbers. */
function renderDetail(detail: HTMLElement, entry: LegendEntry | undefined, unit?: string): void {
  detail.textContent = '';
  if (!entry) return;

  const swatch = document.createElement('span');
  style(swatch, { width: ds.space[2], height: ds.space[2], borderRadius: '50%', background: entry.color, flexShrink: '0' });

  const name = document.createElement('span');
  name.textContent = entry.text;
  name.title = entry.text;
  style(name, {
    flex: '1',
    minWidth: '0',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    fontFamily: ds.font.mono,
    fontSize: ds.text.caption,
    color: ds.gray[700],
  });

  detail.appendChild(swatch);
  detail.appendChild(name);

  for (const column of STAT_COLUMNS) {
    const cell = document.createElement('div');
    style(cell, { display: 'flex', flexDirection: 'column', alignItems: 'flex-end', flexShrink: '0', minWidth: '46px', lineHeight: '1.25' });

    const heading = document.createElement('span');
    heading.textContent = column.label;
    style(heading, { fontSize: '10px', color: ds.gray[500] });

    const value = document.createElement('span');
    value.textContent = formatStat(entry.stats?.[column.key], unit);
    style(value, { fontFamily: ds.font.mono, fontSize: ds.text.caption, fontWeight: ds.weight.semibold, color: ds.gray[700] });

    cell.appendChild(heading);
    cell.appendChild(value);
    detail.appendChild(cell);
  }
}

/** One chip: swatch + truncated name, struck through while its series is hidden. */
function renderChip(entry: LegendEntry): HTMLElement {
  const chip = document.createElement('button');
  chip.type = 'button';
  chip.setAttribute('data-testid', 'chart-legend-chip');
  chip.title = entry.text;
  chip.setAttribute('aria-pressed', String(!entry.hidden));
  style(chip, {
    display: 'inline-flex',
    alignItems: 'center',
    gap: ds.space[1],
    maxWidth: '190px',
    padding: `${ds.space[0]} ${ds.space[2]}`,
    border: `1px solid ${ds.gray[200]}`,
    borderRadius: ds.radius.pill,
    background: ds.background[100],
    color: ds.gray[600],
    fontFamily: 'inherit',
    fontSize: ds.text.caption,
    lineHeight: '1.6',
    cursor: 'pointer',
    opacity: entry.hidden ? '0.45' : '1',
  });

  const swatch = document.createElement('span');
  style(swatch, { width: ds.space[2], height: ds.space[2], borderRadius: '50%', background: entry.color, flexShrink: '0' });

  const name = document.createElement('span');
  name.textContent = entry.text;
  style(name, {
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    textDecoration: entry.hidden ? 'line-through' : 'none',
  });

  chip.appendChild(swatch);
  chip.appendChild(name);
  return chip;
}

/**
 * The Chart.js plugin. Draws into the element with `containerId`, which the chart
 * wrapper renders below the canvas.
 */
export function chipsLegendPlugin(containerId: string, options: ChipsLegendOptions = {}) {
  return {
    id: 'chipsLegend',
    afterUpdate(chart: any) {
      const container = document.getElementById(containerId);
      if (!container) return;

      const items = chart.options?.plugins?.legend?.labels?.generateLabels?.(chart) ?? [];
      const entries: LegendEntry[] = items.map((item: any) => ({
        datasetIndex: item.datasetIndex,
        text: String(item.text ?? ''),
        color: resolveColor(item.fillStyle || item.strokeStyle || ds.gray[400]),
        hidden: Boolean(item.hidden),
        stats: seriesStats(chart.data?.datasets?.[item.datasetIndex]?.data),
      }));
      // Loudest first.
      entries.sort((a, b) => (b.stats?.max ?? -Infinity) - (a.stats?.max ?? -Infinity));

      const { chips, detail } = scaffold(container);
      chips.textContent = '';
      // A single series has nothing to pick between — the strip alone says it all.
      chips.style.display = entries.length > 1 ? 'flex' : 'none';

      const fallback = entries.find((e) => !e.hidden) ?? entries[0];
      let selected: HTMLElement | null = null;

      const focus = (entry: LegendEntry, chip: HTMLElement | null) => {
        if (selected) {
          selected.style.borderColor = ds.gray[200];
          selected.style.background = ds.background[100];
        }
        selected = chip;
        if (chip) {
          chip.style.borderColor = ds.blue[500];
          chip.style.background = ds.blue[100];
        }
        renderDetail(detail, entry, options.unit);
      };

      for (const entry of entries) {
        const chip = renderChip(entry);
        chip.addEventListener('mouseenter', () => focus(entry, chip));
        chip.addEventListener('focus', () => focus(entry, chip));
        chip.addEventListener('click', () => {
          chart.setDatasetVisibility(entry.datasetIndex, !chart.isDatasetVisible(entry.datasetIndex));
          // Re-enters this plugin, which rebuilds every chip from the chart's own
          // visibility state — no second copy of it to keep in step.
          chart.update();
        });
        chips.appendChild(chip);
      }

      chips.onmouseleave = () => focus(fallback, null);
      renderDetail(detail, fallback, options.unit);
    },
  };
}

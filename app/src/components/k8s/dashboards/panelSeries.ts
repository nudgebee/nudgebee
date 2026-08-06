import { convertNumberToTimestamp } from 'src/utils/common';
import type { PanelData } from './usePanelData';

/** One provider series before it is put on a shared time axis. */
export interface RawSeries {
  label: string;
  /** Unix seconds, as the provider returned them. */
  timestamps: number[];
  values: (number | null)[];
}

/**
 * Names a series the way an SRE reads one: `metric{label="value", …}`.
 *
 * The query key ("A") is the LAST resort — it is the same for every series a
 * query returns, so a query matching 30 targets would otherwise draw 30 lines
 * all called "A".
 *
 * A `legend_format` of `{{pod}} on {{node}}` wins when it renders to something;
 * an all-blank render (the labels aren't on this series) falls through to the
 * metric name rather than leaving the series anonymous.
 */
export function seriesLabel(metric: Record<string, string> | undefined | null, legendFormat: string | undefined, queryKey: string): string {
  if (legendFormat) {
    const rendered = legendFormat.replace(/\{\{\s*([\w.]+)\s*\}\}/g, (_m, name: string) => metric?.[name] ?? '');
    if (rendered.trim()) return rendered;
  }
  const name = metric?.__name__ || '';
  const entries = Object.entries(metric || {}).filter(([k]) => k !== '__name__');
  if (entries.length === 0) return name || queryKey || 'series';
  const inner = entries.map(([k, v]) => `${k}="${v}"`).join(', ');
  return `${name}{${inner}}`;
}

/**
 * The caption under a stat panel's number, or '' when there is nothing worth
 * saying.
 *
 * `seriesLabel` falls back to the query's ref id for a series carrying no
 * labels — and an aggregate (`sum(...)`, `min(...)`) drops every label, which
 * is exactly how a stat panel's query is written. That left a bare "A" under
 * each number, naming an internal identifier the viewer never chose. The panel
 * title already says what the number is, so an unlabelled series says nothing.
 */
export function statCaption(label: string | undefined, refIds: string[]): string {
  if (!label) return '';
  if (label === 'series' || refIds.includes(label)) return '';
  return label;
}

/** Flattens the provider's per-query results into one series list. */
export function toRawSeries(results: any[], legendByKey: Record<string, string>): RawSeries[] {
  const out: RawSeries[] = [];
  for (const result of results || []) {
    const key = result?.query_key;
    for (const series of result?.payload ?? []) {
      const timestamps: number[] = (series?.timestamps ?? []).map(toEpochSeconds);
      const values = (series?.values ?? []).map(toFinite);
      out.push({ label: seriesLabel(series?.metric, legendByKey[key], key), timestamps, values });
    }
  }
  return out;
}

/**
 * Puts every series on ONE time axis.
 *
 * Series from a single query rarely share a timeline — a pod that existed for
 * ten minutes reports ten minutes of points. Charting each series' values
 * against the FIRST series' timestamps lines up index-for-index, which draws
 * short-lived series compressed against the left edge and misdates every point
 * on them. So the axis is the sorted union of every timestamp, and each series
 * is looked up by timestamp.
 *
 * A gap is `null`, not 0: Chart.js breaks the line there, whereas 0 would draw
 * a drop to zero that never happened.
 */
export function alignSeries(raw: RawSeries[]): PanelData {
  const axis = [...new Set(raw.flatMap((s) => s.timestamps))].sort((a, b) => a - b);
  const labels = axis.map((t) => convertNumberToTimestamp(t * 1000));
  const series = raw.map((s) => {
    const byTimestamp = new Map<number, number | null>();
    s.timestamps.forEach((t, i) => byTimestamp.set(t, s.values[i] ?? null));
    return { label: s.label, values: axis.map((t) => byTimestamp.get(t) ?? null) };
  });
  return { labels, series };
}

/** The newest value a series actually reported — the tail may be a gap. */
export function lastValue(values: (number | null)[] | undefined): number | undefined {
  for (let i = (values?.length || 0) - 1; i >= 0; i--) {
    const v = values![i];
    if (v !== null && v !== undefined) return v;
  }
  return undefined;
}

/**
 * Normalises a provider's timestamp to epoch SECONDS.
 *
 * The providers disagree on the unit: Prometheus and friends report seconds,
 * the cloud sources (CloudWatch and the Azure / GCP paths beside it) report
 * milliseconds. `alignSeries` multiplies by 1000 to build the axis, so a
 * millisecond value would be charted tens of thousands of years out.
 *
 * 1e11 seconds is the year 5138, so nothing legitimate in seconds reaches it.
 */
function toEpochSeconds(value: unknown): number {
  const n = Number(value);
  if (!Number.isFinite(n)) return n;
  return n > 1e11 ? Math.round(n / 1000) : n;
}

/** Providers send values as strings as often as numbers; unparseable is a gap. */
function toFinite(v: unknown): number | null {
  const n = Number(v);
  return Number.isFinite(n) ? n : null;
}

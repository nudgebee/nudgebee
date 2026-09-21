import { convertNumberToTimestamp } from 'src/utils/common';
import type { PanelData, PanelSeries } from './usePanelData';

/** Joins an account name onto a series label, for a panel that queried several. */
export const ACCOUNT_LABEL_SEPARATOR = ' · ';

/** The legend label of one account's series on a multi-account panel. */
export function accountPrefixed(account: string, label: string): string {
  return `${account}${ACCOUNT_LABEL_SEPARATOR}${label}`;
}

/** The legend label of the series that adds the accounts up. */
export const CONSOLIDATED_LABEL = 'All accounts';

/** A series' label without its account prefix — the metric and labels alone. */
export function metricLabel(s: Pick<PanelSeries, 'label' | 'accountLabel'>): string {
  const prefix = s.accountLabel ? accountPrefixed(s.accountLabel, '') : '';
  return prefix && s.label.startsWith(prefix) ? s.label.slice(prefix.length) : s.label;
}

/** One provider series before it is put on a shared time axis. */
export interface RawSeries {
  label: string;
  /** Unix seconds, as the provider returned them. */
  timestamps: number[];
  values: (number | null)[];
  /**
   * The account this series came from, set only when the panel queried several.
   * `label` already carries the account as a `·`-joined prefix for the chart
   * legend; this is the same fact structured, so a stat panel can lay the
   * account out beside its number instead of splitting a display string.
   */
  accountLabel?: string;
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

/** One account's contribution to a stat, for the breakdown behind the number. */
export interface StatRow {
  /** The account name. '' on a panel that queried a single account. */
  account: string;
  /** `undefined` when the account reported nothing, or when it failed. */
  value: number | undefined;
  /** The account was asked and could not answer — distinct from answering with nothing. */
  failed: boolean;
}

/** A stat panel's number, and where it came from. */
export interface StatTotal {
  /** The sum across every account that answered. `undefined` when none did. */
  total: number | undefined;
  rows: StatRow[];
  /** The single-account caption, per statCaption. '' whenever a breakdown is shown. */
  caption: string;
  /** True when at least one account was asked and did not answer. */
  partial: boolean;
}

/**
 * Adds up what every account reported, and keeps the parts.
 *
 * A stat is one number, so a panel spanning four clusters has to either pick one
 * of them — which reads as the total and is not — or add them. It adds them, and
 * `rows` is what the number breaks down into, shown on hover: a total nobody can
 * take apart is a number nobody can check.
 *
 * `failed` is carried per account rather than folded into the sum, because a
 * cluster that could not answer and a cluster that answered zero are the same
 * arithmetic and completely different facts. An account that failed contributes
 * nothing and is named in the breakdown, so a total that is low for that reason
 * says so.
 *
 * Summing assumes the parts ADD — true of the `sum(...)` / `count(...)` an
 * aggregate stat query is written as, and NOT of an average or a percentile,
 * which have no meaningful sum across clusters. The breakdown is what makes that
 * visible: the parts are on screen next to the total.
 */
export function statTotal(series: PanelSeries[], refIds: string[], failedAccounts: string[] = []): StatTotal {
  const order: string[] = [];
  const byAccount = new Map<string, number | undefined>();

  for (const s of series) {
    const key = s.accountLabel || '';
    if (!byAccount.has(key)) {
      byAccount.set(key, undefined);
      order.push(key);
    }
    const value = lastValue(s.values);
    if (value === undefined) continue;
    // An account answering with several series contributes all of them: the
    // total is what the panel matched, not what its first series matched.
    byAccount.set(key, (byAccount.get(key) ?? 0) + value);
  }

  const rows: StatRow[] = order.map((account) => ({ account, value: byAccount.get(account), failed: false }));
  for (const account of failedAccounts) rows.push({ account, value: undefined, failed: true });

  const answered = rows.filter((r) => r.value !== undefined);
  const total = answered.length === 0 ? undefined : answered.reduce((sum, r) => sum + (r.value as number), 0);

  // One account has nothing to break down, so it keeps the caption it always had.
  const single = order.length <= 1 && failedAccounts.length === 0;
  return {
    total,
    rows,
    caption: single ? statCaption(series[0]?.label, refIds) : '',
    partial: failedAccounts.length > 0,
  };
}

/**
 * The series that adds the accounts up, one per metric label, for a chart that
 * draws several accounts.
 *
 * Per-account lines answer "how is each cluster doing"; they do not answer "how
 * much is there in all", which is what a panel scoped to every cluster is
 * usually asking. So the consolidated view is drawn ALONGSIDE the parts, on the
 * same axis — it is the same unit, and a second axis would let the total be
 * read against a different scale than the lines it is the sum of.
 *
 * Grouped by the label the accounts share once their prefix is stripped: an
 * aggregate (`sum(...)`) answers with one unlabelled series per account and
 * gets one total; `sum by (namespace)` gets one per namespace. A label only ONE
 * account reports — a pod name, say — gets none, because a total of one line is
 * that line drawn twice.
 *
 * An account with a gap at an instant is left out of that instant's sum, and
 * the total is `null` only where EVERY account has one: 95 + gap + 10 is 105,
 * the sum of what was reported. Breaking the total on any single gap left the
 * line full of holes on fleets where clusters miss the odd scrape.
 *
 * Empty on a single-account panel: nothing to add.
 */
export function consolidatedSeries(series: PanelSeries[]): PanelSeries[] {
  const accounts = new Set(series.map((s) => s.accountLabel).filter(Boolean));
  if (accounts.size < 2) return [];

  const order: string[] = [];
  const groups = new Map<string, PanelSeries[]>();
  for (const s of series) {
    if (s.consolidated) continue;
    const key = metricLabel(s);
    if (!groups.has(key)) {
      groups.set(key, []);
      order.push(key);
    }
    groups.get(key)!.push(s);
  }

  const out: PanelSeries[] = [];
  for (const key of order) {
    const parts = groups.get(key)!;
    // Distinct ACCOUNTS, not distinct labels: a series carrying no account must
    // not count as a second one beside a series that does.
    if (new Set(parts.map((p) => p.accountLabel).filter(Boolean)).size < 2) continue;
    const length = parts[0].values.length;
    const values: (number | null)[] = [];
    for (let i = 0; i < length; i++) {
      let sum = 0;
      let reported = false;
      for (const p of parts) {
        const v = p.values[i];
        if (v === null || v === undefined) continue;
        sum += v;
        reported = true;
      }
      values.push(reported ? sum : null);
    }
    // One metric label on the whole chart needs no qualifier; several do, or
    // the totals would all be called the same thing.
    const label = order.length === 1 ? CONSOLIDATED_LABEL : accountPrefixed(CONSOLIDATED_LABEL, key);
    out.push({ label, values, consolidated: true });
  }
  return out;
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
 * Puts every account's points on ONE grid, when several accounts share a chart.
 *
 * Each account is a separate cluster answering from its own Prometheus. They are
 * all sent the same start, end and step — but they do not all answer on the same
 * instants, and an offset of a few seconds is enough to break the chart
 * completely: the union axis then interleaves the accounts, every series is
 * `null` wherever another account reported, and no series has two CONSECUTIVE
 * points. Chart.js draws a line between adjacent non-null points, so it drew
 * nothing at all — an empty plot with a correct axis and a correct Max/Min/Avg
 * underneath it, which is how this was found.
 *
 * Rounding each instant to the nearest multiple of the step collapses the offset
 * accounts back onto one grid, because they are all on the same step to begin
 * with; anything less than half a step apart lands in the same bucket.
 *
 * Left alone for a single account: there is nothing to reconcile, and shifting
 * its points by up to half a step would move a chart that was already right.
 */
function snapToStep(raw: RawSeries[], step?: number): RawSeries[] {
  if (!step || step <= 0) return raw;
  const accounts = new Set(raw.map((s) => s.accountLabel || ''));
  if (accounts.size < 2) return raw;
  return raw.map((s) => {
    const timestamps: number[] = [];
    const values: (number | null)[] = [];
    s.timestamps.forEach((t, i) => {
      const bucket = Math.round(t / step) * step;
      // A backend answering finer than the step we asked for can land two points
      // in one bucket; the later one is the more recent answer for that instant.
      if (timestamps[timestamps.length - 1] === bucket) {
        values[values.length - 1] = s.values[i] ?? null;
        return;
      }
      timestamps.push(bucket);
      values.push(s.values[i] ?? null);
    });
    return { ...s, timestamps, values };
  });
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
export function alignSeries(raw: RawSeries[], step?: number): PanelData {
  const snapped = snapToStep(raw, step);
  const axis = [...new Set(snapped.flatMap((s) => s.timestamps))].sort((a, b) => a - b);
  const labels = axis.map((t) => convertNumberToTimestamp(t * 1000));
  const series = snapped.map((s) => {
    const byTimestamp = new Map<number, number | null>();
    s.timestamps.forEach((t, i) => byTimestamp.set(t, s.values[i] ?? null));
    return { label: s.label, accountLabel: s.accountLabel, values: axis.map((t) => byTimestamp.get(t) ?? null) };
  });
  // The same axis twice: `labels` is the printable form the non-timeseries
  // visualisations read, `timestamps` the raw instants the chart's x-axis and
  // tooltip format for themselves.
  return { labels, timestamps: axis.map((t) => t * 1000), series };
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

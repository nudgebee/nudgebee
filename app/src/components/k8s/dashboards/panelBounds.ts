import type { RawSeries } from './panelSeries';

/**
 * What one panel is allowed to fetch and draw.
 *
 * A panel is a few hundred pixels wide and is read at a glance, so there is a
 * ceiling on what it can usefully show — and, with Chart.js drawing one dataset
 * per series and the legend one chip per series, a hard ceiling on what the
 * browser survives. A per-pod query over a day of ephemeral runner pods came
 * back as 4,798 series and froze the tab; a 7-day range at the agent's default
 * 60s step is 10k points per series before a single series is drawn.
 *
 * Every product that renders arbitrary queries bounds both axes and says so:
 * Cloud Monitoring draws at most 50 series, New Relic facets default to 10,
 * Datadog caps a line at 1,500 points, Grafana sizes the step to the panel
 * width. These are the same two bounds.
 */

/** The busiest series a chart draws. The rest are reported, not drawn. */
export const MAX_CHART_SERIES = 20;

/**
 * A table paginates, so it can hold more — but each row is still a series the
 * browser has to receive, align and keep in memory.
 */
export const MAX_TABLE_SERIES = 500;

/** Points per series a range query aims for: one every couple of pixels. */
export const TARGET_POINTS = 200;

/**
 * How long a panel waits before giving up on its request. The relay times out
 * at 30s and the query engine at 120s, so without this a wedged provider held
 * the panel's skeleton — and its load slot — for as long as either allowed.
 */
export const PANEL_TIMEOUT_MS = 30_000;

/**
 * Steps a range query may use, in seconds. Snapping to these keeps consecutive
 * refreshes asking the same question, which is what lets a result cache hit.
 */
const STEP_LADDER = [15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400];

/**
 * The step, in seconds, that puts about TARGET_POINTS points on the range:
 * the smallest rung of the ladder that is at least range / target.
 */
export function panelStep(startMs: number, endMs: number, targetPoints = TARGET_POINTS): number {
  const rangeSeconds = Math.max(0, (endMs - startMs) / 1000);
  const raw = rangeSeconds / Math.max(1, targetPoints);
  return STEP_LADDER.find((s) => s >= raw) ?? STEP_LADDER[STEP_LADDER.length - 1];
}

/** The largest finite magnitude a series reaches; a series with no numbers is -Infinity. */
function peak(series: RawSeries): number {
  let max = -Infinity;
  for (const v of series.values) {
    if (v === null || !Number.isFinite(v)) continue;
    const abs = Math.abs(v);
    if (abs > max) max = abs;
  }
  return max;
}

/**
 * Keeps the `max` busiest series, in their original order, and reports how many
 * were dropped. "Busiest" is the largest magnitude the series reaches — the
 * lines a viewer would look for on the chart. Stable, so two series with the
 * same peak keep the provider's order.
 */
export function capSeries(raw: RawSeries[], max: number): { kept: RawSeries[]; dropped: number } {
  if (raw.length <= max) return { kept: raw, dropped: 0 };
  const ranked = raw
    .map((series, index) => ({ series, index, peak: peak(series) }))
    // Compared rather than subtracted: `peak` is -Infinity for an all-null
    // series, and -Infinity - -Infinity is NaN. NaN happens to fall through to
    // the index tiebreak today, but a comparator is only defined for negative,
    // zero and positive — engines owe us nothing for NaN.
    .sort((a, b) => (a.peak === b.peak ? a.index - b.index : b.peak > a.peak ? 1 : -1))
    .slice(0, max)
    .sort((a, b) => a.index - b.index)
    .map((r) => r.series);
  return { kept: ranked, dropped: raw.length - max };
}

/**
 * Keeps the busiest series from EACH account, rather than the busiest overall.
 *
 * `capSeries` ranks one pool by magnitude, which on a multi-account panel is a
 * popularity contest between accounts: a cluster whose numbers are an order of
 * magnitude larger takes every slot, and the quieter accounts vanish from the
 * chart with nothing to say they were ever queried. That is the failure the cap
 * exists to prevent, one level up.
 *
 * So the chart's budget is divided evenly and each account is ranked inside its
 * own share — one series each at worst, since an account represented by nothing
 * is exactly what this avoids. Order within an account is the provider's, and
 * accounts keep the order they were queried in.
 */
export function capSeriesByAccount(raw: RawSeries[], maxTotal: number): { kept: RawSeries[]; perAccount: AccountCap[] } {
  const order: string[] = [];
  const byAccount = new Map<string, RawSeries[]>();
  for (const series of raw) {
    // A panel that queried one account labels no series with it; they all share
    // one bucket and the split degenerates to plain capSeries, as it should.
    const key = series.accountLabel || '';
    if (!byAccount.has(key)) {
      byAccount.set(key, []);
      order.push(key);
    }
    byAccount.get(key)!.push(series);
  }

  const share = Math.max(1, Math.floor(maxTotal / Math.max(1, order.length)));
  const kept: RawSeries[] = [];
  const perAccount: AccountCap[] = [];
  for (const key of order) {
    const mine = byAccount.get(key)!;
    const capped = capSeries(mine, share);
    kept.push(...capped.kept);
    perAccount.push({ account: key, kept: capped.kept.length, total: mine.length });
  }
  return { kept, perAccount };
}

/** How much of one account's answer a capped chart is showing. */
export interface AccountCap {
  account: string;
  kept: number;
  total: number;
}

/**
 * The warning a per-account cap shows.
 *
 * Names the accounts that were ACTUALLY trimmed and their real counts, because
 * a share is not what every account hit: two accounts matching 177 and 5 series
 * against a share of 10 draw 10 and 5, and "the 10 busiest from each" is false
 * for the second and overstates what was dropped.
 *
 * The single-account message's advice survives — an aggregation really does
 * reduce a per-account count — with the other lever named alongside it, since
 * the share shrinks as accounts are added and filtering to one account restores
 * the full budget.
 */
export function accountCappedWarning(perAccount: AccountCap[]): string {
  const trimmed = perAccount.filter((a) => a.kept < a.total);
  if (trimmed.length === 0) return '';
  // One account is the ordinary cap, and keeps the message it always had —
  // "each of 1 accounts" is both wrong and worse.
  if (perAccount.length === 1) return cappedSeriesWarning(perAccount[0].kept, perAccount[0].total);

  const advice = 'Add an aggregation such as sum by (…), or filter to one account, to see the rest.';
  // Past a few accounts the per-account list stops being readable and the share
  // is the fact that carries; the true total still says how much was dropped.
  if (trimmed.length > 3) {
    const total = trimmed.reduce((sum, a) => sum + a.total, 0);
    const share = trimmed[0].kept;
    return `Showing the ${share} busiest series from each of ${trimmed.length} accounts — ${total} matched. ${advice}`;
  }
  const parts = trimmed.map((a) => `${a.kept} of ${a.total} from ${a.account}`);
  const list = parts.length === 1 ? parts[0] : `${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}`;
  return `Showing ${list}. ${advice}`;
}

/** The warning a capped panel shows, so the cap is a fact the viewer sees rather than data that silently vanished. */
export function cappedSeriesWarning(kept: number, total: number): string {
  return `Showing the ${kept} busiest of ${total} series. Add an aggregation such as sum by (…) to the query to see the rest.`;
}

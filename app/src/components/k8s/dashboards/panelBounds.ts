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

/** The warning a capped panel shows, so the cap is a fact the viewer sees rather than data that silently vanished. */
export function cappedSeriesWarning(kept: number, total: number): string {
  return `Showing the ${kept} busiest of ${total} series. Add an aggregation such as sum by (…) to the query to see the rest.`;
}

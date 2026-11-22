import dayjs from 'dayjs';
import { ds } from '@utils/colors';

// One line per label — matches the per-workload utilisation chart (Usage amber,
// Request blue, Limit red), with Total in grey as the capacity ceiling.
export const TREND_SERIES = [
  { field: 'usage', label: 'Usage', color: ds.amber[500] },
  { field: 'total', label: 'Total', color: ds.gray[400] },
  { field: 'request', label: 'Request', color: ds.blue[400] },
  { field: 'limit', label: 'Limit', color: ds.red[500] },
];

const formatTrendTs = (ts) => dayjs(ts > 1e12 ? ts : ts * 1000).format('HH:mm');

// The usual spacing between a series' samples: how long one sample may stand for.
const typicalGap = (timestamps) => {
  const gaps = timestamps
    .slice(1)
    .map((t, i) => t - timestamps[i])
    .filter((g) => g > 0)
    .sort((a, b) => a - b);
  return gaps.length ? gaps[(gaps.length - 1) >> 1] : 0;
};

// Place a series on the chart's time grid. Series in one batch need not share a
// grid: Elasticsearch searches requests and limits at a coarser step than usage
// on long ranges. Each grid slot takes the series' latest sample at or before it,
// which covers one of the series' own steps from its timestamp, so a sample is
// never stretched over time it does not cover. Slots before the first sample stay
// empty. Range-query timestamps arrive ascending, which the single pass relies on.
export const alignToGrid = (grid, { timestamps, values }) => {
  const hold = timestamps.length > 1 ? typicalGap(timestamps) : typicalGap(grid);
  const out = new Array(grid.length).fill(null);
  let j = -1;
  for (let i = 0; i < grid.length; i++) {
    while (j + 1 < timestamps.length && timestamps[j + 1] <= grid[i]) j++;
    if (j >= 0 && (grid[i] === timestamps[j] || grid[i] - timestamps[j] < hold)) out[i] = values[j];
  }
  return out;
};

// Build Chart.Series { labels, dataset } from a metric group's range-query response.
export const buildTrendChart = (results, cfg) => {
  const byKey = {};
  results.forEach((item) => {
    const first = (item.payload || [])[0];
    byKey[item.query_key] = { values: (first?.values || []).map((v) => parseFloat(v)), timestamps: first?.timestamps || [] };
  });
  // The densest series sets the time grid; the others are placed on it by timestamp.
  const gridSeries = Object.values(byKey).reduce((a, b) => (b.timestamps.length > a.timestamps.length ? b : a), { timestamps: [] });
  const grid = gridSeries.timestamps;
  const labels = grid.map(formatTrendTs);
  const dataset = TREND_SERIES.map((s) => {
    const series = byKey[cfg.keys[s.field]] || { values: [], timestamps: [] };
    return {
      label: s.label,
      data: alignToGrid(grid, series).map((v) => (Number.isFinite(v) ? v / cfg.divisor : null)),
      borderColor: s.color,
      backgroundColor: s.color,
      borderWidth: 1.5,
      pointRadius: 0,
      tension: 0.3,
      fill: false,
    };
  });
  return { labels, dataset };
};

/**
 * Chart — DS namespace wrapper around the chart family.
 * Spec: app/design-system/primitives/data-display/chart.html
 *
 * Usage (preferred):
 *   import Chart from '@ui/Chart';
 *   <Chart.Line ... />        // heavy metrics/Prometheus line chart (own tooltip/legend)
 *   <Chart.Series ... />      // lean line/area passthrough — caller supplies datasets + options
 *   <Chart.TimeSeries ... />  // multi-series bar/area/line from {labels, series} + totals legend
 *   <Chart.Bar ... />
 *   <Chart.Doughnut ... />
 *
 * Legacy default-import paths continue to work for existing callers:
 *   import LineCharts from '@shared/charts/LineCharts';
 *
 * Both resolve to the same component instance.
 *
 * Line vs Series vs TimeSeries: `Line` is the feature-rich Prometheus chart
 * (pinned points, Ask-Nubi, its own tooltip). `Series` is a thin line/area
 * passthrough for callers that build their own datasets/options. `TimeSeries` is
 * the higher-level composition — give it `{ labels, series }` + `format`/`colorFor`
 * and it builds gradient datasets, the tabular tooltip, on-bar totals / end labels,
 * and a per-series totals legend (stacked bar, stacked area, or multi-line).
 */
import LineCharts from '@shared/charts/LineCharts';
import SeriesChart from '@shared/charts/SeriesChart';
import TimeSeriesChart from '@shared/charts/TimeSeriesChart';
import BarChart from '@shared/charts/BarChart';
import DoughnutChart from '@shared/charts/DoughnutChart';

export const Chart = {
  Line: LineCharts,
  Series: SeriesChart,
  TimeSeries: TimeSeriesChart,
  Bar: BarChart,
  Doughnut: DoughnutChart,
} as const;

export default Chart;

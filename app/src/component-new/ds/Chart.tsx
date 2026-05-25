/**
 * Chart — DS namespace wrapper around the chart family.
 * Spec: app/design-system/primitives/data-display/chart.html
 *
 * Usage (preferred):
 *   import Chart from '@components1/ds/Chart';
 *   <Chart.Line ... />
 *   <Chart.Bar ... />
 *   <Chart.Doughnut ... />
 *
 * Legacy default-import paths continue to work for existing callers:
 *   import LineCharts from '@components1/common/charts/LineCharts';
 *
 * Both resolve to the same component instance.
 */
import LineCharts from '@components1/common/charts/LineCharts';
import BarChart from '@components1/common/charts/BarChart';
import DoughnutChart from '@components1/common/charts/DoughnutChart';

export const Chart = {
  Line: LineCharts,
  Bar: BarChart,
  Doughnut: DoughnutChart,
} as const;

export default Chart;

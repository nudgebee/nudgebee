import React from 'react';
import { Chart as ChartJS, CategoryScale, LinearScale, PointElement, LineElement, Filler, Title, Tooltip, Legend, Colors } from 'chart.js';
import { Line } from 'react-chartjs-2';
import PropTypes from 'prop-types';
import { withErrorBoundary } from '@shared/ErrorBoundary';
import { resolveDatasetColor } from './chartPlugins';

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Filler, Title, Tooltip, Legend, Colors);

/**
 * SeriesChart — a lean, categorical line / area chart driven entirely by the
 * caller's datasets + options. Deliberately separate from `LineCharts` (the
 * heavy Prometheus/metrics line chart with its own tooltip, legend container and
 * "Ask Nubi" affordances): this one is a thin passthrough so callers can supply
 * the shared base options (`chartPlugins.baseChartOptions`), the shared tabular
 * tooltip, and their own canvas plugins — the same contract as the enhanced
 * `BarChart`.
 *
 * @param {{
 *   labels?: string[],
 *   dataset?: any[],          // full Chart.js datasets (passed through; gradient fns kept)
 *   options?: object,         // full Chart.js options passthrough
 *   customPlugins?: any[],    // extra plugins appended after the no-data plugin
 *   id?: string,
 *   loading?: boolean,
 *   maxHeight?: number,
 * }} props
 */
const SeriesChart = ({ labels = [], dataset = [], options = {}, customPlugins = [], id = '', loading = false, maxHeight = 264 }) => {
  const chartDatasets = (dataset || []).map((obj) => ({
    ...obj,
    ...(obj.backgroundColor && { backgroundColor: resolveDatasetColor(obj.backgroundColor) }),
    ...(obj.borderColor && { borderColor: resolveDatasetColor(obj.borderColor) }),
  }));

  const chartData = { labels: labels || [], datasets: chartDatasets };

  const noDataPlugin = {
    afterDraw: function (chart) {
      if (chart.data.datasets && chart.data.datasets.length > 0) {
        const firstDatasetData = chart.data.datasets[0].data;
        if (!firstDatasetData || firstDatasetData.length < 1) {
          const ctx = chart.ctx;
          const width = chart.width;
          const height = chart.height;
          ctx.save();
          ctx.textAlign = 'center';
          ctx.textBaseline = 'middle';
          ctx.font = '30px Arial';
          ctx.fillText('No data to display', width / 2, height / 2);
          ctx.restore();
        }
      }
    },
  };

  return (
    <>
      {loading ? (
        <div className='shimmer' style={{ maxHeight }} />
      ) : (
        <Line id={id} style={{ maxHeight }} options={options} data={chartData} plugins={[noDataPlugin, ...customPlugins]} />
      )}
    </>
  );
};

SeriesChart.propTypes = {
  labels: PropTypes.array,
  dataset: PropTypes.array,
  options: PropTypes.object,
  customPlugins: PropTypes.array,
  id: PropTypes.string,
  loading: PropTypes.bool,
  maxHeight: PropTypes.number,
};

export default withErrorBoundary(SeriesChart);

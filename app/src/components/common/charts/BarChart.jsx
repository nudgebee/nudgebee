import React from 'react';
import { Chart as ChartJS, CategoryScale, LinearScale, BarElement, Title, Tooltip, Legend, Colors } from 'chart.js';
import { Bar } from 'react-chartjs-2';
import PropTypes from 'prop-types';
import { ds, resolveColor } from 'src/utils/colors';
import { withErrorBoundary } from '@shared/ErrorBoundary';
import { resolveDatasetColor } from './chartPlugins';

ChartJS.register(CategoryScale, LinearScale, BarElement, Title, Tooltip, Legend, Colors);

/**
 * @param {{
 *   data?: any[],
 *   labels?: string[],
 *   colors?: string | string[],
 *   chartLabel?: string | string[],
 *   dataset?: any[],
 *   id?: string,
 *   chartTitle?: string,
 *   loading?: boolean,
 *   options?: object,
 *   customPlugins?: any[],
 *   maxHeight?: number,
 * }} props
 *
 * Opt-in extensions (all default off — existing callers are unaffected):
 *   - `options`        — full Chart.js options passthrough. When provided it
 *                        REPLACES the built-in stacked/legend defaults, letting a
 *                        caller drive scales, tooltip (e.g. the shared external
 *                        tooltip), interaction and `onClick` (drill).
 *   - `customPlugins`  — extra Chart.js plugins appended after the no-data plugin
 *                        (e.g. `stackedTotalLabels`, `averageLine`).
 *   - `maxHeight`      — cap the canvas height (default 230).
 */
const BarChart = ({
  data,
  labels,
  colors = ds.blue[500],
  chartLabel = '',
  dataset = [],
  id = '',
  chartTitle = '',
  loading = false,
  options: optionsProp,
  customPlugins = [],
  maxHeight = 230,
}) => {
  if (!data) {
    data = [[]];
  } else if (data.length === 0) {
    data = [[]];
  }

  if (typeof colors === 'string') {
    colors = [colors];
  }
  colors = colors.map(resolveColor);

  if (typeof chartLabel === 'string') {
    chartLabel = [chartLabel];
  }

  if (data && data.length > 0 && !Array.isArray(data[0])) {
    data = [data];
    chartLabel = [chartLabel];
  }

  let chartDatasets = [];
  if (dataset && dataset.length > 0) {
    chartDatasets = dataset.map((obj) => ({
      ...obj,
      ...(obj.backgroundColor && { backgroundColor: resolveDatasetColor(obj.backgroundColor) }),
      ...(obj.borderColor && { borderColor: resolveDatasetColor(obj.borderColor) }),
    }));
  } else {
    chartDatasets = data.map((item, index) => ({
      label: chartLabel[index] || '',
      data: item || [],
      backgroundColor: colors[index],
      borderRadius: 4,
      barPercentage: 0.3,
    }));
  }
  const chartData = {
    labels: labels || [],
    datasets: chartDatasets,
  };

  // Built per-render (never a shared module-level object) so concurrent charts
  // — e.g. one with a title, one without — never clobber each other's options.
  const builtOptions = {
    responsive: true,
    interaction: {
      intersect: false,
      mode: 'index',
    },
    scales: {
      x: { stacked: true },
      y: { stacked: true },
    },
    plugins: {
      legend: {
        position: 'top',
        align: 'end',
        labels: {
          boxWidth: 8,
          boxHeight: 8,
          borderRadius: 10,
        },
      },
      title: chartTitle ? { display: true, text: chartTitle } : { display: false },
    },
  };

  const options = optionsProp || builtOptions;

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
        <div className='shimmer' style={{ maxHeight: 230 }} />
      ) : (
        <Bar id={id} style={{ maxHeight }} options={options} data={chartData} plugins={[noDataPlugin, ...customPlugins]} />
      )}
    </>
  );
};

BarChart.propTypes = {
  data: PropTypes.array,
  labels: PropTypes.array,
  colors: PropTypes.oneOfType([PropTypes.string, PropTypes.array]),
  chartLabel: PropTypes.oneOfType([PropTypes.string, PropTypes.array]),
  dataset: PropTypes.array,
  id: PropTypes.string,
  chartTitle: PropTypes.string,
  loading: PropTypes.bool,
  options: PropTypes.object,
  customPlugins: PropTypes.array,
  maxHeight: PropTypes.number,
};

export default withErrorBoundary(BarChart);

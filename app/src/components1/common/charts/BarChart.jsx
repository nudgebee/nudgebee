import React from 'react';
import { Chart as ChartJS, CategoryScale, LinearScale, BarElement, Title, Tooltip, Legend, Colors } from 'chart.js';
import { Bar } from 'react-chartjs-2';
import PropTypes from 'prop-types';
import { rawColors as color, resolveColor } from 'src/utils/colors';
import { withErrorBoundary } from '@common/ErrorBoundary';

ChartJS.register(CategoryScale, LinearScale, BarElement, Title, Tooltip, Legend, Colors);

const options = {
  responsive: true,
  interaction: {
    intersect: false,
    mode: 'index',
  },
  scales: {
    x: {
      stacked: true,
    },
    y: {
      stacked: true,
    },
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
    title: { display: false },
  },
};

const BarChart = ({ data, labels, colors = color.text.barChart, chartLabel = '', dataset = [], id = '', chartTitle = '', loading = false }) => {
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
      ...(obj.backgroundColor && {
        backgroundColor: Array.isArray(obj.backgroundColor) ? obj.backgroundColor.map(resolveColor) : resolveColor(obj.backgroundColor),
      }),
      ...(obj.borderColor && {
        borderColor: Array.isArray(obj.borderColor) ? obj.borderColor.map(resolveColor) : resolveColor(obj.borderColor),
      }),
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

  if (chartTitle) {
    options.plugins.title = {
      display: true,
      text: chartTitle,
    };
  }

  return (
    <>
      {loading ? (
        <div className='shimmer' style={{ maxHeight: 230 }} />
      ) : (
        <Bar
          id={id}
          style={{ maxHeight: 230 }}
          options={options}
          data={chartData}
          plugins={[
            {
              afterDraw: function (chart) {
                if (chart.data.datasets && chart.data.datasets.length > 0) {
                  if (chart.data.datasets[0].data.length < 1) {
                    let ctx = chart.ctx;
                    let width = chart.width;
                    let height = chart.height;
                    ctx.textAlign = 'center';
                    ctx.textBaseline = 'middle';
                    ctx.font = '30px Arial';
                    ctx.fillText('No data to display', width / 2, height / 2);
                    ctx.restore();
                  }
                }
              },
            },
          ]}
        />
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
};

export default withErrorBoundary(BarChart);

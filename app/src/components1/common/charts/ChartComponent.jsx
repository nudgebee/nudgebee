import PropTypes from 'prop-types';
import { Bar, Pie, Line } from 'react-chartjs-2';
import { Chart as ChartJS, ArcElement, BarElement, CategoryScale, LinearScale, Title, Tooltip, Legend } from 'chart.js';
import { withErrorBoundary } from '@common/ErrorBoundary';
import { resolveColor } from 'src/utils/colors';

ChartJS.register(ArcElement, BarElement, CategoryScale, LinearScale, Title, Tooltip, Legend);

const resolveDatasetColors = (data) => {
  if (!data?.datasets) return data;
  return {
    ...data,
    datasets: data.datasets.map((ds) => ({
      ...ds,
      ...(ds.borderColor && {
        borderColor: Array.isArray(ds.borderColor) ? ds.borderColor.map(resolveColor) : resolveColor(ds.borderColor),
      }),
      ...(ds.backgroundColor && {
        backgroundColor: Array.isArray(ds.backgroundColor) ? ds.backgroundColor.map(resolveColor) : resolveColor(ds.backgroundColor),
      }),
    })),
  };
};

const ChartComponent = ({ type, data: rawData, options, maxHeight = 200, loading }) => {
  const data = resolveDatasetColors(rawData);
  const chartTypes = {
    bar: Bar,
    pie: Pie,
    line: Line,
  };

  const SelectedChart = chartTypes[type];

  return loading ? (
    <div className='shimmer' style={{ maxHeight: maxHeight }} />
  ) : (
    <SelectedChart
      data={data}
      options={options}
      style={{ maxHeight: maxHeight }}
      plugins={[
        {
          beforeDraw: function (chart) {
            const hasData = chart.data.datasets.some((dataset) => dataset.data.length > 0);
            if (!hasData) {
              const ctx = chart.ctx;
              const { width, height } = chart;
              ctx.save();
              ctx.clearRect(0, 0, width, height);
              ctx.textAlign = 'center';
              ctx.textBaseline = 'middle';
              ctx.font = '20px Arial';
              ctx.fillText('No data to display', width / 2, height / 2);
              ctx.restore();
            }
          },
        },
      ]}
    />
  );
};

ChartComponent.propTypes = {
  type: PropTypes.oneOf(['bar', 'pie', 'line']).isRequired,
  data: PropTypes.object.isRequired,
  options: PropTypes.object,
  maxHeight: PropTypes.number,
  loading: PropTypes.bool.isRequired,
};

export default withErrorBoundary(ChartComponent);

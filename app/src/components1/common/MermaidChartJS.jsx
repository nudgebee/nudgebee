import { useMemo } from 'react';
import PropTypes from 'prop-types';
import ChartComponent from './charts/ChartComponent';
import { withErrorBoundary } from '@common/ErrorBoundary';

/**
 * Parse Mermaid xychart syntax and return chart configuration
 */
function parseMermaidChart(mermaidCode) {
  const lines = mermaidCode
    .trim()
    .split('\n')
    .map((l) => l.trim());

  const config = {
    title: '',
    xAxisLabel: '',
    yAxisLabel: '',
    xAxisData: [],
    datasets: [],
    chartType: 'line',
  };

  for (let line of lines) {
    if (!line || line.startsWith('xychart')) {
      continue;
    }

    // Parse title
    if (line.startsWith('title')) {
      const match = line.match(/title\s+"([^"]+)"/);
      if (match) {
        config.title = match[1];
      }
    }

    // Parse x-axis
    else if (line.startsWith('x-axis')) {
      // Extract label if present
      const labelMatch = line.match(/x-axis\s+"([^"]+)"/);
      if (labelMatch) {
        config.xAxisLabel = labelMatch[1];
      }

      // Extract data array
      const dataMatch = line.match(/\[([^\]]+)\]/);
      if (dataMatch) {
        config.xAxisData = dataMatch[1].split(',').map((v) => v.trim().replace(/(?:^["'])|(?:["']$)/g, ''));
      }
    }

    // Parse y-axis
    else if (line.startsWith('y-axis')) {
      const labelMatch = line.match(/y-axis\s+"([^"]+)"/);
      if (labelMatch) {
        config.yAxisLabel = labelMatch[1];
      }
    }

    // Parse line data
    else if (line.startsWith('line')) {
      const labelMatch = line.match(/line\s+"([^"]+)"\s*\[/);
      const label = labelMatch ? labelMatch[1] : `Series ${config.datasets.length + 1}`;

      const dataMatch = line.match(/\[([^\]]+)\]/);
      if (dataMatch) {
        const data = dataMatch[1].split(',').map((v) => parseFloat(v.trim()));

        config.datasets.push({
          label,
          data,
          type: 'line',
        });
      }
    }

    // Parse bar data
    else if (line.startsWith('bar')) {
      const labelMatch = line.match(/bar\s+"([^"]+)"\s*\[/);
      const label = labelMatch ? labelMatch[1] : `Series ${config.datasets.length + 1}`;

      const dataMatch = line.match(/\[([^\]]+)\]/);
      if (dataMatch) {
        const data = dataMatch[1].split(',').map((v) => parseFloat(v.trim()));

        config.datasets.push({
          label,
          data,
          type: 'bar',
        });

        config.chartType = 'bar';
      }
    }
  }

  return config;
}

/**
 * Generate colors for datasets
 */
function getDatasetColors(index) {
  const colors = [
    { border: 'rgb(75, 192, 192)', bg: 'rgba(75, 192, 192, 0.2)' },
    { border: 'rgb(255, 99, 132)', bg: 'rgba(255, 99, 132, 0.2)' },
    { border: 'rgb(54, 162, 235)', bg: 'rgba(54, 162, 235, 0.2)' },
    { border: 'rgb(255, 206, 86)', bg: 'rgba(255, 206, 86, 0.2)' },
    { border: 'rgb(153, 102, 255)', bg: 'rgba(153, 102, 255, 0.2)' },
    { border: 'rgb(255, 159, 64)', bg: 'rgba(255, 159, 64, 0.2)' },
  ];

  return colors[index % colors.length];
}

/**
 * MermaidChartJS Component - Wraps your existing ChartComponent
 */
export function MermaidChartJS({ mermaidCode }) {
  const { chartData, chartOptions, chartType } = useMemo(() => {
    if (!mermaidCode) {
      return { chartData: null, chartOptions: null, chartType: 'line' };
    }

    const config = parseMermaidChart(mermaidCode);

    if (config.datasets.length === 0 || config.xAxisData.length === 0) {
      console.warn('Invalid chart data', config);
      return { chartData: null, chartOptions: null, chartType: 'line' };
    }

    // Determine if x-axis should be rotated based on data size
    const shouldRotateLabels = config.xAxisData.length > 10 || config.xAxisData.some((label) => String(label).length > 5);

    // Build datasets with proper styling
    const datasets = config.datasets.map((dataset, idx) => {
      const colors = getDatasetColors(idx);

      if (config.chartType === 'line') {
        return {
          label: dataset.label,
          data: dataset.data,
          borderColor: colors.border,
          backgroundColor: colors.bg,
          borderWidth: 2,
          pointRadius: config.xAxisData.length === 1 ? 5 : 3,
          pointHoverRadius: 6,
          tension: 0.4,
          fill: false,
        };
      }
      // Bar chart
      return {
        label: dataset.label,
        data: dataset.data,
        borderColor: colors.border,
        backgroundColor: colors.bg,
        borderWidth: 1,
      };
    });

    // Chart.js data format
    const data = {
      labels: config.xAxisData,
      datasets: datasets,
    };

    // Chart.js options
    const options = {
      responsive: true,
      maintainAspectRatio: true,
      aspectRatio: 2,
      interaction: {
        mode: 'index',
        intersect: false,
      },
      plugins: {
        title: {
          display: !!config.title,
          text: config.title,
          font: {
            size: 16,
            weight: 'bold',
          },
          padding: {
            top: 10,
            bottom: 20,
          },
        },
        legend: {
          display: config.datasets.length > 1,
          position: 'top',
          labels: {
            usePointStyle: true,
            padding: 15,
            boxWidth: 8,
            boxHeight: 8,
          },
        },
        tooltip: {
          enabled: true,
          mode: 'index',
          intersect: false,
          backgroundColor: 'rgba(0, 0, 0, 0.8)',
          titleFont: {
            size: 13,
          },
          bodyFont: {
            size: 12,
          },
          padding: 12,
          cornerRadius: 4,
          callbacks: {
            label: function (context) {
              let label = context.dataset.label || '';
              if (label) {
                label += ': ';
              }
              if (context.parsed.y !== null) {
                label += context.parsed.y.toFixed(4);
              }
              return label;
            },
          },
        },
      },
      scales: {
        x: {
          display: true,
          title: {
            display: !!config.xAxisLabel,
            text: config.xAxisLabel,
            font: {
              size: 13,
              weight: 'bold',
            },
            padding: {
              top: 10,
            },
          },
          ticks: {
            maxRotation: shouldRotateLabels ? 45 : 0,
            minRotation: shouldRotateLabels ? 45 : 0,
            autoSkip: true,
            maxTicksLimit: config.xAxisData.length > 20 ? 15 : undefined,
            font: {
              size: 11,
            },
          },
          grid: {
            display: false,
            drawBorder: true,
            color: 'rgba(0, 0, 0, 0.05)',
          },
        },
        y: {
          display: true,
          title: {
            display: !!config.yAxisLabel,
            text: config.yAxisLabel,
            font: {
              size: 13,
              weight: 'bold',
            },
            padding: {
              bottom: 10,
            },
          },
          ticks: {
            font: {
              size: 11,
            },
            callback: function (value) {
              // Format large numbers
              if (Math.abs(value) >= 1000000) {
                return (value / 1000000).toFixed(1) + 'M';
              } else if (Math.abs(value) >= 1000) {
                return (value / 1000).toFixed(1) + 'K';
              }
              return value.toFixed(2);
            },
          },
          grid: {
            display: true,
            color: 'rgba(0, 0, 0, 0.05)',
          },
        },
      },
    };

    return {
      chartData: data,
      chartOptions: options,
      chartType: config.chartType,
    };
  }, [mermaidCode]);

  if (!chartData) {
    return (
      <div
        style={{
          padding: '16px',
          backgroundColor: '#f8f9fa',
          borderRadius: '8px',
          border: '1px solid #e2e8f0',
          marginBottom: '16px',
          textAlign: 'center',
          color: '#6c757d',
        }}
      >
        Unable to parse chart data
      </div>
    );
  }

  return (
    <div
      style={{
        padding: '16px',
        backgroundColor: '#ffffff',
        borderRadius: '8px',
        border: '1px solid #e2e8f0',
        marginBottom: '16px',
      }}
    >
      <ChartComponent type={chartType} data={chartData} options={chartOptions} maxHeight={400} loading={false} />
    </div>
  );
}

MermaidChartJS.propTypes = {
  mermaidCode: PropTypes.string.isRequired,
};

export default withErrorBoundary(MermaidChartJS);

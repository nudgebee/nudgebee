import { Box, Typography, Grid } from '@mui/material';
import PropTypes from 'prop-types';
import { LineChart } from '@components1/common';
import { ds } from '@utils/colors';

const KubernetesRecommendationCharts = ({ memoryData, cpuData, recc, loading }) => {
  const memoryLabels = Object.values(memoryData.labels);
  const memoryLabelsMid = Math.floor(memoryLabels?.length / 2);
  const memoryReccValue = parseFloat(recc?.memoryRecc?.replaceAll(',', ''));

  const cpuLabels = Object.values(cpuData.labels);
  const cpuLabelsMid = Math.floor(cpuLabels?.length / 2);
  const cpuReccValue = parseFloat(recc.cpuRecc);

  const cpuData1 = {
    labels: cpuLabels,
    datasets: [
      {
        type: 'line',
        tension: 0.3,
        label: 'Limit',
        borderColor: '#e5484d',
        borderDash: [8, 2],
        fill: false,
        data: cpuData.data[2],
        borderWidth: 1,
        pointRadius: 0,
        hidden: true,
      },
      {
        type: 'line',
        tension: 0.3,
        label: 'Recommendation',
        borderColor: '#2ca84c',
        fill: false,
        data: Array(cpuLabels.length).fill(cpuReccValue),
        borderWidth: 1,
        pointRadius: 0,
      },
      {
        type: 'line',
        tension: 0.3,
        label: 'Requested',
        borderColor: '#2f7af0',
        fill: false,
        data: cpuData.data[1],
        borderWidth: 1,
        pointRadius: 0,
        hidden: true,
      },
      {
        type: 'line',
        label: 'Usage',
        tension: 0.3,
        borderColor: '#f5b400',
        fill: false,
        data: cpuData.data[0],
        borderWidth: 1,
        pointRadius: 0,
      },
    ],
  };

  const memoryData1 = {
    labels: memoryLabels,
    datasets: [
      {
        type: 'line',
        tension: 0.3,
        label: 'Limit',
        borderColor: '#e5484d',
        borderDash: [8, 2],
        fill: false,
        data: memoryData.data[2],
        borderWidth: 1,
        pointRadius: 0,
        hidden: true,
      },
      {
        type: 'line',
        tension: 0.3,
        label: 'Recommendation',
        borderColor: '#2ca84c',
        fill: false,
        data: Array(memoryLabels.length).fill(memoryReccValue),
        borderWidth: 1,
        pointRadius: 0,
      },
      {
        type: 'line',
        tension: 0.3,
        label: 'Requested',
        borderColor: '#2f7af0',
        fill: false,
        data: memoryData.data[1],
        borderWidth: 1,
        pointRadius: 0,
        hidden: true,
      },
      {
        type: 'line',
        label: 'Usage',
        borderColor: '#f5b400',
        tension: 0.3,
        fill: false,
        data: memoryData.data[0],
        borderWidth: 1,
        pointRadius: 0,
      },
    ],
  };

  const cpuOptions = {
    scales: {
      x: {
        grid: { display: false },
        ticks: {
          autoSkip: true,
          callback: function (value, index, _ticks) {
            if (index == 0 || index == cpuLabelsMid || index === cpuLabels.length - 1) {
              return cpuLabels[index]?.split('T')[0];
            }
          },
        },
      },
    },
  };

  const memOptions = {
    scales: {
      x: {
        grid: { display: false },
        ticks: {
          autoSkip: true,
          callback: function (value, index, _ticks) {
            if (index === 0 || index === memoryLabelsMid || index === memoryLabels.length - 1) {
              return memoryLabels[index]?.split('T')[0];
            }
          },
        },
      },
    },
  };

  return (
    <Grid container spacing={3}>
      <Grid item xs={6}>
        <Box
          sx={{
            margin: 'var(--ds-space-4) 0',
            padding: 'var(--ds-space-6) var(--ds-space-5)',
            display: 'flex',
            flexDirection: 'column',
            borderRadius: 'var(--ds-radius-lg)',
            border: `1px solid ${ds.brand[200]}`,
            background: ds.background[200],
            height: '70%',
          }}
        >
          <Typography fontSize={ds.text.bodyLg} fontWeight={600} color={ds.brand[500]}>
            CPU(Core)
          </Typography>
          <LineChart dataset={cpuData1.datasets} labels={cpuData1.labels} scaleOptions={cpuOptions.scales} loading={loading} />
        </Box>
      </Grid>
      <Grid item xs={6}>
        <Box
          sx={{
            margin: 'var(--ds-space-4) 0',
            padding: 'var(--ds-space-6) var(--ds-space-5)',
            display: 'flex',
            flexDirection: 'column',
            borderRadius: 'var(--ds-radius-lg)',
            border: `1px solid ${ds.brand[200]}`,
            background: ds.background[200],
            height: '70%',
          }}
        >
          <Typography fontSize={ds.text.bodyLg} fontWeight={600} color={ds.brand[500]}>
            Memory(MB)
          </Typography>
          <LineChart dataset={memoryData1.datasets} labels={memoryData1.labels} scaleOptions={memOptions.scales} loading={loading} />
        </Box>
      </Grid>
    </Grid>
  );
};

KubernetesRecommendationCharts.propTypes = {
  memoryData: PropTypes.object,
  cpuData: PropTypes.object,
  recc: PropTypes.any,
  loading: PropTypes.bool,
};

export default KubernetesRecommendationCharts;

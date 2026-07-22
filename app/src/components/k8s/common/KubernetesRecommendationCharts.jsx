import { Box, Typography, Grid } from '@mui/material';
import PropTypes from 'prop-types';
import Chart from '@ui/Chart';
import MetricQueryInfo, { K8S_METRIC_QUERY_LABELS } from '@shared/MetricQueryInfo';
import { ds, resolveColor } from '@utils/colors';

const KubernetesRecommendationCharts = ({ memoryData, cpuData, recc, loading, cpuQueries, memoryQueries }) => {
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
        borderColor: resolveColor(ds.red[500]),
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
        borderColor: resolveColor(ds.green[500]),
        fill: false,
        data: Array(cpuLabels.length).fill(cpuReccValue),
        borderWidth: 1,
        pointRadius: 0,
      },
      {
        type: 'line',
        tension: 0.3,
        label: 'Requested',
        borderColor: resolveColor(ds.blue[500]),
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
        borderColor: resolveColor(ds.amber[500]),
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
        borderColor: resolveColor(ds.red[500]),
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
        borderColor: resolveColor(ds.green[500]),
        fill: false,
        data: Array(memoryLabels.length).fill(memoryReccValue),
        borderWidth: 1,
        pointRadius: 0,
      },
      {
        type: 'line',
        tension: 0.3,
        label: 'Requested',
        borderColor: resolveColor(ds.blue[500]),
        fill: false,
        data: memoryData.data[1],
        borderWidth: 1,
        pointRadius: 0,
        hidden: true,
      },
      {
        type: 'line',
        label: 'Usage',
        borderColor: resolveColor(ds.amber[500]),
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
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
            <Typography fontSize={ds.text.bodyLg} fontWeight={600} color={ds.brand[500]}>
              CPU(Core)
            </Typography>
            <MetricQueryInfo queries={cpuQueries} labelMap={K8S_METRIC_QUERY_LABELS} />
          </Box>
          <Chart.Line dataset={cpuData1.datasets} labels={cpuData1.labels} scaleOptions={cpuOptions.scales} loading={loading} />
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
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
            <Typography fontSize={ds.text.bodyLg} fontWeight={600} color={ds.brand[500]}>
              Memory(MB)
            </Typography>
            <MetricQueryInfo queries={memoryQueries} labelMap={K8S_METRIC_QUERY_LABELS} />
          </Box>
          <Chart.Line dataset={memoryData1.datasets} labels={memoryData1.labels} scaleOptions={memOptions.scales} loading={loading} />
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
  cpuQueries: PropTypes.object,
  memoryQueries: PropTypes.object,
};

export default KubernetesRecommendationCharts;

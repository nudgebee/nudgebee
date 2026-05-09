import { LineChart } from '@components1/common';
import { Box, Typography, Grid } from '@mui/material';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';

const InvestigationCharts = ({
  data, // Container memory usage
  labels, // Container memory usage labels
  dataN, // Node memory usage
  labelsN, // Node memory usage labels
  dataRequest, // Container memory request
  memLimit, // Memory limit
  occurredAt, // Occurred at index
  dataP, // Pod memory usage
  labelsP, // Pod memory usage labels
  podLimitRequest, // Pod memory limit and request
  dataRequestN, // Node memory request
  dataRequestL, // Node memory request labels
  resourceType = 'memory',
}) => {
  let containerData = {};
  if (data && data.length > 0) {
    containerData = {
      labels: labels || [],
      datasets: [
        {
          type: 'line',
          tension: 0.3,
          pointRadius: 0,
          label: 'Limit',
          borderDash: [8, 2],
          borderColor: colors.text.cpuLimit, // Dark blue color
          borderWidth: 1,
          fill: false,
          data: Array(labels.length).fill(memLimit) || [],
        },
        {
          type: 'line',
          tension: 0.4,
          pointRadius: 0,
          borderColor: colors.text.cpuUsage,
          borderWidth: 1,
          fill: false,
          data: data !== null && data !== undefined ? data : [],
          label: 'Usage',
        },
        {
          type: 'line',
          tension: 0.4,
          pointRadius: 0,
          borderColor: colors.text.cpuRequested,
          borderWidth: 1,
          fill: false,
          data: dataRequest || [],
          label: 'Request',
        },
      ],
    };
  }

  if (labels && labels.length > 0 && occurredAt) {
    const values = Array(labels.length).fill(0);
    values[occurredAt] = Math.max(...data);
    containerData.datasets.push({
      type: 'bar',
      label: 'Occurred At',
      colors: colors.text.cpuRecommendation,
      data: values || [],
      fill: true,
      borderWidth: 1,
      barThickness: 1,
    });
  }

  let nodeData = {
    labels: [],
    datasets: [],
  };
  if (dataN && dataN.length > 0) {
    nodeData = {
      labels: labelsN || [],
      datasets: [
        {
          type: 'line',
          tension: 0.4,
          borderColor: colors.text.cpuUsage,
          pointRadius: 0,
          borderWidth: 1,
          fill: false,
          data: dataN !== null && dataN !== undefined ? dataN : [],
          label: 'Usage',
        },
      ],
    };
  }

  if (!labelsN && dataRequestL && dataRequestL.length > 0) {
    nodeData.labels = dataRequestN;
  }

  if (dataRequestN && dataRequestN.length > 0) {
    nodeData.datasets.push({
      type: 'line',
      tension: 0.4,
      pointRadius: 0,
      borderColor: colors.text.cpuRequested,
      borderWidth: 1,
      fill: false,
      data: dataRequestN !== null && dataRequestN !== undefined ? dataRequestN : [],
      label: 'Request',
    });
  }

  let podData = {};
  if (dataP && dataP.length > 0) {
    podData = {
      labels: labelsP || [],
      datasets: [
        {
          type: 'line',
          tension: 0.4,
          borderColor: colors.text.cpuUsage,
          pointRadius: 0,
          borderWidth: 1,
          fill: false,
          data: dataP !== null && dataP !== undefined ? dataP : [],
          label: 'Usage',
        },
      ],
    };
    if (resourceType == 'memory' && podLimitRequest?.limits) {
      podData.datasets.push({
        type: 'line',
        tension: 0.3,
        pointRadius: 0,
        label: 'Limit',
        borderColor: colors.text.cpuLimit, // Dark blue color
        borderDash: [8, 2],
        borderWidth: 1,
        fill: false,
        data: Array(labelsP.length).fill(podLimitRequest?.limits / (1024 * 1024)) || [],
      });
    }
    if (resourceType == 'memory' && podLimitRequest?.request) {
      podData.datasets.push({
        type: 'line',
        tension: 0.4,
        pointRadius: 0,
        borderColor: colors.text.cpuRequested,
        borderWidth: 1,
        fill: false,
        data: Array(labelsP.length).fill(podLimitRequest?.request / (1024 * 1024)) || [],
        label: 'Request',
      });
    }
    if (resourceType == 'cpu' && podLimitRequest?.cpu_limit) {
      podData.datasets.push({
        type: 'line',
        tension: 0.3,
        pointRadius: 0,
        label: 'Limit',
        borderColor: colors.text.cpuLimit, // Dark blue color
        borderDash: [8, 2],
        borderWidth: 1,
        fill: false,
        data: Array(labelsP.length).fill(podLimitRequest?.cpu_limit * 1000) || [],
      });
    }
    if (resourceType == 'cpu' && podLimitRequest?.cpu_request) {
      podData.datasets.push({
        type: 'line',
        tension: 0.4,
        pointRadius: 0,
        borderColor: colors.text.cpuRequested,
        borderWidth: 1,
        fill: false,
        data: Array(labelsP.length).fill(podLimitRequest?.cpu_request * 1000) || [],
        label: 'Request',
      });
    }
  }

  if (labelsP && labelsP.length > 0 && occurredAt) {
    const values = Array(labelsP.length).fill(0);
    values[occurredAt] = Math.max(...dataP);
    podData.datasets.push({
      type: 'bar',
      label: 'Occurred At',
      colors: colors.text.cpuRecommendation,

      data: values || [],
      fill: true,
      borderColor: 'red',
      borderWidth: 1,
      barThickness: 1,
    });
  }

  const options = {
    scales: {
      y: {
        stepSize: 0,
        display: true,
        grid: {
          display: true,
        },
      },
      x: { grid: { display: true } },
    },
  };

  const hasContainerSection = data && data.length > 0;
  const hasNodeSection = (dataN && dataN.length > 0) || (dataRequestN && dataRequestN.length > 0);
  const hasPodSection = dataP && dataP.length > 0;
  const sectionsCount = (hasContainerSection ? 1 : 0) + (hasNodeSection ? 1 : 0) + (hasPodSection ? 1 : 0);
  const sectionXs = sectionsCount <= 1 ? 12 : sectionsCount === 2 ? 6 : 4;

  return (
    <Grid container spacing={2}>
      {hasContainerSection ? (
        <Grid item xs={12} md={sectionXs}>
          <Box
            sx={{
              margin: '20px 0',
              padding: '16px 16px',
              display: 'flex',
              flexDirection: 'column',
              borderRadius: '8px',
              border: '1px solid #D0D0D0',
              background: '#F6FAFF',
              height: '80%',
            }}
          >
            <Typography fontSize={'14px'} fontWeight={500} color='#374151'>
              {`Container ${resourceType}` + `${resourceType == 'memory' ? ' (MB)' : ''}`}
            </Typography>
            <LineChart
              colors={[colors.text.memoryUsage, colors.text.memoryRequested, colors.text.memoryLimit]}
              dataset={containerData.datasets ?? []}
              labels={containerData.labels}
              scaleOptions={options.scales}
              data={containerData}
            />
          </Box>
        </Grid>
      ) : null}
      {hasNodeSection ? (
        <Grid item xs={12} md={sectionXs}>
          <Box
            gap={'8px'}
            sx={{
              margin: '20px 0',
              padding: '16px 16px',
              display: 'flex',
              flexDirection: 'column',
              borderRadius: '8px',
              border: '1px solid #D0D0D0',
              background: '#F6FAFF',
              height: '80%',
            }}
          >
            <Typography fontSize={'14px'} fontWeight={500} color='#374151'>
              {`Node ${resourceType}` + `${resourceType == 'memory' ? ' (GB)' : ''}`}
            </Typography>
            <LineChart dataset={nodeData.datasets ?? []} labels={nodeData.labels} scaleOptions={options.scales} />
          </Box>
        </Grid>
      ) : null}
      {hasPodSection ? (
        <Grid item xs={12} md={sectionXs}>
          <Box
            gap={'8px'}
            sx={{
              margin: '20px 0',
              padding: '31px 25px',
              display: 'flex',
              flexDirection: 'column',
              borderRadius: '8px',
              border: '1px solid #D0D0D0',
              background: '#F6FAFF',
              height: '70%',
            }}
          >
            <Typography fontSize={'14px'} fontWeight={600} color='#374151'>
              {`Pod ${resourceType}` + `${resourceType == 'memory' ? ' (MB)' : ''}`}
            </Typography>
            <LineChart dataset={podData.datasets ?? []} labels={podData.labels} scaleOptions={options.scales} />
          </Box>
        </Grid>
      ) : null}
    </Grid>
  );
};

InvestigationCharts.propTypes = {
  data: PropTypes.array,
  labels: PropTypes.array,
  dataN: PropTypes.array,
  labelsN: PropTypes.array,
  dataRequest: PropTypes.array,
  memLimit: PropTypes.number,
  occurredAt: PropTypes.number,
  dataP: PropTypes.array,
  labelsP: PropTypes.array,
  podLimitRequest: PropTypes.object,
  dataRequestN: PropTypes.array,
  dataRequestL: PropTypes.array,
  resourceType: PropTypes.string,
};

export default InvestigationCharts;

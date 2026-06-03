import React, { useEffect, useState } from 'react';
import k8sApi from '@api1/kubernetes';
import { Box, Typography, Grid, Alert } from '@mui/material';
import { LineChart } from '@components1/common';
import { ds } from 'src/utils/colors';
import PropTypes from 'prop-types';

const STARTUP_WINDOW_MINUTES = 30;
const MIN_POD_AGE_MINUTES = 5;
const MAX_PODS = 5;

// Distinct colors for per-pod usage lines
const POD_COLORS = ['#8B5CF6', '#F59E0B', '#06B6D4', '#EC4899', '#10B981'];

function fetchPodMetrics(pod, query, groupBy, datasource) {
  const createdMs = new Date(pod.timestamp).getTime();
  return k8sApi
    .getK8sPodGroupings2(10, query, groupBy, datasource || 'prometheus')
    .then((res) => ({ pod, data: res?.data?.k8s_pod_groupings || [], createdMs }))
    .catch((err) => {
      console.error(`Failed to fetch startup metrics for pod ${pod.name}:`, err);
      return { pod, data: [], createdMs };
    });
}

function alignEntriesToRelativeTime(entries, createdMs, maxMinutes, valueFn) {
  const values = Array.from({ length: maxMinutes + 1 }, () => null);
  for (const entry of entries) {
    const relMin = Math.round((new Date(entry.timestamp).getTime() - createdMs) / (1000 * 60));
    if (relMin >= 0 && relMin <= maxMinutes) {
      values[relMin] = valueFn(entry);
    }
  }
  return values;
}

const KubernetesStartupCharts = ({ accountId, workloadName, namespaceName, containerName, recc, datasource }) => {
  const [cpuDatasets, setCpuDatasets] = useState([]);
  const [memDatasets, setMemDatasets] = useState([]);
  const [labels, setLabels] = useState([]);
  const [isLoading, setIsLoading] = useState(false);
  const [infoMessage, setInfoMessage] = useState(null);
  const [podCount, setPodCount] = useState(0);
  const [podDetailsSummary, setPodDetailsSummary] = useState('');

  useEffect(() => {
    if (!accountId || !namespaceName || !workloadName) {
      return;
    }

    const fetchStartupMetrics = async () => {
      setIsLoading(true);
      setInfoMessage(null);

      try {
        // Step 1: Fetch recent active pods for this workload
        const podsRes = await k8sApi.getK8sPods(MAX_PODS, 0, { accountId, namespaceName, workloadName, isActive: true }, false);
        const pods = podsRes?.data?.k8s_pods || [];

        if (pods.length === 0) {
          setInfoMessage('No active pods found for this workload.');
          return;
        }

        const now = Date.now();
        // Filter out pods that are too new to have meaningful data
        const eligiblePods = pods.filter((pod) => {
          const createdMs = new Date(pod.timestamp).getTime();
          return now - createdMs >= MIN_POD_AGE_MINUTES * 60 * 1000;
        });

        if (eligiblePods.length === 0) {
          setInfoMessage('Pods were recently created. Startup metrics may take a few minutes to appear.');
          return;
        }

        // Step 2: Fetch startup metrics for each pod in parallel
        const groupBy = ['tenant_id', 'account_id', 'timestamp', 'namespace_name', 'pod_name'];
        const metricsPromises = eligiblePods.map((pod) => {
          const createdMs = new Date(pod.timestamp).getTime();
          const endMs = Math.min(createdMs + STARTUP_WINDOW_MINUTES * 60 * 1000, now);
          const query = {
            accountId,
            podName: pod.name,
            namespaceName,
            containerName,
            startDate: new Date(createdMs),
            endDate: new Date(endMs),
            metrics: ['cpu_usage', 'memory_usage', 'cpu_limit', 'cpu_request', 'memory_limit', 'memory_request'],
          };
          return fetchPodMetrics(pod, query, groupBy, datasource);
        });

        const results = await Promise.all(metricsPromises);

        // Filter to pods that returned data
        const podsWithData = results.filter((r) => r.data.length > 0);

        if (podsWithData.length === 0) {
          setInfoMessage('No startup metrics available. Data may have expired from Prometheus.');
          return;
        }

        setPodCount(podsWithData.length);
        const creationTimes = podsWithData.map((r) => r.createdMs).sort((a, b) => a - b);
        const earliest = new Date(creationTimes[0]);
        const latest = new Date(creationTimes[creationTimes.length - 1]);
        if (creationTimes.length === 1) {
          setPodDetailsSummary(`Started at ${earliest.toLocaleString()}`);
        } else {
          setPodDetailsSummary(`Started between ${earliest.toLocaleString()} — ${latest.toLocaleString()}`);
        }

        // Step 3: Build unified relative time labels (0m, 1m, 2m, ... 30m)
        const maxMinutes = STARTUP_WINDOW_MINUTES;
        const relativeLabels = [];
        for (let m = 0; m <= maxMinutes; m++) {
          relativeLabels.push(`${m}m`);
        }
        setLabels(relativeLabels);

        // Step 4: Build per-pod datasets aligned to relative time
        const cpuDs = [];
        const memDs = [];

        // Get request/limit/recommendation from the first pod with data (shared across pods)
        const firstResult = podsWithData[0];
        const requestCpuValues = alignEntriesToRelativeTime(
          firstResult.data,
          firstResult.createdMs,
          maxMinutes,
          (e) => e.avg_cpu_request || recc?.cpuRequest || 0
        );
        const limitCpuValues = alignEntriesToRelativeTime(
          firstResult.data,
          firstResult.createdMs,
          maxMinutes,
          (e) => e.avg_cpu_limit || recc?.cpuLimit || 0
        );
        const requestMemValues = alignEntriesToRelativeTime(
          firstResult.data,
          firstResult.createdMs,
          maxMinutes,
          (e) => (e.avg_memory_request || 0) / (1024 * 1024)
        );
        const limitMemValues = alignEntriesToRelativeTime(
          firstResult.data,
          firstResult.createdMs,
          maxMinutes,
          (e) => (e.avg_memory_limit || 0) / (1024 * 1024)
        );

        const extractCpuUsed = (e) => e.avg_cpu_used;
        const extractMemUsedMb = (e) => (e.avg_memory_used || 0) / (1024 * 1024);

        podsWithData.forEach((result, idx) => {
          const podShortName = result.pod.name.split('-').slice(-2).join('-');
          const color = POD_COLORS[idx % POD_COLORS.length];

          const cpuValues = alignEntriesToRelativeTime(result.data, result.createdMs, maxMinutes, extractCpuUsed);
          const memValues = alignEntriesToRelativeTime(result.data, result.createdMs, maxMinutes, extractMemUsedMb);

          cpuDs.push({
            type: 'line',
            tension: 0.3,
            label: `Pod ${podShortName}`,
            borderColor: color,
            fill: false,
            data: cpuValues,
            borderWidth: 2,
            pointRadius: 1,
          });

          memDs.push({
            type: 'line',
            tension: 0.3,
            label: `Pod ${podShortName}`,
            borderColor: color,
            fill: false,
            data: memValues,
            borderWidth: 2,
            pointRadius: 1,
          });
        });

        // Add shared lines: Recommendation, Requested, Limit
        const cpuReccValue = parseFloat(recc?.cpuRecc);
        const memReccValue = parseFloat(recc?.memoryRecc?.toString().replaceAll(',', ''));

        if (!isNaN(cpuReccValue)) {
          cpuDs.push({
            type: 'line',
            tension: 0.3,
            label: 'Recommendation',
            borderColor: '#2ca84c',
            fill: false,
            data: Array.from({ length: relativeLabels.length }, () => cpuReccValue),
            borderWidth: 1,
            pointRadius: 0,
          });
        }

        cpuDs.push({
          type: 'line',
          tension: 0.3,
          label: 'Requested',
          borderColor: '#2f7af0',
          fill: false,
          data: requestCpuValues,
          borderWidth: 1,
          pointRadius: 0,
        });

        cpuDs.push({
          type: 'line',
          tension: 0.3,
          label: 'Limit',
          borderColor: '#e5484d',
          borderDash: [8, 2],
          fill: false,
          data: limitCpuValues,
          borderWidth: 1,
          pointRadius: 0,
        });

        if (!isNaN(memReccValue)) {
          memDs.push({
            type: 'line',
            tension: 0.3,
            label: 'Recommendation',
            borderColor: '#2ca84c',
            fill: false,
            data: Array.from({ length: relativeLabels.length }, () => memReccValue),
            borderWidth: 1,
            pointRadius: 0,
          });
        }

        memDs.push({
          type: 'line',
          tension: 0.3,
          label: 'Requested',
          borderColor: '#2f7af0',
          fill: false,
          data: requestMemValues,
          borderWidth: 1,
          pointRadius: 0,
        });

        memDs.push({
          type: 'line',
          tension: 0.3,
          label: 'Limit',
          borderColor: '#e5484d',
          borderDash: [8, 2],
          fill: false,
          data: limitMemValues,
          borderWidth: 1,
          pointRadius: 0,
        });

        setCpuDatasets(cpuDs);
        setMemDatasets(memDs);
      } catch (err) {
        console.error('Failed to fetch startup metrics:', err);
        setInfoMessage('Failed to load startup metrics.');
      } finally {
        setIsLoading(false);
      }
    };

    fetchStartupMetrics();
  }, [accountId, workloadName, namespaceName, containerName, datasource, recc?.cpuRecc, recc?.memoryRecc, recc?.cpuRequest, recc?.cpuLimit]);

  const scaleOptions = {
    x: {
      grid: { display: false },
      ticks: {
        autoSkip: true,
        maxTicksLimit: 7,
      },
      title: {
        display: true,
        text: 'Minutes since pod creation',
        font: { size: 11 },
        color: '#2a3d5a',
      },
    },
  };

  if (infoMessage) {
    return (
      <Box sx={{ mt: ds.space[5] }}>
        <Typography fontSize={ds.text.bodyLg} fontWeight={600} color={ds.brand[500]} sx={{ mb: ds.space[2] }}>
          Startup Metrics (first {STARTUP_WINDOW_MINUTES} min after pod creation)
        </Typography>
        <Alert severity='info'>{infoMessage}</Alert>
      </Box>
    );
  }

  if (!isLoading && cpuDatasets.length === 0) {
    return null;
  }

  return (
    <Box sx={{ mt: ds.space[5] }}>
      <Typography fontSize={ds.text.bodyLg} fontWeight={600} color={ds.brand[500]} sx={{ mb: ds.space[1] }}>
        Startup Metrics — {podCount} pod{podCount !== 1 ? 's' : ''} (first {STARTUP_WINDOW_MINUTES} min after creation)
      </Typography>
      {podDetailsSummary && (
        <Typography fontSize={ds.text.small} color={ds.gray[600]} sx={{ mb: ds.space[2] }}>
          {podDetailsSummary}
        </Typography>
      )}
      <Grid container spacing={ds.space[5]}>
        <Grid item xs={6}>
          <Box
            sx={{
              margin: 'var(--ds-space-2) 0',
              padding: 'var(--ds-space-6) var(--ds-space-5) var(--ds-space-6)',
              display: 'flex',
              flexDirection: 'column',
              borderRadius: 'var(--ds-radius-lg)',
              border: `1px solid ${ds.brand[200]}`,
              background: ds.background[200],
            }}
          >
            <Typography fontSize={ds.text.bodyLg} fontWeight={600} color={ds.brand[500]}>
              Startup CPU (Core)
            </Typography>
            <LineChart dataset={cpuDatasets} labels={labels} scaleOptions={scaleOptions} loading={isLoading} />
          </Box>
        </Grid>
        <Grid item xs={6}>
          <Box
            sx={{
              margin: 'var(--ds-space-2) 0',
              padding: 'var(--ds-space-6) var(--ds-space-5) var(--ds-space-6)',
              display: 'flex',
              flexDirection: 'column',
              borderRadius: 'var(--ds-radius-lg)',
              border: `1px solid ${ds.brand[200]}`,
              background: ds.background[200],
            }}
          >
            <Typography fontSize={ds.text.bodyLg} fontWeight={600} color={ds.brand[500]}>
              Startup Memory (MB)
            </Typography>
            <LineChart dataset={memDatasets} labels={labels} scaleOptions={scaleOptions} loading={isLoading} />
          </Box>
        </Grid>
      </Grid>
    </Box>
  );
};

KubernetesStartupCharts.propTypes = {
  accountId: PropTypes.string.isRequired,
  workloadName: PropTypes.string,
  namespaceName: PropTypes.string,
  containerName: PropTypes.string,
  recc: PropTypes.any,
  datasource: PropTypes.string,
};

export default KubernetesStartupCharts;

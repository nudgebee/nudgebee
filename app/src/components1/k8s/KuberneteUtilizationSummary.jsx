import { Box, Grid, Typography } from '@mui/material';
import React, { useEffect, useState, useRef, useCallback } from 'react';
import Chart from '@components1/ds/Chart';
import DSCard from '@components1/ds/Card';
import ChartSwitcher from '@common/ChartSwitcher';
import { formatNumber, formatMemory } from '@lib/formatter';
import { getDateStringFromDateUnit, getLast30Days } from '@lib/datetime';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import DownloadButton from '@common-new/DownloadButton';
import HeadingWithBorder from '@common-new/HeadingWithBorder';
import kuberneteApi from '@api1/kubernetes';
import PropTypes from 'prop-types';

const BorderedBox = ({ children }) => {
  return (
    <Box
      sx={{
        border: `0.5px solid ${'var(--ds-gray-200)'}`,
        padding: '20px 27px',
        borderRadius: '6px',
        bgcolor: 'var(--ds-background-100)',
        display: 'grid',
        gridTemplateColumns: '0.6fr 1fr 1fr 0.3fr',
        gap: '8px',
        alignItems: 'center',
        boxShadow: '0px 2px 7px 0px #EFF6FF, 0px 4px 6px -1px #E5E5E599',
      }}
    >
      {children}
    </Box>
  );
};

BorderedBox.propTypes = {
  children: PropTypes.node,
};

const ValueWithLeftBorder = ({ title, value = 0, unit = 'CPU', borderColor = 'var(--ds-blue-500)' }) => {
  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'center',
        borderLeft: `3px solid ${borderColor}`,
        minHeight: '45px',
        pl: '20px',
      }}
    >
      <Typography color={'var(--ds-gray-600)'} fontSize={'var(--ds-text-small)'} fontWeight={'var(--ds-font-weight-regular)'} mb={'5px'}>
        {title}
      </Typography>
      <Typography color={'var(--ds-gray-700)'} fontSize={'var(--ds-text-heading)'} fontWeight={'var(--ds-font-weight-medium)'} lineHeight={'24px'}>
        {value}
        <span
          style={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-regular)', marginLeft: '5px' }}
        >
          {unit}
        </span>
      </Typography>
    </Box>
  );
};
ValueWithLeftBorder.propTypes = {
  title: PropTypes.string,
  value: PropTypes.number,
  unit: PropTypes.string,
  borderColor: PropTypes.string,
};

const GraphSections = ({ accountId, heading = '', id = 'KuberneteUtilizationSummary' }) => {
  const cpuUtilizationChartId = 'kubernetesCpuUtilizationChartId';
  const memoryUtilizationChartId = 'kubernetesMemoryUtilizationChartId';
  const networkIngressChartId = 'kubernetesNetworkIngressChartId';
  const networkEgressChartId = 'kubernetesNetworkEgressChartId';
  const cpuMemoryRef = useRef();
  const ingressEgressRef = useRef();

  const [chartUnit, setChartUnit] = useState('Day');
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: getLast30Days().getTime(),
    endDate: new Date().getTime(),
  });
  const [displayBarChart, setDisplayBarChart] = useState(true);
  const [showCpuMemoryLoading, setShowCpuMemoryLoading] = useState(false);
  const [showIngressEgressLoading, setShowIngressEgress] = useState(false);
  const [cpuLinechartData, setCpuLinechartData] = useState({ data: [], label: [], chartLabel: [] });
  const [memLinechartData, setMemLinechartData] = useState({ data: [], label: [], chartLabel: [] });
  const [ingressLinechartData, setIngressLinechartData] = useState({ data: [], label: [], chartLabel: [] });
  const [egressLinechartData, setEgressLinechartData] = useState({ data: [], label: [], chartLabel: [] });
  const [clusterData, setClusterData] = useState([]);
  const [networkData, setNetworkData] = useState([]);
  const [isCPUMemoryElementVisible, setIsCPUMemoryElementVisible] = useState(false);
  const [isIngressEgressElementVisible, setIsIngressEgressElementVisible] = useState(false);
  const [shouldFetchCPUMemory, setShouldFetchCPUMemory] = useState(true);
  const [shouldFetchIngressEgress, setShouldFetchIngressEgress] = useState(true);

  useEffect(() => {
    const cpuMemoryObserver = new IntersectionObserver((entries) => {
      const entry = entries[0];
      setIsCPUMemoryElementVisible(entry.isIntersecting);
    });
    const ingressEgressObserver = new IntersectionObserver((entries) => {
      const entry = entries[0];
      setIsIngressEgressElementVisible(entry.isIntersecting);
    });
    if (cpuMemoryRef.current) {
      cpuMemoryObserver.observe(cpuMemoryRef.current);
    }
    if (ingressEgressRef.current) {
      ingressEgressObserver.observe(ingressEgressRef.current);
    }
    return () => {
      cpuMemoryObserver.disconnect();
      ingressEgressObserver.disconnect();
    };
  }, []);

  const fetchCPUMemoryData = useCallback(() => {
    if (!accountId || !shouldFetchCPUMemory) {
      return;
    }
    setShowCpuMemoryLoading(true);
    kuberneteApi
      .getClusterMetrices2({
        accountId: accountId,
        metric: ['memory', 'cpu'],
        startDate: new Date(selectedDateRange.startDate),
        endDate: new Date(selectedDateRange.endDate),
        dateUnit: chartUnit,
      })
      .then((res) => {
        setClusterData(res.data?.cloud_resource_metrics_groupings);
      })
      .finally(() => {
        setShowCpuMemoryLoading(false);
        setShouldFetchCPUMemory(false);
      });
  }, [accountId, selectedDateRange, chartUnit, shouldFetchCPUMemory]);

  const fetchIngressEgressData = useCallback(() => {
    if (!accountId || !shouldFetchIngressEgress) {
      return;
    }
    setShowIngressEgress(true);
    kuberneteApi
      .getClusterMetrices2({
        accountId: accountId,
        metric: ['networkTransferBytes', 'networkReceiveBytes'],
        startDate: new Date(selectedDateRange.startDate),
        endDate: new Date(selectedDateRange.endDate),
        dateUnit: chartUnit,
      })
      .then((res) => {
        setNetworkData(res?.data?.cloud_resource_metrics_groupings);
      })
      .finally(() => {
        setShowIngressEgress(false);
        setShouldFetchIngressEgress(false);
      });
  }, [accountId, selectedDateRange, chartUnit, shouldFetchIngressEgress]);

  useEffect(() => {
    if (isCPUMemoryElementVisible) {
      fetchCPUMemoryData();
    }
  }, [isCPUMemoryElementVisible, fetchCPUMemoryData]);

  useEffect(() => {
    if (isIngressEgressElementVisible) {
      fetchIngressEgressData();
    }
  }, [isIngressEgressElementVisible, fetchIngressEgressData]);

  useEffect(() => {
    setShouldFetchCPUMemory(true);
    setShouldFetchIngressEgress(true);
  }, [selectedDateRange, chartUnit]);

  useEffect(() => {
    if (!clusterData) {
      return;
    }

    const cpuLinechartData = {
      data: [clusterData?.map((item) => formatNumber(item.avg_cpu_used_node)), clusterData?.map((item) => formatNumber(item.total_cpu_allocatable))],
      label: clusterData?.map((item) => getDateStringFromDateUnit(item.timestamp, chartUnit)) || [],
      chartLabel: ['Avg CPU', 'Allocatable CPU'],
    };
    setCpuLinechartData(cpuLinechartData);

    const memLinechartData = {
      data: [
        clusterData?.map((item) => formatMemory(item.avg_memory_used_node, 'bytes', 'gb', false)),
        clusterData?.map((item) => formatMemory(item.total_memory_allocatable, 'bytes', 'gb', false)),
      ],
      label: clusterData?.map((item) => getDateStringFromDateUnit(item.timestamp, chartUnit)) || [],
      chartLabel: ['Avg Mem', 'Allocatable Mem'],
    };
    setMemLinechartData(memLinechartData);
  }, [clusterData, chartUnit]);

  useEffect(() => {
    if (!networkData) {
      return;
    }

    const ingressLinechartData = {
      data: networkData?.filter((i) => i.metric == 'networkTransferBytes')?.map((item) => item.avg_value / (1024 * 1024 * 1024)) || [],
      label:
        networkData?.filter((i) => i.metric == 'networkTransferBytes')?.map((item) => getDateStringFromDateUnit(item.timestamp, chartUnit)) || [],
      chartLabel: ['Ingress'],
    };
    setIngressLinechartData(ingressLinechartData);

    const egressLinechartData = {
      data: networkData?.filter((i) => i.metric == 'networkReceiveBytes')?.map((item) => item.avg_value / (1024 * 1024 * 1024)) || [],
      label: networkData?.filter((i) => i.metric == 'networkReceiveBytes')?.map((item) => getDateStringFromDateUnit(item.timestamp, chartUnit)) || [],
      chartLabel: ['Egress'],
    };
    setEgressLinechartData(egressLinechartData);
  }, [networkData, chartUnit]);

  const handleDateRangeChange = (passedSelectedDateTime) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  return (
    <ListingLayout id={id}>
      <ListingLayout.Toolbar
        actions={
          <>
            <FilterDropdown
              label='Frequency'
              options={['Day', 'Week', 'Month'].map((o) => ({ value: o, label: o }))}
              value={chartUnit}
              onSelect={(e) => setChartUnit(e?.target?.value)}
            />
            <ChartSwitcher
              isBarChart={displayBarChart}
              leftButtonClick={() => setDisplayBarChart(false)}
              rightButtonClick={() => setDisplayBarChart(true)}
            />
            <CustomDateTimeRangePicker
              onChange={({ selection }) => handleDateRangeChange(selection)}
              passedSelectedDateTime={{
                startTime: selectedDateRange.startDate,
                endTime: selectedDateRange.endDate,
              }}
              minDate={new Date(new Date().getFullYear(), new Date().getMonth() - 1, 1)}
            />
            <DownloadButton
              onClick={async () => ({
                canvasId: [cpuUtilizationChartId, memoryUtilizationChartId, networkIngressChartId, networkEgressChartId],
              })}
            />
          </>
        }
      >
        <HeadingWithBorder
          value={heading}
          borderColor='var(--ds-blue-500)'
          borderWidth='3px'
          sx={{ '& p': { fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' } }}
        />
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        <Box mt={2} />
        <Grid container spacing={3}>
          <Grid item md={6} ref={cpuMemoryRef}>
            <DSCard variant='outlined' size='md'>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: 'var(--ds-gray-700)',
                  mb: 'var(--ds-space-4)',
                }}
              >
                CPU
              </Typography>
              {displayBarChart ? (
                <Chart.Bar
                  id={cpuUtilizationChartId}
                  data={cpuLinechartData.data}
                  labels={cpuLinechartData.label}
                  chartLabel={cpuLinechartData.chartLabel}
                  loading={showCpuMemoryLoading}
                />
              ) : (
                <Chart.Line
                  id={cpuUtilizationChartId}
                  data={cpuLinechartData.data}
                  labels={cpuLinechartData.label}
                  chartLabel={cpuLinechartData.chartLabel}
                  loading={showCpuMemoryLoading}
                />
              )}
            </DSCard>
          </Grid>
          <Grid item md={6} ref={cpuMemoryRef}>
            <DSCard variant='outlined' size='md'>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: 'var(--ds-gray-700)',
                  mb: 'var(--ds-space-4)',
                }}
              >
                Memory (GB)
              </Typography>
              {displayBarChart ? (
                <Chart.Bar
                  id={memoryUtilizationChartId}
                  data={memLinechartData.data}
                  labels={memLinechartData.label}
                  chartLabel={memLinechartData.chartLabel}
                  loading={showCpuMemoryLoading}
                />
              ) : (
                <Chart.Line
                  id={memoryUtilizationChartId}
                  data={memLinechartData.data}
                  labels={memLinechartData.label}
                  chartLabel={memLinechartData.chartLabel}
                  loading={showCpuMemoryLoading}
                />
              )}
            </DSCard>
          </Grid>
          <Grid item md={6} ref={ingressEgressRef}>
            <DSCard variant='outlined' size='md'>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: 'var(--ds-gray-700)',
                  mb: 'var(--ds-space-4)',
                }}
              >
                Network Ingress (GB)
              </Typography>
              {displayBarChart ? (
                <Chart.Bar
                  id={networkIngressChartId}
                  data={ingressLinechartData.data}
                  labels={ingressLinechartData.label}
                  chartLabel={ingressLinechartData.chartLabel}
                  loading={showIngressEgressLoading}
                />
              ) : (
                <Chart.Line
                  id={networkIngressChartId}
                  data={ingressLinechartData.data}
                  labels={ingressLinechartData.label}
                  chartLabel={ingressLinechartData.chartLabel}
                  loading={showIngressEgressLoading}
                />
              )}
            </DSCard>
          </Grid>
          <Grid item md={6} ref={ingressEgressRef}>
            <DSCard variant='outlined' size='md'>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: 'var(--ds-gray-700)',
                  mb: 'var(--ds-space-4)',
                }}
              >
                Network Egress (GB)
              </Typography>
              {displayBarChart ? (
                <Chart.Bar
                  id={networkEgressChartId}
                  data={egressLinechartData.data}
                  labels={egressLinechartData.label}
                  chartLabel={egressLinechartData.chartLabel}
                  loading={showIngressEgressLoading}
                />
              ) : (
                <Chart.Line
                  id={networkEgressChartId}
                  data={egressLinechartData.data}
                  labels={egressLinechartData.label}
                  chartLabel={egressLinechartData.chartLabel}
                  loading={showIngressEgressLoading}
                />
              )}
            </DSCard>
          </Grid>
        </Grid>
      </ListingLayout.Body>
    </ListingLayout>
  );
};
GraphSections.propTypes = {
  accountId: PropTypes.string,
  heading: PropTypes.string,
  id: PropTypes.string,
};

const KuberneteUtilizationSummary = ({ accountId = null, heading = '', id = 'KuberneteUtilizationSummary' }) => {
  return (
    <Box sx={{ mb: '4px' }}>
      <GraphSections accountId={accountId} heading={heading} id={id} />
    </Box>
  );
};

KuberneteUtilizationSummary.propTypes = {
  accountId: PropTypes.string,
  heading: PropTypes.string,
  id: PropTypes.string,
};

export default KuberneteUtilizationSummary;

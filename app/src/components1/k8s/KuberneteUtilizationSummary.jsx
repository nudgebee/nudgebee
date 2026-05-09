import { Box, Grid, Typography } from '@mui/material';
import React, { useEffect, useState, useRef, useCallback } from 'react';
import Title from '@common/Title';
import BarChart from '@components1/common/charts/BarChart';
import ChartSwitcher from '@common/ChartSwitcher';
import LineChart from '@common/charts/LineCharts';
import { formatNumber, formatMemory } from '@lib/formatter';
import { getDateStringFromDateUnit, getLast30Days } from '@lib/datetime';
import BoxLayout2 from '@components1/common/BoxLayout2';
import kuberneteApi from '@api1/kubernetes';
import { SummaryBlock } from './KubernetesClusterSummary';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';

const BorderedBox = ({ children }) => {
  return (
    <Box
      sx={{
        border: `0.5px solid ${colors.border.tertiaryBorder}`,
        padding: '20px 27px',
        borderRadius: '6px',
        bgcolor: colors.background.white,
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

const ValueWithLeftBorder = ({ title, value = 0, unit = 'CPU', borderColor = colors.border.valueWithLeftBorder }) => {
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
      <Typography color={colors.text.secondaryDark} fontSize={'12px'} fontWeight={400} mb={'5px'}>
        {title}
      </Typography>
      <Typography color={colors.text.secondary} fontSize={'20px'} fontWeight={500} lineHeight={'24px'}>
        {value}
        <span style={{ color: colors.text.secondaryDark, fontSize: '14px', fontWeight: 400, marginLeft: '5px' }}>{unit}</span>
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

const GraphSections = ({ accountId }) => {
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
    <BoxLayout2
      id='graph-section'
      dateTimeRange={{
        enabled: true,
        onChange: handleDateRangeChange,
        passedSelectedDateTime: {
          startTime: selectedDateRange.startDate,
          endTime: selectedDateRange.endDate,
        },
      }}
      minDate={new Date(new Date().getFullYear(), new Date().getMonth() - 1, 1)}
      sharingOptions={{
        enabled: true,
        download: {
          enabled: true,
          onClick: async () => {
            return {
              canvasId: [cpuUtilizationChartId, memoryUtilizationChartId, networkIngressChartId, networkEgressChartId],
            };
          },
        },
        sharing: {
          enabled: true,
        },
      }}
      showFiltersOnRightSide={{
        enabled: true,
        label: 'Frequency',
        options: ['Day', 'Week', 'Month'],
        showAll: false,
        value: chartUnit,
        onSelect: function (e, _rule) {
          setChartUnit(e?.target?.value);
        },
      }}
      extraOptions={[
        <ChartSwitcher
          key={1}
          isBarChart={displayBarChart}
          leftButtonClick={() => setDisplayBarChart(false)}
          rightButtonClick={() => setDisplayBarChart(true)}
        />,
      ]}
    >
      <Box mt={2} />
      <Grid container spacing={3}>
        <Grid item md={6} ref={cpuMemoryRef}>
          <SummaryBlock
            hideTitle
            sx={{
              backgroundColor: colors.background.white,
              padding: '16px 28px',
              borderRadius: '6px',
              border: `1px solid ${colors.border.vertical} !important`,
            }}
          >
            <Title
              title={'CPU'}
              sx={{
                fontSize: '12px',
                fontWeight: 500,
                color: colors.text.secondary,
              }}
              mt={'24px'}
              mb={'16px'}
              isUnderline={false}
            />
            {displayBarChart ? (
              <BarChart
                id={cpuUtilizationChartId}
                data={cpuLinechartData.data}
                labels={cpuLinechartData.label}
                chartLabel={cpuLinechartData.chartLabel}
                loading={showCpuMemoryLoading}
              />
            ) : (
              <LineChart
                id={cpuUtilizationChartId}
                data={cpuLinechartData.data}
                labels={cpuLinechartData.label}
                chartLabel={cpuLinechartData.chartLabel}
                loading={showCpuMemoryLoading}
              />
            )}
          </SummaryBlock>
        </Grid>
        <Grid item md={6} ref={cpuMemoryRef}>
          <SummaryBlock
            hideTitle
            sx={{
              backgroundColor: colors.background.white,
              padding: '16px 28px',
              borderRadius: '6px',
              border: `1px solid ${colors.border.vertical} !important`,
            }}
          >
            <Title
              title={'Memory (GB)'}
              sx={{
                fontSize: '12px',
                fontWeight: 500,
                color: colors.text.secondary,
              }}
              mt={'24px'}
              mb={'16px'}
              isUnderline={false}
            />
            {displayBarChart ? (
              <BarChart
                id={memoryUtilizationChartId}
                data={memLinechartData.data}
                labels={memLinechartData.label}
                chartLabel={memLinechartData.chartLabel}
                loading={showCpuMemoryLoading}
              />
            ) : (
              <LineChart
                id={memoryUtilizationChartId}
                data={memLinechartData.data}
                labels={memLinechartData.label}
                chartLabel={memLinechartData.chartLabel}
                loading={showCpuMemoryLoading}
              />
            )}
          </SummaryBlock>
        </Grid>
        <Grid item md={6} ref={ingressEgressRef}>
          <SummaryBlock
            hideTitle
            sx={{
              backgroundColor: colors.background.white,
              padding: '16px 28px',
              borderRadius: '6px',
              border: `1px solid ${colors.border.vertical} !important`,
            }}
          >
            <Title
              title={'Network Ingress (GB)'}
              sx={{
                fontSize: '12px',
                fontWeight: 500,
                color: colors.text.secondary,
              }}
              mt={'24px'}
              mb={'16px'}
              isUnderline={false}
            />
            {displayBarChart ? (
              <BarChart
                id={networkIngressChartId}
                data={ingressLinechartData.data}
                labels={ingressLinechartData.label}
                chartLabel={ingressLinechartData.chartLabel}
                loading={showIngressEgressLoading}
              />
            ) : (
              <LineChart
                id={networkIngressChartId}
                data={ingressLinechartData.data}
                labels={ingressLinechartData.label}
                chartLabel={ingressLinechartData.chartLabel}
                loading={showIngressEgressLoading}
              />
            )}
          </SummaryBlock>
        </Grid>
        <Grid item md={6} ref={ingressEgressRef}>
          <SummaryBlock
            hideTitle
            sx={{
              backgroundColor: colors.background.white,
              padding: '16px 28px',
              borderRadius: '6px',
              border: `1px solid ${colors.border.vertical} !important`,
            }}
          >
            <Title
              title={'Network Egress (GB)'}
              sx={{
                fontSize: '12px',
                fontWeight: 500,
                color: colors.text.secondary,
              }}
              mt={'24px'}
              mb={'16px'}
              isUnderline={false}
            />
            {displayBarChart ? (
              <BarChart
                id={networkEgressChartId}
                data={egressLinechartData.data}
                labels={egressLinechartData.label}
                chartLabel={egressLinechartData.chartLabel}
                loading={showIngressEgressLoading}
              />
            ) : (
              <LineChart
                id={networkEgressChartId}
                data={egressLinechartData.data}
                labels={egressLinechartData.label}
                chartLabel={egressLinechartData.chartLabel}
                loading={showIngressEgressLoading}
              />
            )}
          </SummaryBlock>
        </Grid>
      </Grid>
    </BoxLayout2>
  );
};
GraphSections.propTypes = {
  accountId: PropTypes.string,
};

const KuberneteUtilizationSummary = ({ accountId = null }) => {
  return (
    <Box sx={{ mb: '4px' }}>
      <GraphSections accountId={accountId} />
    </Box>
  );
};

KuberneteUtilizationSummary.propTypes = {
  accountId: PropTypes.string,
};

export default KuberneteUtilizationSummary;

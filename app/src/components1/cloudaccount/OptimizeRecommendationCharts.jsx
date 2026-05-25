import { Box } from '@mui/material';
import React, { useEffect, useState } from 'react';
import Title from '@common/Title';
import BarChart from '@components1/common/charts/BarChart';
import ChartSwitcher from '@common/ChartSwitcher';
import LineChart from '@common/charts/LineCharts';
import { formatNumber, formatMemory } from '@lib/formatter';
import { getDateStringFromDateUnit, getLastSixMonths } from '@lib/datetime';
import BoxLayout2 from '@components1/common/BoxLayout2';
import kuberneteApi from '@api1/kubernetes';
import { colors } from 'src/utils/colors';

const GraphSections = ({ accountId }) => {
  const [chartUnit, setChartUnit] = useState('Month');
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: getLastSixMonths().getTime(),
    endDate: new Date().getTime(),
  });

  const cpuUtilizationChartId = 'kubernetesCpuUtilizationChartId';
  const memoryUtilizationChartId = 'kubernetesMemoryUtilizationChartId';
  const networkIngressChartId = 'kubernetesNetworkIngressChartId';
  const networkEgressChartId = 'kubernetesNetworkEgressChartId';

  const [displayBarChart, setDisplayBarChart] = useState(true);
  const [cpuLinechartData, setCpuLinechartData] = useState({ data: [], label: [], chartLabel: [] });
  const [memLinechartData, setMemLinechartData] = useState({ data: [], label: [], chartLabel: [] });
  const [ingressLinechartData, setIngressLinechartData] = useState({ data: [], label: [], chartLabel: [] });
  const [egressLinechartData, setEgressLinechartData] = useState({ data: [], label: [], chartLabel: [] });

  const [clusterData, setClusterData] = useState([]);
  const [networkData, setNetworkData] = useState([]);

  useEffect(() => {
    if (!accountId) {
      return;
    }
    kuberneteApi
      .getk8ClusterTrendData(accountId, new Date(selectedDateRange.startDate), new Date(selectedDateRange.endDate), chartUnit)
      .then((res) => {
        setClusterData(res.data.cloudaccount_k8s_aggregate);
      });
    kuberneteApi
      .getMetrices({
        accountId: accountId,
        metric: ['networkTransferBytes', 'networkReceiveBytes'],
        groupBy: ['tenant_id', 'account_id', 'timestamp', 'metric'],
        startDate: new Date(selectedDateRange.startDate),
        endDate: new Date(selectedDateRange.endDate),
        dateUnit: chartUnit,
      })
      .then((res) => {
        setNetworkData(res?.data?.cloud_resource_metrics_groupings);
      });
  }, [accountId, selectedDateRange.startDate, selectedDateRange.endDate, chartUnit]);

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
      data: networkData?.filter((i) => i.metric === 'networkTransferBytes')?.map((item) => formatMemory(item.avg_value, 'bytes', 'gb', false)) || [],
      label:
        networkData?.filter((i) => i.metric === 'networkTransferBytes')?.map((item) => getDateStringFromDateUnit(item.timestamp, chartUnit)) || [],
      chartLabel: ['Ingress (GB)'],
    };
    setIngressLinechartData(ingressLinechartData);

    const egressLinechartData = {
      data: networkData?.filter((i) => i.metric === 'networkReceiveBytes')?.map((item) => formatMemory(item.avg_value, 'bytes', 'gb', false)) || [],
      label:
        networkData?.filter((i) => i.metric === 'networkReceiveBytes')?.map((item) => getDateStringFromDateUnit(item.timestamp, chartUnit)) || [],
      chartLabel: ['Egress (GB)'],
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
      minDate={new Date(new Date().getFullYear(), new Date().getMonth() - 6, 1)}
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
      filterOptions={[
        {
          type: 'dropdown',
          label: 'Frequency',
          options: ['Day', 'Week', 'Month'],
          showAll: false,
          value: chartUnit,
          onSelect: function (e) {
            setChartUnit(e?.target?.value);
          },
        },
        {
          type: 'custom',
          component: (
            <ChartSwitcher
              isBarChart={displayBarChart}
              leftButtonClick={() => setDisplayBarChart(false)}
              rightButtonClick={() => setDisplayBarChart(true)}
            />
          ),
        },
      ]}
    >
      <Box mt={2} />

      <Box display='flex' justifyContent='space-between' mb={'16px'}>
        <Title title={'CPU utilization'} fontSize={'16px'} height={'2px'} />
      </Box>
      {displayBarChart ? (
        <BarChart id={cpuUtilizationChartId} data={cpuLinechartData.data} labels={cpuLinechartData.label} chartLabel={cpuLinechartData.chartLabel} />
      ) : (
        <LineChart
          colors={[colors.text.memoryUsage, colors.text.memoryRequested, colors.text.memoryLimit]}
          id={cpuUtilizationChartId}
          data={cpuLinechartData.data}
          labels={cpuLinechartData.label}
          chartLabel={cpuLinechartData.chartLabel}
        />
      )}
      <Title title={'Memory utilization (GB)'} fontSize={'16px'} height={'2px'} mt={'24px'} mb={'16px'} />
      {displayBarChart ? (
        <BarChart
          id={memoryUtilizationChartId}
          data={memLinechartData.data}
          labels={memLinechartData.label}
          chartLabel={memLinechartData.chartLabel}
        />
      ) : (
        <LineChart
          id={memoryUtilizationChartId}
          colors={[colors.text.memoryUsage, colors.text.memoryRequested, colors.text.memoryLimit]}
          data={memLinechartData.data}
          labels={memLinechartData.label}
          chartLabel={memLinechartData.chartLabel}
        />
      )}
      <Title title={'Network Ingress (GB)'} fontSize={'16px'} height={'2px'} mt={'24px'} mb={'16px'} />
      {displayBarChart ? (
        <BarChart
          id={networkIngressChartId}
          data={ingressLinechartData.data}
          labels={ingressLinechartData.label}
          chartLabel={ingressLinechartData.chartLabel}
        />
      ) : (
        <LineChart
          id={networkIngressChartId}
          data={ingressLinechartData.data}
          labels={ingressLinechartData.label}
          chartLabel={ingressLinechartData.chartLabel}
        />
      )}
      <Title title={'Network Egress (GB)'} fontSize={'16px'} height={'2px'} mt={'24px'} mb={'16px'} />
      {displayBarChart ? (
        <BarChart
          id={networkEgressChartId}
          data={egressLinechartData.data}
          labels={egressLinechartData.label}
          chartLabel={egressLinechartData.chartLabel}
        />
      ) : (
        <LineChart
          id={networkEgressChartId}
          data={egressLinechartData.data}
          labels={egressLinechartData.label}
          chartLabel={egressLinechartData.chartLabel}
        />
      )}
    </BoxLayout2>
  );
};

const OptimizeUtilizationSummary = ({ accountId = null, _clusterSummary = {} }) => {
  return (
    <Box sx={{ px: '34px', mb: '4px' }}>
      <GraphSections accountId={accountId} />
    </Box>
  );
};

export default OptimizeUtilizationSummary;

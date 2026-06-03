import { Box } from '@mui/material';
import React, { useEffect, useState } from 'react';
import Title from '@common/Title';
import BarChart from '@components1/common/charts/BarChart';
import ChartSwitcher from '@common/ChartSwitcher';
import LineChart from '@common/charts/LineCharts';
import { formatNumber, formatMemory } from '@lib/formatter';
import { getDateStringFromDateUnit, getLastSixMonths } from '@lib/datetime';
import kuberneteApi from '@api1/kubernetes';
import { ds } from 'src/utils/colors';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import DownloadButton from '@common-new/DownloadButton';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import dayjs from 'dayjs';

const FREQUENCY_OPTIONS = [
  { label: 'Day', value: 'Day' },
  { label: 'Week', value: 'Week' },
  { label: 'Month', value: 'Month' },
];

const findOption = (options, value) => (value ? options?.find((o) => o.value === value) ?? null : null);

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

  const minDate = dayjs(new Date(new Date().getFullYear(), new Date().getMonth() - 6, 1));

  return (
    <ListingLayout id='graph-section'>
      <ListingLayout.Toolbar
        actions={
          <>
            <ChartSwitcher
              isBarChart={displayBarChart}
              leftButtonClick={() => setDisplayBarChart(false)}
              rightButtonClick={() => setDisplayBarChart(true)}
            />
            <CustomDateTimeRangePicker
              passedSelectedDateTime={{
                startTime: selectedDateRange.startDate,
                endTime: selectedDateRange.endDate,
                shortcutClickTime: 0,
              }}
              onChange={(result) => {
                const val = result?.selection ?? result;
                if (val) handleDateRangeChange(val);
              }}
              minDate={minDate}
            />
            <DownloadButton
              id='graph-section-download'
              onClick={async () => {
                return {
                  canvasId: [cpuUtilizationChartId, memoryUtilizationChartId, networkIngressChartId, networkEgressChartId],
                };
              }}
            />
          </>
        }
      >
        <FilterDropdown
          label='Frequency'
          options={FREQUENCY_OPTIONS}
          value={findOption(FREQUENCY_OPTIONS, chartUnit)}
          onSelect={function (e) {
            setChartUnit(e?.target?.value);
          }}
        />
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        <Box mt={ds.space[4]} />

        <Box display='flex' justifyContent='space-between' mb={ds.space[4]}>
          <Title title={'CPU utilization'} fontSize={ds.text.title} height={ds.space[0]} />
        </Box>
        {displayBarChart ? (
          <BarChart
            id={cpuUtilizationChartId}
            data={cpuLinechartData.data}
            labels={cpuLinechartData.label}
            chartLabel={cpuLinechartData.chartLabel}
          />
        ) : (
          <LineChart
            colors={['#ffa500', '#3b82f6', '#ef4444']}
            id={cpuUtilizationChartId}
            data={cpuLinechartData.data}
            labels={cpuLinechartData.label}
            chartLabel={cpuLinechartData.chartLabel}
          />
        )}
        <Title title={'Memory utilization (GB)'} fontSize={ds.text.title} height={ds.space[0]} mt={ds.space[5]} mb={ds.space[4]} />
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
            colors={['#ffa500', '#3b82f6', '#ef4444']}
            data={memLinechartData.data}
            labels={memLinechartData.label}
            chartLabel={memLinechartData.chartLabel}
          />
        )}
        <Title title={'Network Ingress (GB)'} fontSize={ds.text.title} height={ds.space[0]} mt={ds.space[5]} mb={ds.space[4]} />
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
        <Title title={'Network Egress (GB)'} fontSize={ds.text.title} height={ds.space[0]} mt={ds.space[5]} mb={ds.space[4]} />
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
      </ListingLayout.Body>
    </ListingLayout>
  );
};

const OptimizeUtilizationSummary = ({ accountId = null, _clusterSummary = {} }) => {
  return (
    <Box sx={{ px: 'var(--ds-space-6)', mb: 'var(--ds-space-1)' }}>
      <GraphSections accountId={accountId} />
    </Box>
  );
};

export default OptimizeUtilizationSummary;

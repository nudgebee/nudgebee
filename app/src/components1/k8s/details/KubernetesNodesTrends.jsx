import React, { useState, useEffect } from 'react';
import { ListingLayout } from '@components1/ds/ListingLayout';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import DownloadButton from '@common-new/DownloadButton';
import PropTypes from 'prop-types';
import Title from '@components1/common/Title';
import { unique } from '@lib/collections';
import LineChart from '@components1/common/charts/LineCharts';
import { determineAndFormatTime, getLast7Days, isWithinTimeFrame } from '@lib/datetime';
import ChartComponent from '@components1/common/charts/ChartComponent';
import observability from '@api1/observability';
import apiKubernetes1 from '@api1/kubernetes1';

export const KubernetesNodesTrends = ({ accountId, showZoneTrend = false }) => {
  const [dateRange, setDateRange] = useState({
    startDate: getLast7Days(new Date()).getTime(),
    endDate: new Date().getTime(),
  });
  const [activeNodesData, setActiveNodesData] = useState({
    data: [],
    label: [],
  });
  const [allNodesData, setAllNodesData] = useState({
    data: [],
    label: [],
  });
  const [isNodesDataLoading, setIsNodesDataLoading] = useState(false);
  const [pieCharts, setPieCharts] = useState([
    {
      name: '',
      datasets: [],
      labels: [],
    },
  ]);
  const [podsTrend, setPodsTrend] = useState({
    data: [],
    label: [],
  });
  const [nodePoolPodTrend, setNodePoolPodTrend] = useState({
    data: [],
    label: [],
  });
  const [nodeClaimsDisruptedTrend, setNodeClaimsDisruptedTrend] = useState({
    data: [],
    label: [],
  });
  const [nodeCreatedNodePool, setNodeCreatedNodePool] = useState({
    data: [],
    label: [],
  });
  const [nodeTerminatedNodePool, setNodeTerminatedNodePool] = useState({
    data: [],
    label: [],
  });
  const [nodeDisruptionDecisionsReasonDecision, setNodeDisruptionDecisionsReasonDecision] = useState({
    data: [],
    label: [],
  });
  const [nodesEligibleDisruptionReason, setNodesEligibleDisruptionReason] = useState({
    data: [],
    label: [],
  });
  const [loadingTrend, setLoadingTrend] = useState({
    zoneTrend: false,
    podsTrend: false,
    nodePoolPodTrend: false,
    nodeClaimsDisruptedTrend: false,
    nodeCreatedNodePool: false,
    nodeTerminatedNodePool: false,
    nodeDisruptionDecisionsReasonDecision: false,
    nodesEligibleDisruptionReason: false,
  });

  function updateActiveNodesData(timeToNodes) {
    let label = Object.keys(timeToNodes).sort((a, b) => a - b);
    let data = [];
    for (let l of label) {
      data.push(timeToNodes[l].length);
    }

    setActiveNodesData({
      data: data,
      label: label.map((l) => determineAndFormatTime(parseInt(l), isWithinTimeFrame(dateRange.startDate, dateRange.endDate, 24))),
    });
  }

  function updateAllNodesData(timeToNodes) {
    let label = Object.keys(timeToNodes).sort((a, b) => a - b);
    let allDatesMap = {};
    for (let l of label) {
      let d = new Date(l * 1000).toISOString().split('T')[0];
      if (!(d in allDatesMap)) {
        allDatesMap[d] = [];
      }
      allDatesMap[d].push(...timeToNodes[l]);
    }
    let allDates = Object.keys(allDatesMap).sort((a, b) => new Date(a) - new Date(b));
    let allDatesNodes = [];
    for (let l of allDates) {
      allDatesNodes.push(unique(allDatesMap[l]).length);
    }
    setAllNodesData({
      data: allDatesNodes,
      label: allDates,
    });
  }

  function getTimeToNodes(groupData) {
    let timeToNodes = {};
    let nodeDetails = {};
    groupData.forEach((l) => {
      for (let i of l.timestamps) {
        if (!(i in timeToNodes)) {
          timeToNodes[i] = [];
        }
        timeToNodes[i].push(l.metric.node);
        if (!(l.metric.node in nodeDetails)) {
          nodeDetails[l.metric.node] = l.metric;
        }
      }
    });
    return timeToNodes;
  }

  const updatePodTrend = (seriesList) => {
    if (seriesList && seriesList.length == 1) {
      const labels = seriesList[0].timestamps.map((e) =>
        determineAndFormatTime(parseInt(e), isWithinTimeFrame(dateRange.startDate, dateRange.endDate, 24))
      );
      const data = seriesList[0].values;
      setPodsTrend({
        data: data,
        label: labels,
        pointRadius: 0,
        borderWidth: 1,
      });
    }
  };

  const updateNodePoolPodTrend = (seriesList) => {
    if (seriesList && seriesList.length > 0) {
      const datasets = [];
      let labels = [];
      for (const element of seriesList) {
        labels = element.timestamps.map((e) => determineAndFormatTime(parseInt(e), isWithinTimeFrame(dateRange.startDate, dateRange.endDate, 24)));
        datasets.push({
          label: element.metric?.nodepool ?? '',
          data: element.values,
        });
      }
      setNodePoolPodTrend({
        label: labels,
        data: datasets,
      });
    }
  };

  const updateNodeClaimDisruptedTrend = (seriesList) => {
    if (seriesList && seriesList.length > 0) {
      const datasets = [];
      const labels = seriesList[0].timestamps.map((e) =>
        determineAndFormatTime(parseInt(e), isWithinTimeFrame(dateRange.startDate, dateRange.endDate, 24))
      );
      for (const element of seriesList) {
        datasets.push({
          label:
            element.metric?.nodepool && element.metric?.reason
              ? `${element.metric.nodepool} - ${element.metric.reason}`
              : element.metric?.nodepool || element.metric?.reason || '',
          data: element.values,
        });
      }
      setNodeClaimsDisruptedTrend({
        label: labels,
        data: datasets,
      });
    }
  };

  const updateNodeCreatedNodePoolTrend = (seriesList) => {
    if (seriesList && seriesList.length > 0) {
      const datasets = [];
      const labels = seriesList[0].timestamps.map((e) =>
        determineAndFormatTime(parseInt(e), isWithinTimeFrame(dateRange.startDate, dateRange.endDate, 24))
      );
      for (const element of seriesList) {
        datasets.push({
          label: element.metric?.nodepool || '',
          data: element.values,
        });
      }
      setNodeCreatedNodePool({
        label: labels,
        data: datasets,
      });
    }
  };

  const updateNodeTerminatedNodePoolTrend = (seriesList) => {
    if (seriesList && seriesList.length > 0) {
      const datasets = [];
      const labels = seriesList[0].timestamps.map((e) =>
        determineAndFormatTime(parseInt(e), isWithinTimeFrame(dateRange.startDate, dateRange.endDate, 24))
      );
      for (const element of seriesList) {
        datasets.push({
          label: element.metric?.nodepool || '',
          data: element.values,
        });
      }
      setNodeTerminatedNodePool({
        label: labels,
        data: datasets,
      });
    }
  };

  const updateNodeDisruptionDecisionsReasonDecisionTrend = (seriesList) => {
    if (seriesList && seriesList.length > 0) {
      const datasets = [];
      const labels = seriesList[0].timestamps.map((e) =>
        determineAndFormatTime(parseInt(e), isWithinTimeFrame(dateRange.startDate, dateRange.endDate, 24))
      );
      for (const element of seriesList) {
        datasets.push({
          label:
            element.metric?.decision && element.metric?.reason
              ? `${element.metric.decision} - ${element.metric.reason}`
              : element.metric?.decision || element.metric?.reason || '',
          data: element.values,
        });
      }
      setNodeDisruptionDecisionsReasonDecision({
        label: labels,
        data: datasets,
      });
    }
  };

  const updateNodesEligibleDisruptionReasonTrend = (seriesList) => {
    if (seriesList && seriesList.length > 0) {
      const datasets = [];
      const labels = seriesList[0].timestamps.map((e) =>
        determineAndFormatTime(parseInt(e), isWithinTimeFrame(dateRange.startDate, dateRange.endDate, 24))
      );
      for (const element of seriesList) {
        datasets.push({
          label: element.metric?.reason || '',
          data: element.values,
        });
      }
      setNodesEligibleDisruptionReason({
        label: labels,
        data: datasets,
      });
    }
  };

  useEffect(() => {
    const requestBody = {
      account_id: accountId,
      queries: {
        promql_query: 'kube_node_info{__CLUSTER__}',
      },
      start_time: dateRange.startDate,
      end_time: dateRange.endDate,
    };

    setIsNodesDataLoading(true);
    observability
      .metricsQuery(requestBody)
      .then((res) => {
        if (res?.data?.data?.metrics_query?.results?.[0]?.payload) {
          const groupData = res?.data?.data?.metrics_query?.results?.[0]?.payload;
          let timeToNodes = getTimeToNodes(groupData);
          updateActiveNodesData(timeToNodes);
          updateAllNodesData(timeToNodes);
          return;
        }
        setActiveNodesData({
          data: [],
          label: [],
        });
        setAllNodesData({
          data: [],
          label: [],
        });
      })
      .catch(() => {
        setActiveNodesData([]);
      })
      .finally(() => {
        setIsNodesDataLoading(false);
      });
  }, [accountId, dateRange.startDate, dateRange.endDate]);

  const resetState = () => {
    setLoadingTrend((prevTrend) => ({
      ...prevTrend,
      zoneTrend: true,
      podsTrend: true,
      nodePoolPodTrend: true,
      nodeClaimsDisruptedTrend: true,
      nodeCreatedNodePool: true,
      nodeTerminatedNodePool: true,
      nodeDisruptionDecisionsReasonDecision: true,
      nodesEligibleDisruptionReason: true,
    }));
    setPieCharts([
      {
        name: '',
        datasets: [],
        labels: [],
      },
    ]);
    setPodsTrend({
      data: [],
      label: [],
    });
  };

  useEffect(() => {
    if (!showZoneTrend) {
      return;
    }

    resetState();
    const requestBody = createRequestBody(accountId, dateRange);

    apiKubernetes1.utilisationApi(requestBody).then(handleResponse).finally(updateLoadingState);
  }, [showZoneTrend, accountId, dateRange.startDate, dateRange.endDate]);

  function createRequestBody(accountId, dateRange) {
    return {
      accountId: accountId,
      metrics: [
        'node_az',
        'pod_az',
        'no_of_pods',
        'node_pool_pod_trend',
        'nodeclaims_disrupted',
        'node_created_node_pool',
        'nodes_terminated_node_pool',
        'node_disruption_decisions_reason_decision',
        'nodes_eligible_disruption_reason',
      ],
      kind: 'node',
      startDate: dateRange.startDate,
      endDate: dateRange.endDate,
    };
  }

  function handleResponse(results) {
    if (!results?.length) {
      return;
    }

    const pieChart = createPieCharts(results);
    setPieCharts(pieChart);

    const podListSeriesResult = results.find((data) => data.query_key === 'no_of_pods')?.payload || [];
    updatePodTrend(podListSeriesResult);

    const nodePoolPodListSeriesResult = results.find((data) => data.query_key === 'node_pool_pod_trend')?.payload || [];
    updateNodePoolPodTrend(nodePoolPodListSeriesResult);

    const nodeClaimsDisruptedListSeriesResult = results.find((data) => data.query_key === 'nodeclaims_disrupted')?.payload || [];
    updateNodeClaimDisruptedTrend(nodeClaimsDisruptedListSeriesResult);

    const nodeCreatedNodePoolListSeriesResult = results.find((data) => data.query_key === 'node_created_node_pool')?.payload || [];
    updateNodeCreatedNodePoolTrend(nodeCreatedNodePoolListSeriesResult);

    const nodeTerminatedNodePoolListSeriesResult = results.find((data) => data.query_key === 'nodes_terminated_node_pool')?.payload || [];
    updateNodeTerminatedNodePoolTrend(nodeTerminatedNodePoolListSeriesResult);

    const nodeDisruptionDecisionsReasonDecisionListSeriesResult =
      results.find((data) => data.query_key === 'node_disruption_decisions_reason_decision')?.payload || [];
    updateNodeDisruptionDecisionsReasonDecisionTrend(nodeDisruptionDecisionsReasonDecisionListSeriesResult);

    const nodesEligibleDisruptionReasonListSeriesResult =
      results.find((data) => data.query_key === 'nodes_eligible_disruption_reason')?.payload || [];
    updateNodesEligibleDisruptionReasonTrend(nodesEligibleDisruptionReasonListSeriesResult);
  }

  function createPieCharts(results) {
    const pieChart = [];
    addChartData(pieChart, results.find((data) => data.query_key === 'node_az')?.payload, 'Node AZ');
    addChartData(pieChart, results.find((data) => data.query_key === 'pod_az')?.payload, 'Pod AZ');
    return pieChart;
  }

  function addChartData(pieChart, seriesResult, name) {
    if (!seriesResult || seriesResult.length === 0) {
      return;
    }

    const labels = [];
    const data = [];
    for (const element of seriesResult) {
      labels.push(element?.metric.zone);
      data.push(parseInt(element?.values.at(-1)));
    }
    pieChart.push({
      name,
      labels,
      datasets: [{ label: 'Count', data }],
    });
  }

  function updateLoadingState() {
    setLoadingTrend((prevTrend) => ({
      ...prevTrend,
      zoneTrend: false,
      podsTrend: false,
      nodePoolPodTrend: false,
      nodeClaimsDisruptedTrend: false,
      nodeCreatedNodePool: false,
      nodeTerminatedNodePool: false,
      nodeDisruptionDecisionsReasonDecision: false,
      nodesEligibleDisruptionReason: false,
    }));
  }

  const handleDateRangeChange = (passedSelectedDateTime) => {
    setDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  return (
    <ListingLayout id='node-trends'>
      <ListingLayout.Toolbar
        actions={
          <>
            <CustomDateTimeRangePicker
              passedSelectedDateTime={{
                startTime: dateRange.startDate,
                endTime: dateRange.endDate,
              }}
              onChange={({ selection }) => handleDateRangeChange(selection)}
            />
            <DownloadButton onClick={async () => ({ canvasId: ['activeNodesChart', 'allNodesChart'] })} />
          </>
        }
      />
      <ListingLayout.Body>
        <Title title='Active Nodes' fontSize={'16px'} height={'2px'} mt={4} mb={4} />
        <LineChart id='activeNodesChart' data={activeNodesData.data} labels={activeNodesData.label} chartLabel='Nodes' loading={isNodesDataLoading} />
        <Title title='All Nodes In A Day' fontSize={'16px'} height={'2px'} mt={4} mb={4} />
        <LineChart id='allNodesChart' data={allNodesData.data} labels={allNodesData.label} chartLabel='Nodes' loading={isNodesDataLoading} />
        {showZoneTrend ? (
          <div className='node-trends-charts-container'>
            {pieCharts && pieCharts.length > 0
              ? pieCharts.map((chartData, index) => (
                  <div className='chart-wrapper' key={`node-trends-charts-container-pie-${index}`}>
                    <ChartComponent
                      key={`node-trends-charts-container-pie-${index}`}
                      type='pie'
                      data={chartData}
                      options={{
                        responsive: true,
                        plugins: {
                          legend: { position: 'top' },
                          title: { display: true, text: chartData.name },
                        },
                      }}
                      width={100}
                      height={100}
                      loading={loadingTrend.zoneTrend}
                    />
                  </div>
                ))
              : null}

            <LineChart
              key={'node-pool-pod-trend'}
              type='line'
              dataset={nodePoolPodTrend.data}
              loading={loadingTrend.nodePoolPodTrend}
              labels={nodePoolPodTrend.label}
              chartTitle='Node Pool Pod Trend'
            />

            <LineChart data={podsTrend.data} labels={podsTrend.label} chartTitle='Number Of Pods' loading={loadingTrend.podsTrend} />
            <LineChart
              dataset={nodeClaimsDisruptedTrend.data}
              labels={nodeClaimsDisruptedTrend.label}
              chartTitle='Node Claims Disrupted By Node Pool'
              loading={loadingTrend.nodeClaimsDisruptedTrend}
            />
            <LineChart
              dataset={nodeTerminatedNodePool.data}
              labels={nodeTerminatedNodePool.label}
              chartTitle='Nodes Terminated by Node Pool'
              loading={loadingTrend.nodeTerminatedNodePool}
            />
            <LineChart
              dataset={nodeDisruptionDecisionsReasonDecision.data}
              labels={nodeDisruptionDecisionsReasonDecision.label}
              chartTitle='Node Disruption Decisions by Reason and Decision'
              loading={loadingTrend.nodeDisruptionDecisionsReasonDecision}
            />
            <LineChart
              dataset={nodesEligibleDisruptionReason.data}
              labels={nodesEligibleDisruptionReason.label}
              chartTitle='Nodes Eligible for Disruption by Reason'
              loading={loadingTrend.nodesEligibleDisruptionReason}
            />
            <LineChart
              dataset={nodeCreatedNodePool.data}
              labels={nodeCreatedNodePool.label}
              chartTitle='Nodes Created by Node Pool'
              loading={loadingTrend.nodeCreatedNodePool}
            />
          </div>
        ) : null}
      </ListingLayout.Body>
    </ListingLayout>
  );
};

KubernetesNodesTrends.propTypes = {
  accountId: PropTypes.string.isRequired,
  showZoneTrend: PropTypes.bool.isRequired,
};

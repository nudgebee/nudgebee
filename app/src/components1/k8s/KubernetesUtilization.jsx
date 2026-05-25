import React, { useEffect, useState } from 'react';
import k8sApi from '@api1/kubernetes';
import KubernetesRecommendationCharts from './common/KubernetesRecommendationCharts';
import KubernetesStartupCharts from './common/KubernetesStartupCharts';
import PropTypes from 'prop-types';
import CustomDateTimeRangePicker from '@components1/common/widgets/CustomDateTimeRangePicker';
import { Box } from '@mui/material';

const KubernetesUtilizationCharts = ({ accountId, podName, workloadName, namespaceName, recc, containerName, datasource }) => {
  const [cpuData, setCpuData] = useState({ data: [], labels: [] });
  const [memData, setMemData] = useState({ data: [], labels: [] });
  const [isDataLoading, setIsDataLoading] = useState(false);
  const [selectedDateTime, setSelectedDateTime] = useState({
    startTime: new Date().getTime() - 7 * 24 * 60 * 60 * 1000,
    endTime: new Date().getTime(),
    shortcutClickTime: 7 * 24 * 60 * 60 * 1000,
  });

  useEffect(() => {
    // Only pass podName if it differs from workloadName (i.e., it's an actual pod name).
    // When they're equal, the API incorrectly uses kind='pod' with exact match instead of
    // kind='workload' with regex match, causing empty results for Deployments/StatefulSets.
    const effectivePodName = podName && podName !== workloadName ? podName : undefined;
    const query = {
      accountId,
      podName: effectivePodName,
      workloadName,
      namespaceName,
      containerName,
      startDate: new Date(selectedDateTime.startTime),
      endDate: new Date(selectedDateTime.endTime),
      metrics: ['cpu_usage', 'memory_usage', 'cpu_limit', 'cpu_request', 'memory_limit', 'memory_request'],
    };
    query.accountId = accountId;
    const groupBy = ['tenant_id', 'account_id', 'timestamp'];
    if (query.namespaceName) {
      groupBy.push('namespace_name');
    }
    if (query.workloadName) {
      groupBy.push('workload_name');
    }
    if (query.podName) {
      groupBy.push('pod_name');
    }
    setIsDataLoading(true);
    k8sApi
      .getK8sPodGroupings2(10, query, groupBy, datasource)
      .then((res) => {
        let cpuDataL = {
          data: [[], [], []],
          labels: [],
        };
        let memDataL = {
          data: [[], [], []],
          labels: [],
        };
        res?.data?.k8s_pod_groupings?.forEach((e) => {
          cpuDataL.data[0].push(e.avg_cpu_used);
          cpuDataL.data[1].push(e.avg_cpu_request || recc.cpuRequest || 0);
          cpuDataL.data[2].push(e.avg_cpu_limit || recc.cpuLimit || 0);

          memDataL.data[0].push(((e.avg_memory_used || 0) / (1024 * 1024)).toFixed(2));
          if (e.avg_memory_request) {
            memDataL.data[1].push(((e.avg_memory_request || 0) / (1024 * 1024)).toFixed(2));
          } else if (recc.memRequest) {
            memDataL.data[1].push(parseFloat(recc.memRequest.replaceAll(',', '')));
          } else {
            memDataL.data[1].push(0);
          }
          if (e.avg_memory_limit) {
            memDataL.data[2].push(((e.avg_memory_limit || 0) / (1024 * 1024)).toFixed(2));
          } else if (recc.memLimit) {
            memDataL.data[2].push(parseFloat(recc.memLimit.replaceAll(',', '')));
          } else {
            memDataL.data[2].push(0);
          }

          cpuDataL.labels.push(e.timestamp);
          memDataL.labels.push(e.timestamp);
        });
        setCpuData(cpuDataL);
        setMemData(memDataL);
      })
      .finally(() => {
        setIsDataLoading(false);
      });
  }, [accountId, podName, workloadName, selectedDateTime]);

  const handleDateTimeChange = ({ selection }) => {
    setSelectedDateTime(selection);
  };

  return (
    <>
      <Box style={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end' }}>
        <CustomDateTimeRangePicker passedSelectedDateTime={selectedDateTime} onChange={handleDateTimeChange} width='200px' />
      </Box>
      <KubernetesRecommendationCharts memoryData={memData} cpuData={cpuData} recc={recc} loading={isDataLoading} />
      <KubernetesStartupCharts
        accountId={accountId}
        workloadName={workloadName}
        namespaceName={namespaceName}
        containerName={containerName}
        recc={recc}
        datasource={datasource}
      />
    </>
  );
};

KubernetesUtilizationCharts.propTypes = {
  accountId: PropTypes.string.isRequired,
  podName: PropTypes.string,
  recc: PropTypes.any,
  workloadName: PropTypes.string,
  namespaceName: PropTypes.string,
  containerName: PropTypes.string,
  datasource: PropTypes.string,
};

const KubernetesUtilization = ({ account, podName, recc, workloadName, namespaceName, containerName, datasource }) => {
  return (
    <KubernetesUtilizationCharts
      accountId={account}
      podName={podName}
      workloadName={workloadName}
      namespaceName={namespaceName}
      containerName={containerName}
      recc={recc}
      datasource={datasource}
    />
  );
};

KubernetesUtilization.propTypes = {
  account: PropTypes.string.isRequired,
  podName: PropTypes.string,
  recc: PropTypes.any,
  workloadName: PropTypes.string,
  namespaceName: PropTypes.string,
  containerName: PropTypes.string,
  datasource: PropTypes.string,
};

export default KubernetesUtilization;

import { useEffect, useState } from 'react';
import k8sApi from '@api1/kubernetes';
import OptimizeRecommendationCharts from './OptimizeRecommendationCharts';
import { getDateString } from '@lib/datetime';
import PropTypes from 'prop-types';

type OptimizeUtilizationChartsType = {
  row: any;
  accountId: string;
  podName?: string;
  workloadName: string;
  namespaceName: string;
};

type OptimizeUtilizationType = {
  row: any;
  account: string;
  podName?: string;
  workloadName: string;
  namespaceName: string;
  recc: any;
};
const OptimizeUtilizationCharts = ({ row, accountId, podName, workloadName, namespaceName }: OptimizeUtilizationChartsType) => {
  const [_cpuData, _setCpuData] = useState({ data: [], labels: [] });
  const [_memData, _setMemData] = useState({ data: [], labels: [] });

  useEffect(() => {
    const query = { accountId, podName, workloadName, namespaceName };
    query.accountId = accountId;
    const groupBy: string[] = ['tenant_id', 'account_id', 'timestamp'];
    if (query.namespaceName) {
      groupBy.push('namespace_name');
    }
    if (query.workloadName) {
      groupBy.push('workload_name');
    }
    if (query.podName) {
      groupBy.push('pod_name');
    }

    k8sApi.getK8sPodGroupings(10, query, groupBy).then((res) => {
      const cpuDataL: any = {
        data: [[], []],
        labels: [],
      };
      const memDataL: any = {
        data: [[], []],
        labels: [],
      };
      res?.data?.k8s_pod_groupings?.forEach((e: any) => {
        cpuDataL.data[0].push(e.avg_cpu_used);
        memDataL.data[0].push(((e.avg_memory_used || 0) / (1024 * 1024)).toFixed(2));

        cpuDataL.data[1].push(e.avg_cpu_request || 0);
        memDataL.data[1].push(((e.avg_memory_request || 0) / (1024 * 1024)).toFixed(2));

        cpuDataL.labels.push(getDateString(e.timestamp));
        memDataL.labels.push(getDateString(e.timestamp));
      });
      _setCpuData(cpuDataL);
      _setMemData(memDataL);
    });
  }, [accountId, podName, workloadName, row]);

  return (
    <>
      <OptimizeRecommendationCharts />
    </>
  );
};

OptimizeUtilizationCharts.propTypes = {
  row: PropTypes.any,
  accountId: PropTypes.string.isRequired,
  podName: PropTypes.string,
  workloadName: PropTypes.string,
  namespaceName: PropTypes.string,
  recc: PropTypes.any,
};

const OptimizeUnutilizaionNetwork = ({ row, account, podName, recc, workloadName, namespaceName }: OptimizeUtilizationType) => {
  const kubeid = account;

  return (
    <>
      <OptimizeUtilizationCharts
        row={row}
        accountId={kubeid}
        podName={podName}
        workloadName={workloadName}
        namespaceName={namespaceName}
        recc={recc}
      />
    </>
  );
};

OptimizeUnutilizaionNetwork.propTypes = {
  row: PropTypes.any,
  account: PropTypes.string.isRequired,
  podName: PropTypes.string,
  recc: PropTypes.any,
  workloadName: PropTypes.string,
  namespaceName: PropTypes.string,
};

export default OptimizeUnutilizaionNetwork;

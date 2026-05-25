import { useEffect, useState } from 'react';
import KubernetesMonitoringCard from '@components1/k8s/common/KubernetesMonitoringCard';
import { Box, Typography } from '@mui/material';
import apiMonitoring from '@api1/monitoring';
import Loader from '@components1/common/Loader';
import apiKubernetes from '@api1/kubernetes';
import type { ApplicationStats } from 'src/utils/common';
import { getLast7Days } from '@lib/datetime';
import FilterGroup from '@components1/common/FilterGroup';
import { useData } from '@context/DataContext';
import apiHome from '@api1/home';

const KubernetesMonitoring: React.FC = () => {
  const [data, setData] = useState<ApplicationStats[]>([]);
  const [uniqueAcc, setUniqueAcc] = useState<string[]>([]);
  const [consumeRelayServer, setConsumeRelayServer] = useState<boolean>(false);
  const [loading, setLoading] = useState(false);
  const [errorMessage, setErrorMessage] = useState('');
  const [selectedDateRange, setSelectedDateRange] = useState<any>({
    startDate: getLast7Days().getTime(),
    endDate: new Date().getTime(),
  });
  const [clusterOption, setClusterOption] = useState<string[]>([]);
  const [selectedCluster, setSelectedCluster] = useState<string>('');
  const [selectedNamespace, setSelectedNamespace] = useState<string>('');
  const [nsWorkloadOptions, setNSWorkloadOptions] = useState<string[]>([]);
  const [selectedWorkload, setSelectedWorkload] = useState<string>('');
  const [allNameSpaces, setAllNameSpaces] = useState<any[]>([]);
  const { allCluster } = useData();

  const getClustersData = async () => {
    try {
      const response = await apiHome.getCloudAccounts('K8s');
      if (response && response.length > 0) {
        const clusters = response.map((item: any) => ({
          label: item.account_name,
          value: item.id,
        }));
        setClusterOption(clusters);
      } else {
        setClusterOption([]);
      }
    } catch (error) {
      console.error(error);
    }
  };

  const getAllNameSpacesData = async () => {
    try {
      const response: any = await apiKubernetes.getAllK8sNamespaces();
      setAllNameSpaces([...new Set(response?.data.map((item: any) => item.namespace_name))]);
    } catch (error) {
      console.error(error);
    }
  };

  const getAllWorkloadsData = async () => {
    try {
      const query: any = {};
      if (selectedNamespace) {
        query['namespace'] = selectedNamespace;
      }
      if (selectedCluster) {
        query['account_id'] = selectedCluster;
      }

      query['kind'] = ['Deployment', 'StatefulSet', 'Rollout'];
      query.isActive = true;
      const response: any = await apiKubernetes.getAllK8sWorkload(query);
      const uniqueNames = new Set();
      const workloadNameArray = response?.data
        .filter((item: any) => {
          if (uniqueNames.has(item.name)) {
            return false;
          }
          uniqueNames.add(item.name);
          return true;
        })
        .map((item: any) => ({
          label: item.name,
          value: item.name,
        }));
      setNSWorkloadOptions(workloadNameArray);
    } catch (error) {
      console.error(error);
    }
  };

  useEffect(() => {
    getClustersData();
    getAllNameSpacesData();
    getAllWorkloadsData();
  }, []);

  useEffect(() => {
    getAllWorkloadsData();
  }, [selectedNamespace, selectedCluster]);

  useEffect(() => {
    setErrorMessage('');
    setLoading(true);
    const query: any = {};
    if (selectedCluster) {
      query.accountId = selectedCluster;
    }
    if (selectedNamespace) {
      query.namespaceName = selectedNamespace;
    }
    if (selectedWorkload) {
      query.workloadName = selectedWorkload;
    }
    apiMonitoring
      .listMonitoringWorkload(query)
      .then((res) => {
        setLoading(false);
        const monitoring = res?.data?.data?.k8s_workloads_cloud_account_monitoring_v2?.rows;
        const error = res?.data.errors;
        if (monitoring && monitoring.length > 0) {
          const data = monitoring.map((m: any) => ({
            name: m.name,
            namespace: m.namespace,
            accountName: m.account_name,
            accountId: m.account_id,
            workloadId: m.workload_id,
            nevents: m.event_count ? atob(m.event_count) : '',
            readyPods: m.ready_pods,
            totalPods: m.total_pods,
            application_error_count: m.application_error_count ? atob(m.application_error_count) : '',
            pod_error_count: m.pod_error_count ? atob(m.pod_error_count) : '',
            total_slo_count: m.total_slo_count ? m.total_slo_count : '-',
            failed_slo_count: m.failed_slo_count ? m.failed_slo_count : '-',
          }));
          const uniqueAcc = [...new Set(data.filter((b: any) => b.accountId).map((item: any) => item.accountId))];
          setData(data);
          setUniqueAcc(uniqueAcc as string[]);
        } else if (error && error.length > 0) {
          setData([]);
          setErrorMessage('Failed to fetch Monitoring');
        } else {
          setData([]);
        }
      })
      .catch(() => {
        setLoading(false);
        setErrorMessage('Failed to fetch Monitoring');
      });
  }, [selectedDateRange.startDate, selectedDateRange.endDate, selectedCluster, selectedNamespace, selectedWorkload]);

  useEffect(() => {
    if (data == undefined || data.length == 0) {
      return;
    }
    listMonitoringWorkloadRecommendationCount();
  }, [uniqueAcc, selectedCluster, selectedNamespace, selectedWorkload, selectedDateRange]);

  useEffect(() => {
    if (data == undefined || data.length == 0 || !consumeRelayServer) {
      return;
    }
    let updatedStats = JSON.parse(JSON.stringify(data));
    const promises = [];

    let allUniqueAccounts = uniqueAcc;
    if (allCluster && allCluster.length > 0 && !selectedCluster) {
      const connectedAgentIds = allCluster.filter((b: any) => b?.agent?.status == 'CONNECTED').map((n: any) => n.value);
      allUniqueAccounts = uniqueAcc.filter((o: string) => connectedAgentIds.includes(o));
    }
    if (allUniqueAccounts && allUniqueAccounts.length > 0) {
      for (const value of allUniqueAccounts) {
        const applicationArray = data
          .filter((b: any) => b.accountId === value)
          .map((b: any) => ({
            name: b.name,
            namespace: b.namespace,
          }));
        const request = {
          no_sinks: true,
          body: {
            account_id: value,
            action_name: 'application_stats',
            action_params: { applications: JSON.stringify(applicationArray) },
          },
          cache: false,
        };

        const promise = apiKubernetes
          .relayForwardRequest(request)
          .then((res: any) => {
            const statsData = res?.data?.data || [];
            if (statsData && statsData.length > 0) {
              updatedStats = updatedStats.map((obj1: any) => {
                if (obj1.accountId === value) {
                  const matchingObj = statsData.find((obj2: any) => obj1.name === obj2.name && obj1.namespace === obj2.namespace);
                  if (matchingObj) {
                    return {
                      ...obj1,
                      cpu: matchingObj?.cpu_p99 ? (matchingObj?.cpu_p99 * 1000).toFixed(0) : '-',
                      memoryp99: matchingObj?.memory_p99,
                      nrequests: Math.round(matchingObj?.total_request_count),
                      latency: matchingObj?.latency,
                      nerrors: Math.round(matchingObj?.failure_request_count),
                      nerrorscritical: Math.round(matchingObj?.log_failure_count),
                      maxCPUReq: matchingObj?.max_cpu_request ? (matchingObj?.max_cpu_request * 1000).toFixed(0) : '-',
                      maxMemoryReq: matchingObj?.max_memory_request,
                      maxMemoryUsage: matchingObj?.memory_max,
                      max_cpu_limit: matchingObj?.max_cpu_limit ? (matchingObj?.max_cpu_limit * 1000).toFixed(0) : '-',
                      max_memory_limit: matchingObj?.max_memory_limit,
                      cpu_p50: matchingObj?.cpu_p50 ? (matchingObj?.cpu_p50 * 1000).toFixed(0) : '-',
                      memory_p50: matchingObj?.memory_p50,
                    };
                  }
                  return obj1;
                }
                return obj1;
              });
              setData(updatedStats);
            }
          })
          .catch((err) => {
            console.error('failed to fetch app monitring- ', err);
          });

        promises.push(promise);
      }
      Promise.all(promises).then(() => {
        setConsumeRelayServer(false);
      });
    } else {
      setConsumeRelayServer(false);
    }
  }, [consumeRelayServer]);

  const listMonitoringWorkloadRecommendationCount = () => {
    const query: any = {};
    if (selectedCluster) {
      query.accountId = selectedCluster;
    }
    if (selectedNamespace) {
      query.namespaceName = selectedNamespace;
    }
    if (selectedWorkload) {
      query.workloadName = selectedWorkload;
    }
    apiMonitoring
      .listMonitoringWorkloadRecommendationCount(query)
      .then((res) => {
        const workloadRecommendationCountRes = res?.data?.data?.k8s_workloads_cloud_account_monitoring_recommendations_v2?.rows || [];
        if (workloadRecommendationCountRes && workloadRecommendationCountRes.length > 0) {
          const updatedStats = data.map((obj1: any) => {
            const matchingObj = workloadRecommendationCountRes.find(
              (obj2: any) => obj1.accountId == obj2.account_id && obj1.name == obj2.workload_name && obj1.namespace == obj2.namespace
            );
            if (matchingObj) {
              return {
                ...obj1,
                optimize: matchingObj?.recommendation_count,
              };
            }
            return obj1;
          });
          setData(updatedStats);
        }
      })
      .catch((err) => {
        console.error(err);
      })
      .finally(() => {
        setConsumeRelayServer(true);
      });
  };

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  return (
    <>
      <FilterGroup
        filterOptions={[
          {
            type: 'dropdown',
            enabled: true,
            options: clusterOption,
            onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
              setSelectedCluster(e?.target?.value);
              setSelectedNamespace('');
              setSelectedWorkload('');
              setUniqueAcc([]);
              setData([]);
            },
            minWidth: '150px',
            label: 'Cluster',
            disabled: consumeRelayServer,
          },
          {
            type: 'dropdown',
            enabled: true,
            options: allNameSpaces,
            onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
              setSelectedNamespace(e?.target?.value);
              setSelectedWorkload('');
              setUniqueAcc([]);
              setData([]);
            },
            minWidth: '150px',
            label: 'Namespace',
            value: selectedNamespace,
            disabled: consumeRelayServer,
          },
          {
            type: 'dropdown',
            enabled: true,
            options: nsWorkloadOptions,
            onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
              setSelectedWorkload(e?.target?.value);
              setUniqueAcc([]);
              setData([]);
            },
            minWidth: '150px',
            label: 'Workload',
            value: selectedWorkload,
            disabled: consumeRelayServer,
          },
        ]}
        dateTimeRange={{
          enabled: false,
          onChange: handleDateRangeChange,
          passedSelectedDateTime: {
            startTime: selectedDateRange.startDate,
            endTime: selectedDateRange.endDate,
            shortcutClickTime: 0,
          },
        }}
      />
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: 'repeat(5, 1fr)',

          '@media(min-width: 1800px) and (max-width: 2300px)': {
            gridTemplateColumns: 'repeat(4, 1fr)',
            '& .cardItem': {
              maxWidth: '210px !important',
            },
          },

          '@media(min-width: 1450px) and (max-width: 1800px)': {
            gridTemplateColumns: 'repeat(3, 1fr)',
            '& .cardItem': {
              maxWidth: '210px !important',
            },
          },
          '@media(min-width: 1290px) and (max-width: 1450px)': {
            gridTemplateColumns: 'repeat(3, 1fr)',
          },
          '@media(max-width: 1290px)': {
            gridTemplateColumns: 'repeat(2, 1fr)',
            '& .cardItem': {
              maxWidth: '230px !important',
            },
          },
        }}
      >
        {loading ? (
          <Loader />
        ) : !loading && data.length > 0 ? (
          data.map((item, index) => <KubernetesMonitoringCard data={item} key={index} />)
        ) : errorMessage ? (
          <Typography>{errorMessage}</Typography>
        ) : (
          <Typography>No Data For Monitoring</Typography>
        )}
      </Box>
    </>
  );
};

export default KubernetesMonitoring;

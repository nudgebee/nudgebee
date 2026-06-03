import { Box, Typography } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import podIcon from '../../../assets/kubernetesPod-icon.svg';
import CustomTabs from '@common-new/CustomTabs';
import React, { useState, useEffect } from 'react';
import PodDetailsBox from './PodDetailsBox';
import { KubernetesCostCharts, KubernetesSecurityDrilldown, KubernetesUtilizationCharts3 } from '@components1/k8s/common/KubernetesTable2';
import KubernetesEventsTable from '@components1/events/KubernetesEvents';
import { getLast30Days, getLast7Days } from '@lib/datetime';
import KubernetesPodYaml from '@components1/k8s/details/KubernetesPodYaml';
import KubernetesPodLogs from '@components1/k8s/details/KubernetesPodLogs';
import KubernetesPodProfiler from '@components1/k8s/details/KubernetesPodProfiler';
import { ds } from 'src/utils/colors';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';
import KubernetesServiceMap from '@components1/k8s/details/KubernetesServiceMap';
import AppDashboard from '@components1/dashboards/AppDashboard';
import apiKubernetes1 from '@api1/kubernetes1';

const optionsToDisplay = {
  tabOptions: [
    { text: 'Pod Details', value: 0, fragment: 'pod-details' },
    { text: 'Utilization Trends', value: 1, fragment: 'utilization-trends' },
    { text: 'Cost Trends', value: 2, fragment: 'cost-trends' },
    { text: 'Recent Events', value: 3, fragment: 'recent-events' },
    { text: 'Yaml', value: 4, fragment: 'yaml' },
    { text: 'Logs', value: 5, fragment: 'logs' },
    { text: 'Profiler', value: 6, fragment: 'profiler' },
    { text: 'Service Map', value: 7, fragment: 'service-map' },
    { text: 'App Dashboard', value: 8, fragment: 'app-dashboard' },
    { text: 'Security', value: 9, fragment: 'security' },
  ],
};

const PodDetailsPage = ({ pod }) => {
  const router = useRouter();
  const [option, setOption] = useState(0);
  const [stateQuery, setStateQuery] = useState({});
  const podData = (pod ?? [])[0];

  // Sync tab from hash — runs on mount and on back/forward navigation
  useEffect(() => {
    const hash = router.asPath.split('#')[1] ?? '';
    const tab = optionsToDisplay.tabOptions.find((t) => t.fragment === hash);
    if (tab) setOption(tab.value);
    else setOption(0);
  }, [router.asPath]);

  const selectedDateRange = {
    startDate: getLast30Days().getTime(),
    endDate: new Date().getTime(),
  };

  useEffect(() => {
    if (podData && Object.keys(podData).length > 0) {
      let query = {
        accountId: podData?.account,
        pod_name: podData?.name,
        namespace_name: podData?.meta?.namespace,
        kind: podData?.meta?.controllerKind,
        workload_name: podData?.meta?.controller,
        workloadMeta: podData?.meta,
      };
      if (selectedDateRange?.startDate) {
        query.startDate = selectedDateRange.startDate;
      }
      if (selectedDateRange?.endDate) {
        query.endDate = selectedDateRange.endDate;
      }
      query.type = 'pod';
      setStateQuery(query);

      const d = new Date();
      const twelveHoursAgo = new Date(d.getTime() - 12 * 60 * 60 * 1000); // 12 hours in milliseconds

      const requestBody = {
        accountId: query.accountId,
        metrics: ['container_application_type_with_workload'],
        startDate: twelveHoursAgo.getTime(),
        endDate: d.getTime(),
        namespaceName: query.namespace_name,
        workloadName: query.workload_name || query.pod_name,
        instant: true,
      };
      apiKubernetes1.utilisationApi(requestBody).then((res) => {
        const seriesOfApplicationTypesResponse = res?.[0]?.payload || [];
        for (const containerApplicationType of seriesOfApplicationTypesResponse) {
          if (containerApplicationType?.metric?.container_id.includes('/metrics')) {
            continue;
          }
          const lang = containerApplicationType?.metric.application_type;
          if (lang) {
            const framework = lang === 'golang' ? 'go' : lang;
            setStateQuery((prev) => ({ ...prev, framework }));
          }
          break;
        }
      });
    }
  }, [podData]);

  if (!podData) {
    return null;
  }

  return (
    <>
      <Box
        sx={{
          padding: 'var(--ds-space-4) var(--ds-space-4)',
          border: `1px solid ${ds.blue[300]}`,
          borderRadius: 'var(--ds-radius-lg)',
          display: 'flex',
          alignItems: 'center',
        }}
      >
        <Box marginRight={ds.space[5]} display={'flex'} alignItems={'center'}>
          <SafeIcon alt='' src={podIcon} width={30} height={30} />
        </Box>
        <Box marginRight={ds.space[5]}>
          <Typography fontSize={ds.text.small} fontWeight={400} color={ds.gray[400]} lineHeight={'20px'}>
            Type
          </Typography>
          <Typography fontSize={ds.text.title} fontWeight={500} color={ds.gray[700]}>
            {podData?.meta?.controllerKind ?? '-'}
          </Typography>
        </Box>
        <Box marginRight={ds.space[5]}>
          <Typography fontSize={ds.text.small} fontWeight={400} color={ds.gray[400]} lineHeight={'20px'}>
            Cluster
          </Typography>
          <Typography fontSize={ds.text.title} fontWeight={500} color={ds.gray[700]}>
            {podData?.cloud_account?.account_name ?? '-'}
          </Typography>
        </Box>
        <Box marginRight={ds.space[5]}>
          <Typography fontSize={ds.text.small} fontWeight={400} color={ds.gray[400]} lineHeight={'20px'}>
            Namespace
          </Typography>
          <Typography fontSize={ds.text.title} fontWeight={500} color={ds.gray[700]}>
            {podData?.meta?.namespace ?? '-'}
          </Typography>
        </Box>
        <Box marginRight={ds.space[5]}>
          <Typography fontSize={ds.text.small} fontWeight={400} color={ds.gray[400]} lineHeight={'20px'}>
            Controlled by
          </Typography>
          <Typography fontSize={ds.text.title} fontWeight={500} color={ds.gray[700]}>
            {podData?.meta?.controller ?? '-'}
          </Typography>
        </Box>
      </Box>

      <Box sx={{ margin: 'var(--ds-space-5) 0px var(--ds-space-5) 0px' }}>
        <CustomTabs value={option} onChange={setOption} options={optionsToDisplay} showBorderBottom={true} p='0' borderRadius='0px' />

        {option === 0 && <PodDetailsBox wordBreak={'break-all'} pod={podData} accountId={podData?.account} />}
        {option === 1 && (
          <Box sx={{ padding: 'var(--ds-space-6) var(--ds-space-2)' }}>
            {stateQuery?.pod_name && <KubernetesUtilizationCharts3 row={''} accountId={podData?.account} query={stateQuery} />}
          </Box>
        )}
        {option === 2 && (
          <Box sx={{ padding: 'var(--ds-space-6) var(--ds-space-2)' }}>
            <KubernetesCostCharts
              row={''}
              accountId={podData?.account}
              query={stateQuery}
              selectedDateRange={{
                startDate: getLast7Days().getTime(),
                endDate: new Date().getTime(),
              }}
            />
          </Box>
        )}
        {option === 3 && (
          <Box sx={{ padding: 'var(--ds-space-2) 0px var(--ds-space-2) 0px' }}>
            <KubernetesEventsTable
              row={''}
              accountId={podData?.account}
              defaultQuery={{
                subject_name: podData?.name,
                subject_namespace: podData?.meta?.namespace,
                subject_type: 'pod',
              }}
              enableFilters={false}
            />
          </Box>
        )}
        {option === 4 && (
          <Box>
            <KubernetesPodYaml accountId={podData?.account} query={stateQuery} />
          </Box>
        )}
        {option === 5 && (
          <Box>
            <KubernetesPodLogs podData={podData} />
          </Box>
        )}
        {option === 6 && (
          <Box>
            <KubernetesPodProfiler accountId={podData?.account} query={stateQuery} />
          </Box>
        )}
        {option === 7 && (
          <Box>
            <KubernetesServiceMap accountId={podData?.account} appName={podData?.meta?.controller} namespaceName={podData?.meta?.namespace} />
          </Box>
        )}
        {option === 8 && (
          <Box>
            <AppDashboard
              accountId={podData?.account}
              namespaceName={stateQuery.namespace_name}
              podName={stateQuery.pod_name}
              podIp={podData?.meta?.status_info?.pod_ip}
              workloadName={stateQuery.workload_name}
            />
          </Box>
        )}
        {option === 9 && (
          <Box>
            <KubernetesSecurityDrilldown accountId={podData?.account} query={stateQuery} />
          </Box>
        )}
      </Box>
    </>
  );
};

PodDetailsPage.propTypes = {
  pod: PropTypes.array,
};

export default PodDetailsPage;

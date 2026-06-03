import React, { useEffect, useMemo } from 'react';
import AnchorComponent from '@common-new/AnchorComponent';
import ErrorBoundary from '@components1/common/ErrorBoundary';
import { Box } from '@mui/material';
import KubernetesClusterOverview from '@components1/k8s/landing/KubernetesClusterOverview';
import KubernetesMonitoring from '@components1/k8s/landing/KubernetesMonitoringView';
import KubernetesApplicationGrouping from '@components1/k8s/landing/k8sGrouping/KubernetesApplicationGrouping';
import { useData } from '@context/DataContext';
import { useRouter } from 'next/router';
import homeApi from '@api1/home';
import { transformClusters } from '@components1/common/UpdateDataContext';
import { KubernetesClusterIcon, ApplicationsIcon } from '@assets';

const Kubernetes = () => {
  const router = useRouter();
  const { setSelectedCluster, allCluster, setAllCluster } = useData();

  useEffect(() => {
    if (!allCluster || allCluster.length === 0) {
      homeApi.getCloudAccounts().then((res) => {
        const clusters = transformClusters(res);
        setAllCluster(clusters);
      });
    }
  }, []);

  const hasClusters = allCluster?.some((c) => c.value !== 'demo' && c.cloud_provider?.toUpperCase() === 'K8S');

  const filterOptions = useMemo(
    () => [
      {
        name: 'Cluster Overview',
        id: 'cluster-overview',
        fragment: 'overview',
        value: 0,
        disabled: false,
        icon: KubernetesClusterIcon,
        ...(hasClusters && {
          options: [
            { id: 'clusters', name: 'Clusters' },
            { id: 'issues', name: 'Issues' },
            { id: 'pod-exception', name: 'Pod Exception' },
            { id: 'node-exception', name: 'Node Exception' },
          ],
        }),
      },
      {
        name: 'Application Grouping',
        id: 'cluster-application-grouping',
        fragment: 'groups',
        value: 2,
        disabled: false,
        betaIcon: true,
        icon: ApplicationsIcon,
      },
    ],
    [hasClusters]
  );

  const [selectedFilter, setSelectedFilter] = React.useState(0);

  useEffect(() => {
    setSelectedCluster({});
  }, []);

  useEffect(() => {
    const hash = router.asPath.split('#')[1];
    if (!hash || !filterOptions.length) {
      setSelectedFilter(0);
      return;
    }
    const fragment = hash;
    const filter = filterOptions.find((option) => option.fragment === fragment);
    if (filter) {
      setSelectedFilter(filter.value);
    }
  }, []);

  return (
    <>
      <AnchorComponent
        manageRoute={true}
        filterOptions={filterOptions}
        onChangeFilter={(val) => {
          if (val === 0 || val === 1 || val === 2) {
            setSelectedFilter(val);
          }
        }}
      />
      <ErrorBoundary key={selectedFilter}>
        {selectedFilter === 0 && (
          <Box>
            <KubernetesClusterOverview />
          </Box>
        )}
        {selectedFilter === 1 && (
          <Box>
            <KubernetesMonitoring />
          </Box>
        )}
        {selectedFilter === 2 && (
          <Box mt='80px'>
            <KubernetesApplicationGrouping />
          </Box>
        )}
      </ErrorBoundary>
    </>
  );
};

export default Kubernetes;

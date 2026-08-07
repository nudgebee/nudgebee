import { useEffect, useMemo, useState } from 'react';
import AnchorComponent from '@components/common/navigation/AnchorComponent';
import ErrorBoundary from '@shared/ErrorBoundary';
import { Box, CircularProgress } from '@mui/material';
import KubernetesClusterOverview from '@components/k8s/landing/KubernetesClusterOverview';
import { useData } from '@context/DataContext';
import { useRouter } from 'next/router';
import homeApi from '@api1/home';
import { transformClusters } from '@shared/layout/UpdateDataContext';
import { KubernetesClusterIcon } from '@assets';

const isK8sProvider = (provider) => provider?.toUpperCase() === 'K8S';

const Kubernetes = () => {
  const router = useRouter();
  const { selectedCluster, setSelectedCluster, allCluster, setAllCluster } = useData();

  // `/kubernetes` with no hash is a redirector to the in-scope cluster's detail
  // page, mirroring `/cloud-account`. The Cluster Overview below still renders
  // for an explicit `#overview` deep link, and as the zero-cluster state
  // (KubernetesClusterOverview owns the connect-cluster CTA).
  const [mode, setMode] = useState('redirecting');

  useEffect(() => {
    let cancelled = false;

    // window.location.hash, not router.asPath: on an auto-statically-optimized
    // page asPath can still be the server-rendered value on mount, and a dropped
    // hash here redirects a `#overview` deep link into a cluster instead.
    const hash = window.location.hash.replace('#', '');
    // Application Grouping moved to /dashboards; keep older links working.
    if (hash === 'groups') {
      router.replace('/dashboards#groups');
      return undefined;
    }
    if (hash) {
      setMode('landing');
      return undefined;
    }

    const redirectToCluster = async () => {
      if (selectedCluster?.value && isK8sProvider(selectedCluster.cloud_provider)) {
        router.push(`/kubernetes/details/${selectedCluster.value}#summary`);
        return;
      }
      try {
        const accounts = await homeApi.getCloudAccounts();
        if (cancelled) {
          return;
        }
        const clusters = transformClusters(accounts.filter((account) => isK8sProvider(account.cloud_provider)));
        if (clusters.length > 0) {
          setSelectedCluster(clusters[0]);
          await router.push(`/kubernetes/details/${clusters[0].value}#summary`);
          return;
        }
      } catch (error) {
        console.error(error);
      }
      // No cluster to open (or the lookup failed) — fall back to the landing page.
      if (!cancelled) {
        setMode('landing');
      }
    };

    redirectToCluster();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (mode !== 'landing') {
      return;
    }
    setSelectedCluster({});
    if (!allCluster || allCluster.length === 0) {
      homeApi.getCloudAccounts().then((res) => {
        const clusters = transformClusters(res);
        setAllCluster(clusters);
      });
    }
  }, [mode]);

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
    ],
    [hasClusters]
  );

  if (mode === 'redirecting') {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '70vh' }}>
        <CircularProgress />
      </Box>
    );
  }

  return (
    <>
      <AnchorComponent manageRoute={true} filterOptions={filterOptions} />
      <ErrorBoundary>
        <Box>
          <KubernetesClusterOverview />
        </Box>
      </ErrorBoundary>
    </>
  );
};

export default Kubernetes;

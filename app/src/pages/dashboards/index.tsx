import React, { useEffect, useState } from 'react';
import { useRouter } from 'next/router';
import { Box } from '@mui/material';
import CustomDashboards from '@components/k8s/dashboards/CustomDashboards';
import DashboardSkeleton from '@components/k8s/dashboards/DashboardSkeleton';
import KubernetesApplicationGrouping from '@components/k8s/landing/k8sGrouping/KubernetesApplicationGrouping';
import ErrorBoundary from '@shared/ErrorBoundary';
import { useData } from '@context/DataContext';
import homeApi from '@api1/home';
import { transformClusters } from '@shared/layout/UpdateDataContext';
import { ds } from '@utils/colors';

/** Custom dashboards, as a top-level page. Previously the third tab of /kubernetes. */

/** Anything else — including no hash at all — is the dashboard list. */
const GROUPING_FRAGMENT = 'groups';

const DashboardsPage: React.FC = () => {
  const router = useRouter();
  const { allCluster, setAllCluster } = useData();
  /*
   * Whether the account list is still arriving.
   */
  const [loadingAccounts, setLoadingAccounts] = useState(!allCluster?.length);
  const [selectedTab, setSelectedTab] = useState<number | null>(null);

  useEffect(() => {
    if (allCluster && allCluster.length > 0) {
      setLoadingAccounts(false);
      return undefined;
    }
    let cancelled = false;
    setLoadingAccounts(true);
    homeApi
      .getCloudAccounts()
      .then((res: unknown) => {
        if (!cancelled) setAllCluster(transformClusters(res));
      })
      .finally(() => {
        // Cleared on failure too: a fetch that errored is not "still loading",
        // and the empty state is then the honest answer.
        if (!cancelled) setLoadingAccounts(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    // window.location.hash, not router.asPath: on an auto-statically-optimized
    const hash = window.location.hash.replace('#', '');
    setSelectedTab(hash === GROUPING_FRAGMENT ? 1 : 0);
  }, [router.asPath]);

  return (
    <ErrorBoundary key={selectedTab ?? 'none'}>
      {/* Top gutter for the bodies that render as a card. An open dashboard
          pulls back out of it — its toolbar is pinned under the app header. */}
      <Box sx={{ pt: ds.space[5] }}>
        {/* The same skeleton CustomDashboards shows while it loads a deep-linked
            dashboard, so the two consecutive waits read as one. */}
        {selectedTab === 0 && (loadingAccounts ? <DashboardSkeleton /> : <CustomDashboards />)}
        {selectedTab === 1 && <KubernetesApplicationGrouping />}
      </Box>
    </ErrorBoundary>
  );
};

export default DashboardsPage;

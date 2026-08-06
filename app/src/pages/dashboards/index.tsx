import React, { useEffect, useState } from 'react';
import CustomDashboards from '@components/k8s/dashboards/CustomDashboards';
import DashboardSkeleton from '@components/k8s/dashboards/DashboardSkeleton';
import ErrorBoundary from '@shared/ErrorBoundary';
import { useData } from '@context/DataContext';
import homeApi from '@api1/home';
import { transformClusters } from '@shared/layout/UpdateDataContext';

/**
 * Custom dashboards, as a top-level page.
 *
 * Previously the third tab of /kubernetes. A dashboard panel may query any
 * connected account — cloud accounts and integrations as much as clusters — so
 * living under Kubernetes both understated what it does and buried it three
 * tabs deep.
 */
const DashboardsPage: React.FC = () => {
  const { allCluster, setAllCluster } = useData();
  /*
   * Whether the account list is still arriving.
   *
   * The list backs every panel's account picker, and CustomDashboards reads an
   * empty one as "no accounts are connected" — so without this the page opens
   * on "Connect a cloud account to build dashboards" and then swaps to the
   * listing a moment later, telling a tenant with accounts that it has none.
   *
   * Under /kubernetes that page had already fetched it; landing here directly,
   * or reloading on this URL, has not.
   */
  const [loadingAccounts, setLoadingAccounts] = useState(!allCluster?.length);

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

  return (
    <ErrorBoundary>
      {/* The same skeleton CustomDashboards shows while it loads a deep-linked
          dashboard, so the two consecutive waits read as one. */}
      {loadingAccounts ? <DashboardSkeleton /> : <CustomDashboards />}
    </ErrorBoundary>
  );
};

export default DashboardsPage;

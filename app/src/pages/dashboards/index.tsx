import React, { useEffect, useState } from 'react';
import AnchorComponent from '@components/common/navigation/AnchorComponent';
import CustomDashboards from '@components/k8s/dashboards/CustomDashboards';
import DashboardSkeleton from '@components/k8s/dashboards/DashboardSkeleton';
import KubernetesApplicationGrouping from '@components/k8s/landing/k8sGrouping/KubernetesApplicationGrouping';
import ErrorBoundary from '@shared/ErrorBoundary';
import { useData } from '@context/DataContext';
import homeApi from '@api1/home';
import { transformClusters } from '@shared/layout/UpdateDataContext';
import { dashboardIcon1, ApplicationsIcon } from '@assets';

/**
 * Custom dashboards, as a top-level page.
 *
 * Previously the third tab of /kubernetes. A dashboard panel may query any
 * connected account — cloud accounts and integrations as much as clusters — so
 * living under Kubernetes both understated what it does and buried it three
 * tabs deep. Application Grouping followed it here for the same reason: a group
 * is a saved view over accounts, not a property of one cluster, and /kubernetes
 * is now a redirector to the in-scope cluster's detail page.
 */
const tabOptions = [
  { name: 'Dashboard List', id: 'dashboard-list', fragment: 'list', value: 0, disabled: false, icon: dashboardIcon1 },
  { name: 'Application Grouping', id: 'application-grouping', fragment: 'groups', value: 1, disabled: false, betaIcon: true, icon: ApplicationsIcon },
];

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
    // page asPath can still be the server-rendered value on mount, which drops
    // the hash and would open `#groups` on the list tab. This is also the source
    // AnchorComponent picks its own initial tab from, so the bar and the body
    // cannot disagree. Reading it in an effect keeps it off the SSR render.
    const hash = window.location.hash.replace('#', '');
    const tab = tabOptions.find((option) => option.fragment === hash);
    setSelectedTab(tab ? tab.value : 0);
  }, []);

  return (
    <>
      <AnchorComponent manageRoute={true} filterOptions={tabOptions} onChangeFilter={(val: number) => setSelectedTab(val)} />
      <ErrorBoundary key={selectedTab ?? 'none'}>
        {/* The same skeleton CustomDashboards shows while it loads a deep-linked
            dashboard, so the two consecutive waits read as one. */}
        {selectedTab === 0 && (loadingAccounts ? <DashboardSkeleton /> : <CustomDashboards />)}
        {selectedTab === 1 && <KubernetesApplicationGrouping />}
      </ErrorBoundary>
    </>
  );
};

export default DashboardsPage;

import { useEffect, useMemo, useState } from 'react';
import AnchorComponent from '@components/common/navigation/AnchorComponent';
import ErrorBoundary from '@shared/ErrorBoundary';
import { Box } from '@mui/material';
import AccountOverview from '@components/overview/AccountOverview';
import { useData } from '@context/DataContext';
import homeApi from '@api1/home';
import { transformClusters } from '@shared/layout/UpdateDataContext';
import { KubernetesClusterIcon } from '@assets';
import { isCloudProvider, isSelfHostedProvider } from '@components/overview/providers';

/**
 * Account Overview — the fleet-wide landing surface, one card per connected
 * account across every provider (K8s, AWS, Azure, GCP, CloudFoundry and
 * self-hosted VMs).
 *
 * Was `/kubernetes#overview` while it only listed clusters; that hash now
 * redirects here (src/pages/kubernetes/index.jsx) so old links keep working.
 * It is a route of its own rather than a tab because it is no longer scoped to
 * one provider's page.
 */
const Overview = () => {
  const { setSelectedCluster, allCluster, setAllCluster } = useData();

  const [accountKinds, setAccountKinds] = useState({ k8s: false, cloud: false, vm: false });

  useEffect(() => {
    // The page is fleet-wide, so no single account is "in scope" while it is
    // open — clearing keeps the header from showing a stale cluster.
    setSelectedCluster({});
    if (!allCluster || allCluster.length === 0) {
      homeApi.getCloudAccounts().then((res) => {
        setAllCluster(transformClusters(res));
      });
    }
  }, []);

  useEffect(() => {
    const real = (allCluster || []).filter((account) => account.value !== 'demo');
    setAccountKinds({
      k8s: real.some((account) => account.cloud_provider?.toUpperCase() === 'K8S'),
      cloud: real.some((account) => isCloudProvider(account.cloud_provider)),
      vm: real.some((account) => isSelfHostedProvider(account.cloud_provider)),
    });
  }, [allCluster]);

  // Anchors mirror the sections AccountOverview actually renders — offering a
  // "Cloud Accounts" jump on a K8s-only tenant would scroll to nothing.
  const filterOptions = useMemo(() => {
    const sections = [];
    if (accountKinds.k8s) {
      sections.push({ id: 'clusters', name: 'Clusters' });
    }
    if (accountKinds.cloud) {
      sections.push({ id: 'cloud-accounts', name: 'Cloud Accounts' });
    }
    if (accountKinds.vm) {
      sections.push({ id: 'vm-fleets', name: 'Self-hosted VMs' });
    }
    if (accountKinds.k8s) {
      sections.push(
        { id: 'issues', name: 'Issues' },
        { id: 'pod-exception', name: 'Pod Exception' },
        { id: 'node-exception', name: 'Node Exception' }
      );
    }
    return [
      {
        name: 'Account Overview',
        id: 'account-overview',
        fragment: 'overview',
        value: 0,
        disabled: false,
        icon: KubernetesClusterIcon,
        ...(sections.length > 0 && { options: sections }),
      },
    ];
  }, [accountKinds]);

  return (
    <>
      <AnchorComponent manageRoute={true} filterOptions={filterOptions} />
      <ErrorBoundary>
        <Box>
          <AccountOverview />
        </Box>
      </ErrorBoundary>
    </>
  );
};

export default Overview;

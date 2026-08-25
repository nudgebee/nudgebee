import React, { useEffect, useState, useMemo } from 'react';
import { useRouter } from 'next/router';
import apiKubernetes from '@api1/kubernetes';
import apiHome from '@api1/home';
import apiOverview from '@api1/overview';
import ClusterViewCard from '@components/k8s/common/ClusterViewCard';
import KubernetesMemoryCpuOverView, { CpuMemorySkeleton } from '@components/k8s/common/KubernetesMemoryCpuOverView';
import KubernetesIssuesOverView from '@components/k8s/common/KubernetesIssuesOverView';
import KubernetesSaving from '@components/k8s/common/KubernetesSaving';
import K8sClusterInsights from '@components/k8s/common/k8sClusterInsights';
import { Grid, Box, Typography, Stack } from '@mui/material';
import { K8sIcon } from '@assets';
import KubernetesDashboardIssues from '@components/k8s/dashboard/KubernetesDashboardIssues';
import KubernetesDashboardPodExceptions from '@components/k8s/dashboard/KubernetesDashboardPodExceptions';
import KubernetesDashboardNodeExceptions from '@components/k8s/dashboard/KubernetesDashboardNodeExceptions';
import CloudAccountOverviewCard from '@components/overview/CloudAccountOverviewCard';
import VmAccountOverviewCard from '@components/overview/VmAccountOverviewCard';
import ErrorBoundary from '@shared/ErrorBoundary';
import { Skeleton } from '@ui/Skeleton';
import { toast as snackbar } from '@ui/Toast';
import K8sAccountModal from '@components/integrations/modal/K8sAccountModal';
import { Button as DsButton } from '@ui/Button';
import { TourLauncher } from '@components/common/tour';
import { useData } from '@context/DataContext';
import { hasWriteAccess } from '@lib/auth';
import { ds } from '@utils/colors';
import { CLOUD_PROVIDERS, isCloudProvider, isSelfHostedProvider } from './providers';

const ClusterCardSkeleton = ({ cardStyle }) => (
  <Box sx={cardStyle}>
    <Box display='flex' gap={ds.space[4]} alignItems='flex-start'>
      <Box
        sx={{
          width: { xs: ds.space.mul(0, 130), md: ds.space.mul(0, 160) },
          flexShrink: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--ds-space-3)',
        }}
      >
        <Skeleton shape='rect' width='65%' height='24px' />
        <Skeleton shape='rect' width='100%' height={ds.space.mul(0, 48)} />
        <Skeleton shape='rect' width='100%' height={ds.space.mul(0, 26)} />
        <Skeleton shape='rect' width='80%' height='14px' />
      </Box>
      <Box sx={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        <Box display='flex' gap='14px'>
          <Box sx={{ flex: 7 }}>
            <CpuMemorySkeleton />
          </Box>
          <Box sx={{ flex: 3 }}>
            <Skeleton shape='rect' width='100%' height={ds.space.mul(0, 65)} />
          </Box>
          <Box sx={{ flex: 2 }}>
            <Skeleton shape='rect' width='100%' height={ds.space.mul(0, 65)} />
          </Box>
        </Box>
        <Skeleton shape='rect' width='100%' height={ds.space.mul(0, 30)} />
      </Box>
    </Box>
  </Box>
);

/**
 * Section divider between provider groups. Carries the `id` the page's anchor
 * bar scrolls to, so each group is reachable from the tab strip.
 */
const SectionHeading = ({ id, title, count }) => (
  <Box id={id} sx={{ display: 'flex', alignItems: 'baseline', gap: 'var(--ds-space-2)', mt: ds.space[4], scrollMarginTop: ds.space.mul(0, 60) }}>
    <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>
      {title}
    </Typography>
    {count > 0 ? <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>({count})</Typography> : null}
  </Box>
);

/**
 * Fleet-wide summary across every connected account, whatever its provider —
 * the `/overview` page.
 *
 * Three sections, each rendered only when the tenant has accounts of that kind:
 * K8s clusters, provider-API cloud accounts (AWS / Azure / GCP / CloudFoundry)
 * and self-hosted VM fleets. The K8s fleet tables below them (issues, pod and
 * node exceptions) stay K8s-only because there is no cross-provider equivalent
 * of a pod.
 *
 * Request shape differs by section on purpose. The K8s cards keep their
 * existing per-cluster fan-out (each child component fetches its own slice);
 * the cloud and VM cards are fed by two batched queries in @api1/overview, so
 * adding providers here costs two requests, not two per account.
 */
const AccountOverview = () => {
  const router = useRouter();
  const { setSelectedCluster } = useData();
  const [clusterOption, setClusterOption] = useState([]);
  const [allNameSpaces, setAllNameSpaces] = useState([]);
  const [k8sClusters, setK8sClusters] = useState([]);
  const [cloudAccounts, setCloudAccounts] = useState([]);
  const [vmAccounts, setVmAccounts] = useState([]);
  const [cloudSummaries, setCloudSummaries] = useState({});
  const [vmSummaries, setVmSummaries] = useState({});
  // All three start true: the zero-account state below is now a full-page
  // panel, so opening on "nothing is connected" for the one frame before the
  // fetches start would be a visible flash, not a subtle one.
  const [loading, setLoading] = useState(true);
  // Split from the summaries below on purpose — which sections exist is known
  // as soon as the account list lands, so a tenant with no cloud accounts stops
  // rendering a Cloud Accounts skeleton then, not when the (empty) rollup
  // query it never needed comes back.
  const [loadingAccountList, setLoadingAccountList] = useState(true);
  const [loadingSummaries, setLoadingSummaries] = useState(true);
  const [showAddClusterModal, setShowAddClusterModal] = useState(false);

  const sortedClusters = useMemo(() => {
    if (!k8sClusters?.length) return k8sClusters || [];

    const checkConnections = (clusterEntry) => {
      const requiredProps = ['logsConnection', 'nodeAgentConnection', 'prometheusConnection', 'relayConnection'];
      for (const prop of requiredProps) {
        if (!clusterEntry?.agent?.connection_status?.[prop]) return false;
      }
      const connectionStatus = clusterEntry?.agent?.connection_status;
      return !!(connectionStatus?.opencostConnection || connectionStatus?.opencostServerSide);
    };

    const getConnectionPriority = (clusterEntry) => {
      if (clusterEntry?.agent?.status === 'CONNECTED') {
        return checkConnections(clusterEntry) ? 0 : 1;
      }
      return 2;
    };

    return [...k8sClusters].sort((a, b) => {
      const pA = getConnectionPriority(a);
      const pB = getConnectionPriority(b);
      if (pA !== pB) return pA - pB;
      return (a.account_name || '').localeCompare(b.account_name || '', undefined, { numeric: true, sensitivity: 'base' });
    });
  }, [k8sClusters]);

  const sortByName = (accounts) =>
    [...accounts].sort((a, b) => (a.account_name || '').localeCompare(b.account_name || '', undefined, { numeric: true, sensitivity: 'base' }));

  useEffect(() => {
    getClustersData();
  }, []);

  useEffect(() => {
    let cancelled = false;
    const getOtherAccountsData = async () => {
      let cloud = [];
      let vms = [];
      try {
        const accounts = await apiHome.getCloudAccounts();
        if (cancelled) return;
        cloud = sortByName(accounts.filter((account) => isCloudProvider(account.cloud_provider)));
        vms = sortByName(accounts.filter((account) => isSelfHostedProvider(account.cloud_provider)));
        setCloudAccounts(cloud);
        setVmAccounts(vms);
      } catch (error) {
        console.error(error);
      } finally {
        if (!cancelled) setLoadingAccountList(false);
      }

      try {
        const [cloudSummary, vmSummary] = await Promise.all([
          apiOverview.listCloudAccountSummaries(cloud.map((account) => account.id)),
          apiOverview.listVmAccountSummaries(vms.map((account) => account.id)),
        ]);
        if (cancelled) return;
        setCloudSummaries(cloudSummary);
        setVmSummaries(vmSummary);
      } catch (error) {
        console.error(error);
      } finally {
        if (!cancelled) setLoadingSummaries(false);
      }
    };

    getOtherAccountsData();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (clusterOption && clusterOption.length > 0) {
      getDropDownData(clusterOption.map((co) => co.value));
    }
  }, [clusterOption]);

  // /vm is a single tenant-level route with no account path segment, so opening
  // a fleet has to move the header's selected account as well as navigate —
  // the same two-step the header's own account dropdown does.
  const openVmAccount = (accountId) => {
    const account = vmAccounts.find((entry) => entry.id === accountId);
    if (account) {
      setSelectedCluster({
        label: account.account_name,
        value: account.id,
        status: account.status || '',
        cloud_provider: account.cloud_provider,
        account_type: account.account_type || '',
        agent: account.agents?.[0] || {},
      });
    }
    router.push(`/vm?accountId=${accountId}#summary`);
  };

  const getClustersData = async () => {
    try {
      setLoading(true);
      const response = await apiKubernetes.listk8ClusterData();
      const data = response?.cloudaccount_k8s_aggregate;
      setK8sClusters(data);
      const clusters = data
        .filter((f) => f.account_name)
        .map((item) => ({
          label: item.account_name,
          value: item.account_id,
        }));

      setClusterOption(clusters);
    } catch {
      snackbar.error('Failed to fetch clusters');
    } finally {
      setLoading(false);
    }
  };

  const getDropDownData = async (accountIds) => {
    try {
      const response = await apiKubernetes.getK8sNamespacesList(accountIds);
      setAllNameSpaces(response);
    } catch (error) {
      console.error(error);
    }
  };

  const styles = {
    clusterCard: {
      padding: 'var(--ds-space-4) var(--ds-space-3)',
      flexDirection: 'column',
      borderRadius: 'var(--ds-radius-xl)',
      background: 'var(--ds-background-100)',
      border: '1px solid var(--ds-gray-200)',
      boxShadow:
        '0px 1px 3px color-mix(in srgb, var(--ds-gray-700) 6%, transparent), 0px 1px var(--ds-space-0) color-mix(in srgb, var(--ds-gray-700) 4%, transparent)',
      transition: 'box-shadow 0.2s ease',
      '&:hover': {
        boxShadow:
          '0px var(--ds-space-1) var(--ds-space-2) color-mix(in srgb, var(--ds-gray-700) 8%, transparent), 0px var(--ds-space-0) var(--ds-space-1) color-mix(in srgb, var(--ds-gray-700) 4%, transparent)',
      },
    },
    clusterLayout: {
      mt: ds.space[2],
      gap: 'var(--ds-space-4)',
      display: 'flex',
      flexDirection: 'column',
      marginBottom: 'var(--ds-space-5)',
    },
  };

  const getClusterResourceData = (cluster, type) => {
    if (type === 'node') {
      return [
        { type: 'demand', count: cluster?.ondemand_node_count || 0 },
        { type: 'spot', count: cluster?.spot_node_count || 0 },
        { type: 'fallback', count: 0 },
      ];
    } else if (type === 'pod') {
      const podStatusCounts = cluster?.pod_status_counts ?? {};
      const podStatusArray = Object.entries(podStatusCounts)
        .filter(([, count]) => count > 0)
        .map(([type, count]) => ({
          type,
          count,
        }));
      const totalCount = Object.values(podStatusCounts)
        .filter((count) => count > 0)
        .reduce((sum, count) => sum + count, 0);
      podStatusArray.push({
        type: 'Total',
        count: totalCount,
      });
      return podStatusArray.sort((a, b) => b.count - a.count);
    }
  };

  const renderClusterOverViewComponents = (allcluster) => {
    if (loading) {
      return [0, 1].map((i) => <ClusterCardSkeleton key={`skeleton-cluster-${i}`} cardStyle={styles.clusterCard} />);
    }
    return allcluster?.map((cluster) => (
      <ErrorBoundary key={cluster?.account_id}>
        <Box id={`cluster_box_${cluster.account_name}`} sx={styles.clusterCard}>
          <Box display='flex' gap={ds.space[4]} alignItems='flex-start'>
            <Box sx={{ width: { xs: ds.space.mul(0, 130), md: ds.space.mul(0, 160) }, flexShrink: 0 }}>
              <ErrorBoundary>
                <ClusterViewCard
                  accountId={cluster?.account_id}
                  clusterName={cluster?.account_name}
                  nodeData={getClusterResourceData(cluster, 'node')}
                  podData={getClusterResourceData(cluster, 'pod')}
                />
              </ErrorBoundary>
            </Box>
            <Grid container alignItems='stretch' spacing='14px' columns={{ xs: 4, sm: 8, md: 12 }} sx={{ minWidth: 0, overflow: 'visible' }}>
              <Grid item md={7} sx={{ overflow: 'visible' }}>
                <ErrorBoundary>
                  <KubernetesMemoryCpuOverView
                    key={`cluster-box-${cluster?.account_id ?? ''}`}
                    requiredTooltip={true}
                    showUpdatedUi={true}
                    updatedOverview={false}
                    showUsage={false}
                    accountId={cluster?.account_id}
                  />
                </ErrorBoundary>
              </Grid>
              <Grid item sm={4} md={3}>
                <ErrorBoundary>
                  <KubernetesIssuesOverView accountId={cluster?.account_id} />
                </ErrorBoundary>
              </Grid>
              <Grid item sm={4} md={2}>
                <ErrorBoundary>
                  <KubernetesSaving accountId={cluster?.account_id} />
                </ErrorBoundary>
              </Grid>
              <Grid item md={12} sm={12}>
                <ErrorBoundary>
                  <K8sClusterInsights accountId={cluster?.account_id} />
                </ErrorBoundary>
              </Grid>
            </Grid>
          </Box>
        </Box>
      </ErrorBoundary>
    ));
  };

  const hasK8sClusters = k8sClusters?.length > 0;
  const hasCloudAccounts = cloudAccounts.length > 0;
  const hasVmAccounts = vmAccounts.length > 0;
  // The connect-your-first-account state belongs to the whole page, not to K8s:
  // a tenant that has only an AWS account must see its card, not an onboarding
  // panel telling it to install a cluster agent.
  const hasNoAccounts = !loading && !loadingAccountList && !hasK8sClusters && !hasCloudAccounts && !hasVmAccounts;

  return (
    <Box>
      {hasNoAccounts ? (
        <>
          <K8sAccountModal
            openModal={showAddClusterModal}
            handleClose={() => setShowAddClusterModal(false)}
            handleOnAccountCreate={getClustersData}
          />
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              justifyContent: 'center',
              padding: 'var(--ds-space-7) var(--ds-space-6)',
              borderRadius: 'var(--ds-radius-xl)',
              border: '1px solid var(--ds-gray-300)',
              background: 'var(--ds-background-100)',
            }}
          >
            <Box
              sx={{
                width: ds.space.mul(0, 32),
                height: ds.space.mul(0, 32),
                borderRadius: 'var(--ds-radius-xl)',
                background: `linear-gradient(135deg, var(--ds-blue-100) 0%, ${ds.blue[200]} 100%)`,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                mb: ds.space[5],
                boxShadow: '0px 1px 3px color-mix(in srgb, var(--ds-gray-700) 6%, transparent)',
                '& svg': { filter: 'none', width: 36, height: 36 },
              }}
            >
              <K8sIcon />
            </Box>

            <Typography
              sx={{
                fontSize: 'var(--ds-text-title)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-foreground)',
                mb: ds.space[2],
                fontFamily: 'Poppins',
              }}
            >
              Get started with infrastructure monitoring
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body-lg)',
                color: 'var(--ds-gray-600)',
                mb: ds.space[6],
                textAlign: 'center',
                maxWidth: ds.space.mul(0, 230),
                lineHeight: 1.6,
              }}
            >
              Connect a Kubernetes cluster, a cloud account ({CLOUD_PROVIDERS.join(', ')}) or a self-hosted VM fleet to gain full visibility into your
              workloads and resource usage - with actionable insights and cost optimization.
            </Typography>

            <Stack direction='row' spacing={4} sx={{ mb: ds.space[6] }}>
              {[
                {
                  label: 'Real-time monitoring',
                  icon: 'M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z',
                },
                {
                  label: 'Issue detection',
                  icon: 'M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z',
                },
                {
                  label: 'Cost optimization',
                  icon: 'M12 8c-1.657 0-3 .895-3 2s1.343 2 3 2 3 .895 3 2-1.343 2-3 2m0-8c1.11 0 2.08.402 2.599 1M12 8V7m0 1v8m0 0v1m0-1c-1.11 0-2.08-.402-2.599-1M21 12a9 9 0 11-18 0 9 9 0 0118 0z',
                },
              ].map((item) => (
                <Box key={item.label} sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                  <Box
                    sx={{
                      width: ds.space[6],
                      height: ds.space[6],
                      borderRadius: 'var(--ds-radius-lg)',
                      background: 'var(--ds-blue-100)',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      flexShrink: 0,
                    }}
                  >
                    <svg
                      width='16'
                      height='16'
                      viewBox='0 0 24 24'
                      fill='none'
                      stroke='var(--ds-blue-600)'
                      strokeWidth='1.5'
                      strokeLinecap='round'
                      strokeLinejoin='round'
                    >
                      <path d={item.icon} />
                    </svg>
                  </Box>
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-body)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      color: 'var(--ds-brand-500)',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    {item.label}
                  </Typography>
                </Box>
              ))}
            </Stack>

            {hasWriteAccess() ? (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                <TourLauncher tourId='connect-cluster' label='Show me how' size='lg' />
                {/* id='add-k8s-account' anchors the connect-cluster tour's first step (same id the
                    Accounts-page button uses; only one route renders at a time, so no duplicate). */}
                <DsButton id='add-k8s-account' tone='primary' size='lg' onClick={() => setShowAddClusterModal(true)}>
                  Add Cluster
                </DsButton>
                <DsButton
                  id='connect-cloud-account'
                  tone='secondary'
                  size='lg'
                  onClick={() => router.push('/user-management?integration=account#integrations')}
                >
                  Connect Cloud Account
                </DsButton>
              </Box>
            ) : (
              <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-600)', fontStyle: 'italic' }}>
                Need admin permission to connect an account
              </Typography>
            )}
          </Box>
        </>
      ) : (
        <>
          {loading || hasK8sClusters ? (
            <>
              <SectionHeading id='clusters' title='Kubernetes Clusters' count={k8sClusters?.length} />
              <Box sx={styles.clusterLayout}>{renderClusterOverViewComponents(sortedClusters)}</Box>
            </>
          ) : null}

          {loadingAccountList || hasCloudAccounts ? (
            <>
              <SectionHeading id='cloud-accounts' title='Cloud Accounts' count={cloudAccounts.length} />
              <Box sx={styles.clusterLayout}>
                {loadingAccountList && cloudAccounts.length === 0
                  ? [0, 1].map((i) => <ClusterCardSkeleton key={`skeleton-cloud-${i}`} cardStyle={styles.clusterCard} />)
                  : cloudAccounts.map((account) => (
                      <ErrorBoundary key={account.id}>
                        <Box id={`account_box_${account.account_name}`} sx={styles.clusterCard}>
                          <CloudAccountOverviewCard
                            accountId={account.id}
                            accountName={account.account_name}
                            cloudProvider={account.cloud_provider}
                            summary={cloudSummaries[account.id]}
                            loading={loadingSummaries}
                          />
                        </Box>
                      </ErrorBoundary>
                    ))}
              </Box>
            </>
          ) : null}

          {loadingAccountList || hasVmAccounts ? (
            <>
              <SectionHeading id='vm-fleets' title='Self-hosted VMs' count={vmAccounts.length} />
              <Box sx={styles.clusterLayout}>
                {loadingAccountList && vmAccounts.length === 0
                  ? [0].map((i) => <ClusterCardSkeleton key={`skeleton-vm-${i}`} cardStyle={styles.clusterCard} />)
                  : vmAccounts.map((account) => (
                      <ErrorBoundary key={account.id}>
                        <Box id={`account_box_${account.account_name}`} sx={styles.clusterCard}>
                          <VmAccountOverviewCard
                            accountId={account.id}
                            accountName={account.account_name}
                            summary={vmSummaries[account.id]}
                            loading={loadingSummaries}
                            onOpen={openVmAccount}
                          />
                        </Box>
                      </ErrorBoundary>
                    ))}
              </Box>
            </>
          ) : null}

          {hasK8sClusters ? (
            <ErrorBoundary>
              <KubernetesDashboardIssues id={'issues'} allClusters={k8sClusters} clusterOption={clusterOption} allNameSpaces={allNameSpaces} />
            </ErrorBoundary>
          ) : null}
          {hasK8sClusters ? (
            <ErrorBoundary>
              <KubernetesDashboardPodExceptions
                id={'pod-exception'}
                allClusters={k8sClusters}
                clusterOption={clusterOption}
                allNameSpaces={allNameSpaces}
              />
            </ErrorBoundary>
          ) : null}
          {hasK8sClusters ? (
            <ErrorBoundary>
              <KubernetesDashboardNodeExceptions id={'node-exception'} allClusters={k8sClusters} clusterOption={clusterOption} />
            </ErrorBoundary>
          ) : null}
        </>
      )}
    </Box>
  );
};

export default AccountOverview;

import React, { useEffect, useState } from 'react';
import apiKubernetes from '@api1/kubernetes';
import ClusterViewCard from '@components1/k8s/common/ClusterViewCard';
import KubernetesMemoryCpuOverView from '@components1/k8s/common/KubernetesMemoryCpuOverView';
import KubernetesIssuesOverView from '@components1/k8s/common/KubernetesIssuesOverView';
import KubernetesSaving from '@components1/k8s/common/KubernetesSaving';
import K8sClusterInsights from '@components1/k8s/common/k8sClusterInsights';
import { Grid, Box, Typography, Stack } from '@mui/material';
import { K8sIcon } from '@assets';
import KubernetesDashboardIssues from '@components1/k8s/dashboard/KubernetesDashboardIssues';
import KubernetesDashboardPodExceptions from '@components1/k8s/dashboard/KubernetesDashboardPodExceptions';
import KubernetesDashboardNodeExceptions from '@components1/k8s/dashboard/KubernetesDashboardNodeExceptions';
import ErrorBoundary from '@components1/common/ErrorBoundary';
import { Skeleton } from '@components1/ds/Skeleton';
import { v4 } from 'uuid';
import { toast as snackbar } from '@components1/ds/Toast';
import K8sAccountModal from '@components1/common/K8sAccountModal';
import { Button as DsButton } from '@components1/ds/Button';
import { hasWriteAccess } from '@lib/auth';
import { ds } from '@utils/colors';

const KubernetesClusterOverview = () => {
  const [allClusters, setAllClusters] = useState([]);
  const [clusterOption, setClusterOption] = useState([]);
  const [allNameSpaces, setAllNameSpaces] = useState([]);
  const [k8sClusters, setK8sClusters] = useState([]);
  const [loading, setLoading] = useState(false);
  const [showAddClusterModal, setShowAddClusterModal] = useState(false);

  useEffect(() => {
    getClustersData();
  }, []);

  useEffect(() => {
    if (clusterOption && clusterOption.length > 0) {
      getDropDownData(clusterOption.map((co) => co.value));
    }
  }, [clusterOption]);

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

  useEffect(() => {
    const appendEventstoAllDataArray = [];
    k8sClusters?.forEach((element) => {
      let row = { ...element };
      appendEventstoAllDataArray.push(row);
      return false;
    });
    setAllClusters(appendEventstoAllDataArray);
  }, [k8sClusters]);

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
      boxShadow: '0px 1px 3px rgba(16, 24, 40, 0.06), 0px 1px 2px rgba(16, 24, 40, 0.04)',
      transition: 'box-shadow 0.2s ease',
      '&:hover': {
        boxShadow: '0px 4px 8px rgba(16, 24, 40, 0.08), 0px 2px 4px rgba(16, 24, 40, 0.04)',
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
        .filter(([_, count]) => count > 0)
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
    if (loading || !allcluster?.length) {
      return Array(2)
        .fill(null)
        .map((_) => <Skeleton key={`shimmer-${v4()}`} shape='rect' height={ds.space.mul(0, 140)} width='auto' />);
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
            <Grid container alignItems='stretch' spacing='14px' columns={{ xs: 4, sm: 8, md: 12 }} sx={{ minWidth: 0, overflow: 'hidden' }}>
              <Grid item md={7} sx={{ overflow: 'hidden' }}>
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

  return (
    <Box>
      {!loading && k8sClusters?.length === 0 ? (
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
                boxShadow: '0px 1px 3px rgba(16, 24, 40, 0.06)',
                '& svg': { filter: 'none', width: 36, height: 36 },
              }}
            >
              <K8sIcon />
            </Box>

            <Typography
              sx={{
                fontSize: 'var(--ds-text-title)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-brand-700)',
                mb: ds.space[2],
                fontFamily: 'Poppins',
              }}
            >
              Get started with Kubernetes monitoring
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
              Connect your cluster to gain full visibility into workloads, nodes, and resource usage - with actionable insights and cost optimization.
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
                      stroke='#2563EB'
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
              <DsButton tone='primary' size='lg' onClick={() => setShowAddClusterModal(true)}>
                Add Cluster
              </DsButton>
            ) : (
              <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-600)', fontStyle: 'italic' }}>
                Need admin permission to connect a cluster
              </Typography>
            )}
          </Box>
        </>
      ) : (
        <>
          <Box sx={styles.clusterLayout}>{renderClusterOverViewComponents(allClusters)}</Box>
          {k8sClusters.length > 0 ? (
            <ErrorBoundary>
              <KubernetesDashboardIssues id={'issues'} allClusters={k8sClusters} clusterOption={clusterOption} allNameSpaces={allNameSpaces} />
            </ErrorBoundary>
          ) : null}
          {k8sClusters.length > 0 ? (
            <ErrorBoundary>
              <KubernetesDashboardPodExceptions
                id={'pod-exception'}
                allClusters={k8sClusters}
                clusterOption={clusterOption}
                allNameSpaces={allNameSpaces}
              />
            </ErrorBoundary>
          ) : null}
          {k8sClusters.length > 0 ? (
            <ErrorBoundary>
              <KubernetesDashboardNodeExceptions id={'node-exception'} allClusters={k8sClusters} clusterOption={clusterOption} />
            </ErrorBoundary>
          ) : null}
        </>
      )}
    </Box>
  );
};

export default KubernetesClusterOverview;

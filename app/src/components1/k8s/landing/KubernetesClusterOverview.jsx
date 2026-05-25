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
import ShimmerLoading from '@components1/common/ShimmerLoading';
import { v4 } from 'uuid';
import { snackbar } from '@components1/common/snackbarService';
import K8sAccountModal from '@components1/common/K8sAccountModal';
import CustomButton from '@components1/common/NewCustomButton';
import { hasWriteAccess } from '@lib/auth';

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
      padding: '16px 12px',
      flexDirection: 'column',
      borderRadius: '12px',
      background: '#FFF',
      border: '1px solid #E8ECF1',
      boxShadow: '0px 1px 3px rgba(16, 24, 40, 0.06), 0px 1px 2px rgba(16, 24, 40, 0.04)',
      transition: 'box-shadow 0.2s ease',
      '&:hover': {
        boxShadow: '0px 4px 8px rgba(16, 24, 40, 0.08), 0px 2px 4px rgba(16, 24, 40, 0.04)',
      },
    },
    clusterLayout: {
      mt: 1,
      gap: '20px',
      display: 'flex',
      flexDirection: 'column',
      marginBottom: '24px',
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
        .map((_) => <ShimmerLoading key={`shimmer-${v4()}`} isLoading={true} height='280px' width={'auto'} />);
    }
    return allcluster?.map((cluster) => (
      <ErrorBoundary key={cluster?.account_id}>
        <Box id={`cluster_box_${cluster.account_name}`} sx={styles.clusterCard}>
          <Box display='flex' gap='16px' alignItems='flex-start'>
            <Box sx={{ width: { xs: '260px', md: '320px' }, flexShrink: 0 }}>
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
              padding: '50px 32px',
              borderRadius: '12px',
              border: '1px solid #E4E4E4',
              background: '#FFF',
            }}
          >
            <Box
              sx={{
                width: 64,
                height: 64,
                borderRadius: '16px',
                background: 'linear-gradient(135deg, #EBF2FF 0%, #DBEAFE 100%)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                mb: 3,
                boxShadow: '0px 1px 3px rgba(16, 24, 40, 0.06)',
                '& svg': { filter: 'none', width: 36, height: 36 },
              }}
            >
              <K8sIcon />
            </Box>

            <Typography sx={{ fontSize: '18px', fontWeight: 600, color: '#101828', mb: 1, fontFamily: 'Poppins' }}>
              Get started with Kubernetes monitoring
            </Typography>
            <Typography sx={{ fontSize: '14px', color: '#667085', mb: 4, textAlign: 'center', maxWidth: '460px', lineHeight: 1.6 }}>
              Connect your cluster to gain full visibility into workloads, nodes, and resource usage - with actionable insights and cost optimization.
            </Typography>

            <Stack direction='row' spacing={4} sx={{ mb: 4 }}>
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
                <Box key={item.label} sx={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                  <Box
                    sx={{
                      width: 32,
                      height: 32,
                      borderRadius: '8px',
                      background: '#F5F8FF',
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
                  <Typography sx={{ fontSize: '13px', fontWeight: 500, color: '#344054', whiteSpace: 'nowrap' }}>{item.label}</Typography>
                </Box>
              ))}
            </Stack>

            {hasWriteAccess() ? (
              <CustomButton
                variant='primary'
                size='large'
                text='Add Cluster'
                sx={{ padding: '8px 20px' }}
                onClick={() => setShowAddClusterModal(true)}
              />
            ) : (
              <Typography sx={{ fontSize: '13px', color: '#667085', fontStyle: 'italic' }}>Need admin permission to connect a cluster</Typography>
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

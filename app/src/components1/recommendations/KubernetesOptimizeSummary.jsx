import { Box, Typography } from '@mui/material';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import React, { useEffect, useState, useCallback } from 'react';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import TextWithBorder from '@components1/common/TextWithBorder';
import recommendationApi, { NODE_RECOMMENDATION } from '@api1/recommendation';
import apiAutoPilot from '@api1/autoPilot';
import { useSession } from 'next-auth/react';
import { queryGraphQL } from '@lib/HttpService';
import { useRouter } from 'next/router';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import { hasWriteAccess } from '@lib/auth';
import { useData } from '@context/DataContext';
import { BetaIcon } from '@assets';
import { colors } from 'src/utils/colors';
import { formatNumber } from '@lib/formatter';
import apiAccount from '@api1/account';
import { Modal } from '@components1/ds/Modal';
import AutoOptimizeVerticalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeVerticalRightSizingSingleConfiguration';
import AutoOptimizeHorizontalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeHorizontalRightSizingSingleConfiguration';
import AutoOptimizePVRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizePVRightSizingSingleConfiguration';
import AutoOptimizeContinuousVerticalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeContinuousVerticalRightSizingSingleConfiguration';
import { snackbar } from '@components1/common/snackbarService';
import { Text } from '@components1/common';
import CustomTable from '@common-new/tables/CustomTable2';
import SafeIcon from '@components1/common/SafeIcon';
import Card from '@components1/ds/Card';
import { Stat } from '@components1/ds/Stat';
import { Skeleton } from '@components1/ds/Skeleton';
import { CostCallout } from '@components1/ds/CostCallout';
import HeadingWithBorder from '@common-new/HeadingWithBorder';
import { Button } from '@components1/ds/Button';
import { DropdownMenu } from '@components1/ds/DropdownMenu';
import { Divider } from '@components1/ds/Divider';
import { Comparison } from '@components1/ds/Comparison';

const initialStateSavingsData = [
  {
    title: 'Monthly Savings',
    value: '-',
    suffix: '/mo',
  },
  {
    title: 'Annual Savings',
    value: '-',
    suffix: '/yr',
  },
];

const nodeRecommendationInitialStateData = {
  id: '32',
  name: 'Node Config',
  description: 'Automated configuration recommendations for optimal Kubernetes node performance and resource management.',
  current_instance_type: {
    cost: '-',
    number_of_nodes: '-',
    total_cpu: '-',
    total_memory: '-',
    instance_types: ['-'],
    graviton: false,
  },
  recommended_instance_type: [
    {
      cost: '-',
      number_of_nodes: '-',
      total_cpu: '-',
      total_memory: '-',
      instance_types: ['-'],
      graviton: false,
    },
  ],
};

const initialStateData = [
  {
    pIdx: 1,
    category: 'Right Sizing',
    items: [
      {
        id: '11',
        name: 'Workload Right Sizing',
        description:
          'Workload right sizing involves optimizing resource allocation to match the specific demands of a workload, ensuring efficient performance and cost-effectiveness without over-provisioning or under-provisioning resources.',
        potentialSavings: {
          monthly: '-',
          yearly: '-',
        },
        optimizations: {
          new: 24,
          autoPilot: 0,
        },
        savedWithNB: 111,
        fragment: 'optimize/right-sizing', // tab: 1, subtab: 0,
        count: 0,
      },
      {
        id: '12',
        name: 'Replica Right Sizing',
        description:
          'Replica right sizing involves optimizing the number and size of instances in a distributed system to match current demand, ensuring high availability and performance while minimizing costs. This process dynamically adjusts resource allocation based on real-time metrics.',
        potentialSavings: {
          monthly: '-',
          yearly: '-',
        },
        optimizations: {
          new: 24,
          autoPilot: 0,
        },
        savedWithNB: 111,
        fragment: 'optimize/replica-rightsizing', // tab: 1, subtab: 5
        count: 0,
      },
      {
        id: '13',
        name: 'PV Right Sizing',
        description:
          'PV right sizing involves adjusting the capacity of Persistent Volumes (PVs) to match the storage needs of applications, ensuring efficient resource utilization and cost-effectiveness while avoiding over-provisioning or under-provisioning storage.',
        potentialSavings: {
          monthly: '-',
          yearly: '-',
        },
        optimizations: {
          new: 11,
          autoPilot: 0,
        },
        savedWithNB: 172,
        fragment: 'optimize/pv-rightsizing', // tab: 1, subtab: 4,
        count: 0,
      },
    ],
    autoPilot: {
      count: 0,
      execution: 0,
    },
  },
  {
    pIdx: 4,
    category: 'Abandoned Resources',
    items: [
      {
        id: '41',
        name: 'Unused Volume',
        description:
          'An unused volume refers to a Persistent Volume (PV) that is provisioned but not currently bound to any Persistent Volume Claim (PVC) or in use by any pod.',
        potentialSavings: {
          monthly: '-',
          yearly: '-',
        },
        optimizations: {
          new: 11,
          autoPilot: 0,
        },
        savedWithNB: 172,
        fragment: 'optimize/unused-volume', // tab: 1, subtab: 1,
        count: 0,
      },
      {
        id: '42',
        name: 'Abandoned Applications',
        description:
          'Abandoned applications refer to deployed applications that are no longer managed or monitored, often resulting in orphaned resources and potential security risks.',
        potentialSavings: {
          monthly: '-',
          yearly: '-',
        },
        optimizations: {
          new: 11,
          autoPilot: 0,
        },
        savedWithNB: 172,
        fragment: 'optimize/abandoned-resources', // tab: 1, subtab: 3,
        count: 0,
      },
    ],
    autoPilot: {
      count: 0,
      execution: 0,
    },
  },
  {
    pIdx: 3,
    category: 'Modernization',
    items: [
      {
        id: '31',
        name: 'Spot Instances',
        description: 'Workload right-sizing in Kubernetes optimizes resource',
        potentialSavings: {
          monthly: '-',
          yearly: '-',
        },
        optimizations: {
          new: 34,
          autoPilot: 0,
        },
        savedWithNB: 12,
        fragment: 'optimize/spot-recommendation', // tab: 1,  subtab: 6,
        count: 0,
      },
    ],
  },
];

const costItems = [
  { key: 'number_of_nodes', label: 'number of nodes' },
  { key: 'total_cpu', label: 'CPU' },
  { key: 'total_memory', label: 'GiB' },
  { key: 'instance_types', label: 'Instance Types' },
  { key: 'network_profile', label: 'Network Profile' },
];

const toComparisonValue = (raw) => {
  if (raw === undefined || raw === null || raw === '-') return null;
  if (Array.isArray(raw)) {
    const filtered = raw.filter((v) => v !== '-' && v !== null && v !== undefined);
    return filtered.length === 0 ? null : filtered.join(', ');
  }
  return raw;
};

const isFiniteNumber = (v) => typeof v === 'number' && Number.isFinite(v);

const COST_DASH_FONT_SIZE = {
  display: 'var(--ds-text-display)',
  lg: 'var(--ds-text-heading)',
  md: 'var(--ds-text-body)',
  sm: 'var(--ds-text-small)',
};

const CostOrDash = ({ value, size = 'md', ...props }) => {
  if (isFiniteNumber(value)) {
    return <CostCallout value={value} size={size} {...props} />;
  }
  return (
    <Box
      component='span'
      sx={{
        color: 'var(--ds-gray-400)',
        fontSize: COST_DASH_FONT_SIZE[size] || 'var(--ds-text-body)',
        fontWeight: 'var(--ds-font-weight-medium)',
      }}
    >
      —
    </Box>
  );
};

const KubernetesOptimizeSummary = () => {
  const [savingsData, setSavingsData] = useState(initialStateSavingsData);
  const [data, setData] = useState(initialStateData);
  const [averageTotalCost, setAverageTotalCost] = useState();
  const [loading, setLoading] = useState(false);
  const [eksUpgrade, setEksUpgrade] = useState({});
  const [openCreateAutoOptimizeType, setOpenCreateAutoOptimizeType] = React.useState(null);
  const [openCreateAutoOptimize, setOpenCreateAutoOptimize] = React.useState(false);
  const [msTeamsData, setMsTeamsData] = useState([]);
  const [isMsTeamsLoading, setIsMsTeamsLoading] = useState(false);
  const [googleChannelList, setGoogleChannelList] = useState([]);
  const [isGoogleChannelsLoading, setIsGoogleChannelsLoading] = useState(false);
  const [nodeRecommendation, setNodeRecommendation] = useState(nodeRecommendationInitialStateData);

  const { selectedCluster } = useData();
  const { data: session } = useSession();
  const includeGraviton = selectedCluster?.k8s_provider === 'EKS';
  const router = useRouter();

  const updateSavingsData = (optimizeSummary) => {
    const totalEstimatedSavings = calculateTotalEstimatedSavings(optimizeSummary?.data);
    const updates = {
      0: totalEstimatedSavings,
      1: totalEstimatedSavings * 12,
    };

    // CHANGE: Use functional update (prevSavingsData)
    setSavingsData((prevSavingsData) => prevSavingsData.map((item, idx) => (updates[idx] !== undefined ? { ...item, value: updates[idx] } : item)));
  };

  const handleOpenCreateAutoOptimize = (type) => {
    setOpenCreateAutoOptimizeType(type);
    setOpenCreateAutoOptimize(true);
  };

  const closeAutoPilotSingleConfigModal = (success) => {
    if (success) {
      snackbar.success('Auto Optimize Updated Successfully');
    }
    setOpenCreateAutoOptimizeType('');
    setOpenCreateAutoOptimize(false);
  };

  useEffect(() => {
    const fetchMsTeamsChannels = async () => {
      if (msTeamsData.length === 0) {
        setIsMsTeamsLoading(true);
        try {
          const res = await apiAccount.getNotificationChannelList('ms_teams');
          const teamOptions =
            res?.data?.data?.map((item) => ({
              label: item.name,
              value: item.id,
              channels: item.channels,
            })) || [];
          setMsTeamsData(teamOptions);
        } finally {
          setIsMsTeamsLoading(false);
        }
      }
    };

    const fetchGoogleChatChannels = async () => {
      if (googleChannelList.length === 0) {
        setIsGoogleChannelsLoading(true);
        try {
          const res = await apiAccount.getNotificationChannelList('google_chat');
          const chatOptions =
            res?.data?.data?.map((item) => ({
              label: item.name,
              value: item.id,
            })) || [];
          setGoogleChannelList(chatOptions);
        } finally {
          setIsGoogleChannelsLoading(false);
        }
      }
    };

    if (openCreateAutoOptimize) {
      fetchMsTeamsChannels();
      fetchGoogleChatChannels();
    }
  }, [openCreateAutoOptimize, msTeamsData.length, googleChannelList.length]);

  const updatePotentialSavingsById = (updates, currentData) => {
    return currentData.map((category) => ({
      ...category,
      items: category.items.map((item) => {
        const update = updates.find((update) => update.id === item.id);
        if (update) {
          return {
            ...item,
            potentialSavings: {
              ...item.potentialSavings,
              monthly: update.newMonthly,
              yearly: update.newYearly,
            },
            count: update.count,
          };
        }
        return item;
      }),
    }));
  };

  const updateAutoPilotById = (updatedData, updates) => {
    return updatedData.map((category) => ({
      ...category,
      items: category.items.map((item) => {
        const update = updates.find((update) => update.id === item.id);
        if (update) {
          return {
            ...item,
            optimizations: {
              ...item.optimizations,
              autoPilot: update.autoPilot,
            },
          };
        }
        return item;
      }),
    }));
  };

  const updateAutoPilotByPId = (data, pIdx, count, execution) => {
    return data.map((item) => {
      if (item.pIdx === pIdx) {
        return {
          ...item,
          autoPilot: {
            count: count,
            execution: execution,
          },
        };
      }
      return item;
    });
  };

  const updateNodeRecommendationState = (updates) => {
    const current_instance_type = updates.current_instance_type || {};
    const recommended_instance_type = updates.recommended_instance_type || [];

    // Helper to format instance types
    const formatInstanceTypes = (types = ['-']) => {
      const counts = {};
      types.forEach((t) => (counts[t] = (counts[t] || 0) + 1));
      return Object.entries(counts).map(([type, count]) => `${count} : ${type}`);
    };

    const currentCost = current_instance_type?.cost ? current_instance_type.cost * 40 * 4.33 : '-';

    // CHANGE: Use functional update (prevNodeRecommendation)
    setNodeRecommendation((prevNodeRecommendation) => ({
      ...prevNodeRecommendation,
      current_instance_type: {
        cost: currentCost,
        number_of_nodes: current_instance_type.number_of_nodes ?? '-',
        total_cpu: current_instance_type.total_cpu ?? '-',
        total_memory: current_instance_type.total_memory ?? '-',
        instance_types: formatInstanceTypes(current_instance_type.instance_types),
        graviton: current_instance_type.graviton ?? false,
      },
      recommended_instance_type:
        recommended_instance_type?.map((type) => ({
          cost: type.cost ? type.cost * 40 * 4.33 : '-',
          number_of_nodes: type.number_of_nodes || '-',
          total_cpu: type.total_cpu || '-',
          total_memory: type.total_memory || '-',
          instance_types: formatInstanceTypes(type.instance_types),
          graviton: type.graviton || false,
          percent: currentCost !== '-' && type.cost ? ((currentCost - type.cost * 40 * 4.33) / currentCost) * 100 : '-',
        })) || [],
    }));
  };

  const loadInforgraphicData = useCallback(async () => {
    if (!selectedCluster.value) {
      return;
    }
    try {
      const optimizeSummary = await recommendationApi.optimizeSummaryInfographic(selectedCluster.value);
      const autoPilotData = await apiAutoPilot.getAutoPilotAggregate({ accountId: selectedCluster.value });
      if (selectedCluster?.k8s_provider === 'EKS') {
        const clusterUpgrade = await recommendationApi.getK8sRecommendation({
          accountId: selectedCluster.value,
          ruleName: 'eks_cluster_upgrade',
          category: 'InfraUpgrade',
          status: ['Open'],
          recommendation: {},
          limit: 1,
          offset: 0,
          fetchTicket: false,
        });
        const recommendationObject = clusterUpgrade?.data?.recommendation?.[0]?.recommendation || {};
        if (Object.keys(recommendationObject).length > 0) {
          setEksUpgrade(recommendationObject);
        }
      }
      updateDataStates(optimizeSummary, autoPilotData);
    } catch (error) {
      console.error('Failed to load infographic data:', error);
    }
  }, [selectedCluster]);

  const loadInforgraphicNodeRecommendationData = useCallback(async () => {
    if (selectedCluster.value) {
      const nodeRecommendation = await getNodeRecommendation(selectedCluster, includeGraviton);
      updateNodeRecommendationState(nodeRecommendation?.ml_generate_node_recommendations?.data || {});
    }
  }, [selectedCluster]);

  useEffect(() => {
    setData(initialStateData);
    setNodeRecommendation(nodeRecommendationInitialStateData);
    setSavingsData(initialStateSavingsData);
    setAverageTotalCost();
    setLoading(true);
    setEksUpgrade({});
    // Load data in parallel - both APIs will be called simultaneously
    Promise.all([loadInforgraphicData(), loadInforgraphicNodeRecommendationData()]).finally(() => {
      setLoading(false);
    });
  }, [selectedCluster, loadInforgraphicData, loadInforgraphicNodeRecommendationData]);

  const getNodeRecommendation = async (selectedCluster, includeGraviton) => {
    const tenantId = session?.tenant?.id;
    if (!tenantId) {
      console.error('NodeRecommendation skipped - no active tenant session found');
      return {};
    }
    const response = await queryGraphQL(NODE_RECOMMENDATION, 'NodeRecommendation', {
      account: selectedCluster.value,
      graviton: includeGraviton,
      instance_groups: selectedCluster?.k8s_provider === 'EKS' ? ['m', 'c', 'r'] : [],
      tenant_id: tenantId,
      number_of_recommendations: 1,
    });
    return response?.data?.data ?? {};
  };

  const calculateCurrentMonthNumber = () => {
    const date = new Date();
    return date.getMonth() + 1;
  };

  const updateDataStates = (optimizeSummary, autoPilotData) => {
    const currentMonth = calculateCurrentMonthNumber();
    let monthsToAverage = currentMonth;

    if (selectedCluster?.created_at) {
      const clusterDate = new Date(selectedCluster.created_at);
      const clusterYear = clusterDate.getFullYear();
      const clusterMonth = clusterDate.getMonth() + 1;
      const currentYear = new Date().getFullYear();

      if (currentYear === clusterYear && clusterMonth <= currentMonth) {
        monthsToAverage = currentMonth - clusterMonth + 1;
      }
      monthsToAverage = Math.max(1, monthsToAverage);
    }

    setAverageTotalCost(optimizeSummary?.data?.spends_aggregate?.aggregate?.sum?.amount / monthsToAverage);
    updateSavingsData(optimizeSummary);

    // CHANGE: Use functional update to ensure we work on the latest data structure
    setData((prevData) => {
      // Pass prevData into the update function
      let updatedData = updatePotentialSavings(optimizeSummary, prevData);
      // Pass the result into the next function
      updatedData = updateAutoPilotData(updatedData, autoPilotData);
      return updatedData;
    });
  };

  const calculateTotalEstimatedSavings = (data) => {
    return Object.values(data ?? {})
      .filter((item) => item?.aggregate?.sum?.estimated_savings !== undefined)
      .reduce(
        (
          acc,
          {
            aggregate: {
              sum: { estimated_savings },
            },
          }
        ) => acc + estimated_savings,
        0
      );
  };

  const updatePotentialSavings = (optimizeSummary, currentData) => {
    const { data } = optimizeSummary;
    return updatePotentialSavingsById(
      [
        createSavingItem('11', data?.workload_rightsize),
        createSavingItem('12', data?.replica_rightsize),
        createSavingItem('13', data?.pv_rightsize),
        createSavingItem('31', data?.spot_instance),
        createSavingItem('41', data?.unused_pvc),
        createSavingItem('42', data?.abandoned_resource),
      ],
      currentData
    );
  };

  const createSavingItem = (id, data) => ({
    id,
    newMonthly: data?.aggregate?.sum?.estimated_savings ?? 0,
    newYearly: (data?.aggregate?.sum?.estimated_savings ?? 0) * 12,
    count: data?.aggregate?.count ?? 0,
  });

  const updateAutoPilotData = (data, autoPilotData) => {
    const autoPilotCount = autoPilotData?.auto_pilot_aggregate?.aggregate.count;
    const autoPilotTaskCount = autoPilotData?.auto_pilot_task_aggregate?.aggregate.count;

    let updatedData = updateAutoPilotById(data, [{ id: '1', autoPilot: autoPilotCount }]);
    return updateAutoPilotByPId(updatedData, 1, autoPilotCount, autoPilotTaskCount);
  };

  return (
    <>
      {openCreateAutoOptimize && openCreateAutoOptimizeType === 'continuous_rightsize' && (
        <Modal
          width='md'
          open={openCreateAutoOptimize}
          handleClose={() => closeAutoPilotSingleConfigModal(false)}
          title={'Auto Optimize Configuration - Vertical RightSizing'}
          loader={loading}
        >
          <AutoOptimizeContinuousVerticalRightSizingSingleConfiguration
            autoOptimizeData={{}}
            closeAutoPilotSingleConfigModal={closeAutoPilotSingleConfigModal}
            msTeamsData={msTeamsData}
            isMsTeamsLoading={isMsTeamsLoading}
            googleChannelList={googleChannelList}
            isGoogleChannelsLoading={isGoogleChannelsLoading}
            setIsLoading={setLoading}
          />
        </Modal>
      )}
      {openCreateAutoOptimize && openCreateAutoOptimizeType === 'vertical_rightsize' && (
        <Modal
          width='md'
          open={openCreateAutoOptimize}
          handleClose={() => closeAutoPilotSingleConfigModal(false)}
          title={'Auto Optimize Configuration - Scheduled Vertical RightSizing'}
          loader={loading}
        >
          <AutoOptimizeVerticalRightSizingSingleConfiguration
            autoOptimizeData={{}}
            closeAutoPilotSingleConfigModal={closeAutoPilotSingleConfigModal}
            msTeamsData={msTeamsData}
            isMsTeamsLoading={isMsTeamsLoading}
            googleChannelList={googleChannelList}
            isGoogleChannelsLoading={isGoogleChannelsLoading}
            setIsLoading={setLoading}
            currentData={{}}
          />
        </Modal>
      )}
      {openCreateAutoOptimize && openCreateAutoOptimizeType === 'horizontal_rightsize' && (
        <Modal
          width='lg'
          open={openCreateAutoOptimize}
          handleClose={() => closeAutoPilotSingleConfigModal(false)}
          title={'Auto Optimize - Replica Rightsizing'}
          loader={loading}
        >
          <AutoOptimizeHorizontalRightSizingSingleConfiguration
            autoOptimizeData={{}}
            closeAutoPilotSingleConfigModal={closeAutoPilotSingleConfigModal}
            msTeamsData={msTeamsData}
            isMsTeamsLoading={isMsTeamsLoading}
            googleChannelList={googleChannelList}
            isGoogleChannelsLoading={isGoogleChannelsLoading}
            setIsLoading={setLoading}
          />
        </Modal>
      )}
      {openCreateAutoOptimize && openCreateAutoOptimizeType === 'pvc_rightsize' && (
        <Modal
          width='md'
          open={openCreateAutoOptimize}
          handleClose={() => closeAutoPilotSingleConfigModal(false)}
          title={'Auto Optimize - Persistent Volume Claim Rightsizing'}
          loader={loading}
        >
          <AutoOptimizePVRightSizingSingleConfiguration
            autoOptimizeData={{}}
            closeAutoPilotSingleConfigModal={closeAutoPilotSingleConfigModal}
            msTeamsData={msTeamsData}
            isMsTeamsLoading={isMsTeamsLoading}
            googleChannelList={googleChannelList}
            isGoogleChannelsLoading={isGoogleChannelsLoading}
            setIsLoading={setLoading}
          />
        </Modal>
      )}
      <Box
        sx={{
          display: 'flex',
          gap: 'var(--ds-space-3)',
          mt: 'var(--ds-space-3)',
        }}
      >
        <Card variant='elevated' size='md' sx={{ flex: 1, mt: 0 }}>
          <Stat
            label='Average cost'
            size='hero'
            value={
              loading ? (
                <Skeleton shape='text' size='heading' width={160} />
              ) : (
                <CostOrDash value={averageTotalCost} tone='neutral' size='display' period=' / mo' />
              )
            }
          />
        </Card>
        {savingsData.map((item, idx) => (
          <Card key={item.title} variant='elevated' size='md' sx={{ flex: 1, mt: 0 }}>
            <Stat
              label={item.title}
              size='hero'
              value={
                loading ? (
                  <Skeleton shape='text' size='heading' width={160} />
                ) : (
                  <CostOrDash value={item.value} tone={idx === 1 ? 'high-savings' : 'medium-savings'} size='display' period={` ${item.suffix}`} />
                )
              }
            />
          </Card>
        ))}
      </Box>
      {data.map((category) => {
        return (
          <Box
            key={category.pIdx}
            sx={{
              display: 'grid',
              gridTemplateColumns: 'repeat(12, 1fr)',
              gap: 1,
              mt: 'var(--ds-space-3)',
            }}
          >
            <Box sx={{ gridColumn: 'span 9' }}>
              <Card variant='elevated' size='md'>
                <Box>
                  <HeadingWithBorder
                    value={category.category}
                    sx={{ color: 'var(--ds-gray-700)' }}
                    borderWidth='3px'
                    borderColor='var(--ds-brand-600)'
                  />
                  {category.items.map((item) => (
                    <Box
                      key={item.name}
                      sx={{
                        padding: 'var(--ds-space-3)',
                        display: 'grid',
                        gridTemplateColumns: '380px 250px 1fr',
                        gridColumnGap: '30px',
                        '@media (max-width: 1500px)': {
                          gridTemplateColumns: '285px 200px 1fr',
                        },
                        '@media (max-width: 1250px)': {
                          gridTemplateColumns: '230px 185px 1fr',
                          '@media (max-width: 1200px)': {
                            gridColumnGap: '10px',
                            padding: 'var(--ds-space-3) 0px',
                          },
                        },
                      }}
                    >
                      <Box>
                        <Box
                          sx={{
                            display: 'flex',
                            alignItems: 'center',
                            '& img:hover': {
                              cursor: 'pointer',
                            },
                          }}
                        >
                          <HeadingWithBorder
                            value={`${item.name} (${item.count})`}
                            borderWidth='2px'
                            borderColor='var(--ds-brand-600)'
                            sx={{ '& p': { fontSize: 'var(--ds-text-title) !important', color: 'var(--ds-gray-700)' } }}
                          />
                        </Box>
                        <Typography
                          color={colors.text.secondaryDark}
                          fontSize={'12px'}
                          fontWeight={400}
                          pt={'10px'}
                          pr={'40px'}
                          sx={{
                            '@media (max-width: 1250px)': {
                              pr: 'var(--ds-space-2)',
                            },
                          }}
                        >
                          {item.description}
                        </Typography>
                      </Box>
                      <Box pt='var(--ds-space-2)'>
                        {loading ? (
                          <Skeleton shape='text' size='heading' width={140} />
                        ) : (
                          <Box
                            sx={{
                              display: 'flex',
                              alignItems: 'center',
                              gap: 'var(--ds-space-3)',
                              '@media (max-width: 1250px)': {
                                gap: 'var(--ds-space-2)',
                              },
                            }}
                          >
                            <CostOrDash value={item.potentialSavings.monthly} tone='medium-savings' size='lg' period=' / mo' />
                            <Divider orientation='vertical' sx={{ height: '24px' }} />
                            <CostOrDash value={item.potentialSavings.yearly} tone='high-savings' size='lg' period=' / yr' />
                          </Box>
                        )}
                        <Typography
                          sx={{
                            color: 'var(--ds-gray-600)',
                            fontSize: 'var(--ds-text-small)',
                            fontWeight: 'var(--ds-font-weight-regular)',
                            pt: 'var(--ds-space-1)',
                          }}
                        >
                          Potential Savings
                        </Typography>
                      </Box>
                      <Box
                        display={'flex'}
                        justifyContent={'space-between'}
                        sx={{
                          '& .borderedBox': {
                            borderLeft: `0.5px solid ${colors.border.vertical}`,
                            paddingLeft: 'var(--ds-space-6)',
                            pt: 'var(--ds-space-2)',
                            '@media (max-width: 1500px)': {
                              borderLeft: '0px ',
                              paddingLeft: 'var(--ds-space-6)',
                            },
                            '@media (max-width: 1200px)': {
                              paddingLeft: 'var(--ds-space-2)',
                            },
                          },
                        }}
                      >
                        <Box className='borderedBox' sx={{ marginLeft: 'var(--ds-space-4)' }}>
                          <Button
                            tone='secondary'
                            size='md'
                            trailingAccent={<ArrowForwardIcon fontSize='small' />}
                            onClick={() => {
                              router.push(`/kubernetes/details/${selectedCluster.value}?accountId=${selectedCluster.value}#${item.fragment}`);
                            }}
                          >
                            Optimize
                          </Button>
                        </Box>
                        {item.optimizations.autoPilot ? (
                          <Box className='borderedBox'>
                            <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 'var(--ds-space-1)' }}>
                              <Typography
                                component='span'
                                sx={{
                                  color: 'var(--ds-gray-700)',
                                  fontSize: 'var(--ds-text-heading)',
                                  fontWeight: 'var(--ds-font-weight-semibold)',
                                }}
                              >
                                {formatNumber(item.optimizations.autoPilot, '-', 0, 0)}
                              </Typography>
                              <Typography
                                component='span'
                                sx={{
                                  color: 'var(--ds-gray-600)',
                                  fontSize: 'var(--ds-text-small)',
                                  fontWeight: 'var(--ds-font-weight-regular)',
                                }}
                              >
                                Auto Optimize
                              </Typography>
                            </Box>
                          </Box>
                        ) : null}
                      </Box>
                    </Box>
                  ))}
                  {category.pIdx === 3 && (
                    <>
                      <Divider sx={{ my: 'var(--ds-space-3)' }} />
                      <Box
                        sx={{
                          padding: 'var(--ds-space-3) var(--ds-space-3) 0',
                          '@media (max-width: 1200px)': { padding: 'var(--ds-space-3) 0 0' },
                        }}
                      >
                        <Box>
                          <HeadingWithBorder
                            value={nodeRecommendation.name}
                            borderWidth='2px'
                            borderColor='var(--ds-brand-600)'
                            releaseIcon={BetaIcon}
                            sx={{ '& p': { fontSize: 'var(--ds-text-title) !important', color: 'var(--ds-gray-700)' } }}
                          />
                          <Typography
                            sx={{
                              color: 'var(--ds-gray-600)',
                              fontSize: 'var(--ds-text-small)',
                              fontWeight: 'var(--ds-font-weight-regular)',
                              pt: 'var(--ds-space-2)',
                              pr: 'var(--ds-space-6)',
                              '@media (max-width: 1250px)': { pr: 'var(--ds-space-2)' },
                            }}
                          >
                            {nodeRecommendation.description}
                          </Typography>
                        </Box>
                        <Box sx={{ mt: 'var(--ds-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
                          {loading ? (
                            <Skeleton shape='rect' height={180} width='100%' />
                          ) : !nodeRecommendation?.recommended_instance_type?.some((rec) => rec.cost !== '-') ? (
                            <Card variant='tinted' tone='neutral' size='md'>
                              <Stat
                                label='Current Cost'
                                size='hero'
                                value={
                                  <CostOrDash value={nodeRecommendation.current_instance_type?.cost} tone='neutral' size='display' period=' / mo' />
                                }
                              />
                              <Divider sx={{ my: 'var(--ds-space-4)' }} />
                              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
                                {costItems.map((attr) => {
                                  const value = toComparisonValue(nodeRecommendation.current_instance_type?.[attr.key]);
                                  return (
                                    <Box
                                      key={attr.key}
                                      sx={{
                                        display: 'flex',
                                        alignItems: 'baseline',
                                        gap: 'var(--ds-space-2)',
                                        fontSize: 'var(--ds-text-body)',
                                      }}
                                    >
                                      <Box
                                        component='span'
                                        sx={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-small)', fontStyle: 'italic', minWidth: '120px' }}
                                      >
                                        {attr.label}
                                      </Box>
                                      <Box
                                        component='span'
                                        sx={{
                                          color: value === null ? 'var(--ds-gray-400)' : 'var(--ds-gray-700)',
                                          fontVariantNumeric: 'tabular-nums',
                                        }}
                                      >
                                        {value === null ? '—' : value}
                                      </Box>
                                    </Box>
                                  );
                                })}
                              </Box>
                            </Card>
                          ) : (
                            (nodeRecommendation?.recommended_instance_type ?? []).map((rec, idx) => {
                              const current = nodeRecommendation.current_instance_type ?? {};
                              const recKey = rec.graviton ? `graviton-${idx}` : `standard-${idx}`;
                              return (
                                <Box key={recKey} sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
                                  <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--ds-space-3)' }}>
                                    <Card variant='outlined' size='md' elevation='flat'>
                                      <Stat
                                        label='Current Cost'
                                        size='hero'
                                        value={<CostOrDash value={current.cost} tone='neutral' size='display' period=' / mo' />}
                                      />
                                    </Card>
                                    <Card variant='tinted' tone='success' size='md'>
                                      <Stat
                                        label={rec.graviton ? 'Optimized Cost (Graviton)' : 'Optimized Cost'}
                                        size='hero'
                                        value={<CostOrDash value={rec.cost} tone='high-savings' size='display' period=' / mo' />}
                                        sub={rec.percent && rec.percent > 0 ? `${rec.percent.toFixed()}% savings` : undefined}
                                      />
                                    </Card>
                                  </Box>
                                  <Box
                                    sx={{
                                      display: 'flex',
                                      flexDirection: 'column',
                                      gap: 'var(--ds-space-2)',
                                      padding: 'var(--ds-space-3) var(--ds-space-4)',
                                    }}
                                  >
                                    {costItems.map((attr) => {
                                      const before = toComparisonValue(current[attr.key]);
                                      const after = toComparisonValue(rec[attr.key]);
                                      if (before === null && after === null) return null;
                                      return (
                                        <Comparison
                                          key={attr.key}
                                          label={attr.label}
                                          before={{ value: before }}
                                          after={{ value: after }}
                                          polarity='neutral'
                                          size='md'
                                          layout='inline'
                                        />
                                      );
                                    })}
                                  </Box>
                                </Box>
                              );
                            })
                          )}
                        </Box>
                      </Box>
                    </>
                  )}
                </Box>
              </Card>
            </Box>
            {category.pIdx === 1 ? (
              <Box sx={{ gridColumn: 'span 3' }}>
                <Card variant='elevated' size='md'>
                  <HeadingWithBorder
                    value='Auto Optimize Response'
                    borderColor='var(--ds-brand-600)'
                    borderWidth='3px'
                    sx={{
                      '& p': {
                        fontSize: 'var(--ds-text-heading)',
                        fontWeight: 'var(--ds-font-weight-semibold)',
                        color: 'var(--ds-gray-700)',
                      },
                    }}
                  />
                  <Box
                    sx={{
                      display: 'flex',
                      flexDirection: 'column',
                      gap: 'var(--ds-space-4)',
                    }}
                  >
                    <Box>
                      <Box sx={{ mt: 'var(--ds-space-5)' }}>
                        <Stat
                          label='Auto Optimize Configured'
                          size='hero'
                          value={loading ? <Skeleton shape='text' size='heading' width={80} /> : formatNumber(category.autoPilot?.count, '-', 0, 0)}
                        />
                      </Box>
                      <Divider sx={{ my: 'var(--ds-space-4)' }} />
                      <Box sx={{ mt: 'var(--ds-space-5)' }}>
                        <Stat
                          label='Response Triggered Last 7 Days'
                          size='hero'
                          value={
                            loading ? <Skeleton shape='text' size='heading' width={80} /> : formatNumber(category.autoPilot?.execution, '-', 0, 0)
                          }
                        />
                      </Box>
                    </Box>

                    <Box
                      sx={{
                        display: 'flex',
                        justifyContent: 'flex-end',
                        gap: 'var(--ds-space-2)',
                        '@media (max-width: 1470px)': {
                          flexWrap: 'wrap',
                          justifyContent: 'left',
                        },
                      }}
                    >
                      {hasWriteAccess(selectedCluster.value) && (
                        <DropdownMenu
                          trigger={
                            <Button tone='primary' size='md' icon={<KeyboardArrowDownIcon />} iconPlacement='end'>
                              Add Auto Optimize
                            </Button>
                          }
                          items={[
                            {
                              label: (
                                <Box component='span' sx={{ display: 'flex', alignItems: 'center' }}>
                                  Continuous Vertical Right Sizing
                                  <SafeIcon src={BetaIcon} alt='Beta Icon' width={25} height={20} style={{ marginLeft: 'var(--ds-space-1)' }} />
                                </Box>
                              ),
                              onSelect: () => handleOpenCreateAutoOptimize('continuous_rightsize'),
                            },
                            { label: 'Horizontal Right Sizing', onSelect: () => handleOpenCreateAutoOptimize('horizontal_rightsize') },
                            { label: 'Scheduled Vertical Right Sizing', onSelect: () => handleOpenCreateAutoOptimize('vertical_rightsize') },
                            { label: 'PVC Right Sizing', onSelect: () => handleOpenCreateAutoOptimize('pvc_rightsize') },
                          ]}
                        />
                      )}
                      <Button
                        tone='secondary'
                        size='md'
                        onClick={() => {
                          router.push(`/auto-pilot?accountId=${selectedCluster.value}#auto-optimize/optimizations`);
                        }}
                      >
                        View all
                      </Button>
                    </Box>
                  </Box>
                </Card>
              </Box>
            ) : (
              <></>
            )}
          </Box>
        );
      })}
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: 'repeat(12, 1fr)',
          gap: 1,
          mt: 'var(--ds-space-6)',
        }}
      >
        <Box sx={{ gridColumn: 'span 9' }}>
          {selectedCluster.k8s_provider === 'EKS' && Object.keys(eksUpgrade).length > 0 ? (
            <SummaryBlock
              hideTitle={true}
              height='100%'
              sx={{
                height: '100%',
                border: '0.5px solid transparent !important',
                backgroundColor: colors.background.white,
                boxShadow: `${colors.shadow.softGray} !important`,
              }}
            >
              <Box>
                <TextWithBorder value={'EKS Upgrade'} borderWidth={'3px'} borderColor={colors.border.primary} borderStyle={'solid'} />
                <Box display='flex' flexDirection='column' alignItems='flex-start' borderRight={`0.5px solid ${colors.border.vertical}`} pr={'20px'}>
                  <Text
                    sx={{
                      color: 'red',
                    }}
                    value={eksUpgrade.message}
                  />
                  <CustomTable
                    tableData={[
                      [
                        {
                          component: <Text value={eksUpgrade.eks_version} />,
                        },
                        {
                          component: <Text value={eksUpgrade.end_of_support.eks_release} />,
                        },
                        {
                          component: <Text value={eksUpgrade.end_of_support.end_of_extended_support} />,
                        },
                        {
                          component: <Text value={eksUpgrade.end_of_support.end_of_standard_support} />,
                        },
                        {
                          component: <Text value={`${eksUpgrade.estimated_savings}$`} />,
                        },
                      ],
                    ]}
                    headers={['EKS Version', 'EKS Release', 'End Of Extended Support', 'End Of Standard Support', 'Savings']}
                    totalRows={1}
                    rowsPerPage={1}
                  />
                </Box>
              </Box>
            </SummaryBlock>
          ) : null}
        </Box>
      </Box>
    </>
  );
};

export default KubernetesOptimizeSummary;

import Currency from '@components1/common/format/Currency';
import { Box, Typography, Divider } from '@mui/material';
import React, { useEffect, useState, useCallback } from 'react';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import SummaryWidget from '@components1/optimise/SummaryWidget';
import TextWithBorder from '@components1/common/TextWithBorder';
import recommendationApi from '@api1/recommendation';
import apiAutoPilot from '@api1/autoPilot';
import { useRouter } from 'next/router';
import OptimizeIcon from '@assets/OptimizeIcon';
import { hasWriteAccess } from '@lib/auth';
import { useData } from '@context/DataContext';
import { BetaIcon } from '@assets';
import CustomButton from '@components1/common/NewCustomButton';
import ThreeDotLoader from '@components1/common/ThreeDotLoader';
import { colors } from 'src/utils/colors';
import { formatNumber } from '@lib/formatter';
import ButtonMenu from '@components1/common/ButtonMenu';
import apiAccount from '@api1/account';
import { v4 as uuidv4 } from 'uuid';
import { Modal } from '@components1/common/modal';
import AutoOptimizeVerticalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeVerticalRightSizingSingleConfiguration';
import AutoOptimizeHorizontalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeHorizontalRightSizingSingleConfiguration';
import AutoOptimizePVRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizePVRightSizingSingleConfiguration';
import AutoOptimizeContinuousVerticalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeContinuousVerticalRightSizingSingleConfiguration';
import { snackbar } from '@components1/common/snackbarService';
import { Text } from '@components1/common';
import CustomTable from '@components1/common/tables/CustomTable2';
import SafeIcon from '@components1/common/SafeIcon';

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

const CostBlock = ({ title, data, borderColor, backgroundColor }) => {
  if (!data) {
    return null;
  }

  return (
    <SummaryBlock
      hideTitle={true}
      sx={{
        border: `0.5px solid ${borderColor} !important`,
        backgroundColor: backgroundColor,
        mb: '24px',
        padding: '12px 16px',
      }}
    >
      <Typography color={colors.text.secondaryDark} fontSize={'12px'} fontWeight={400} mb={'2px'}>
        {title}
      </Typography>
      <Currency
        value={data.cost}
        sx={{ color: colors.text.secondary, fontSize: '28px', lineHeight: '32.81px', fontWeight: 600 }}
        suffix={'/mo'}
        withTooltip={false}
        sxPrefix={{ color: colors.text.secondary, marginRight: '5px' }}
        percent={data.percent && data.percent > 0 ? data.percent.toFixed() + '%' : ''}
      />
      <Box mt={'12px'}>
        {costItems.map(
          (item) =>
            data[item.key] !== undefined && (
              <TextWithBorder
                key={item.key}
                value={Array.isArray(data[item.key]) ? data[item.key].join(', ') : data?.[item.key]?.toFixed?.() ?? '-'}
                span={item.label}
                borderWidth={'2px'}
                borderColor={borderColor === colors.border.secondary ? colors.border.secondary : colors.done}
                borderStyle={'solid'}
                sx={{ '& p': { fontSize: '12px !important', fontWeight: 400, color: colors.text.secondary } }}
                spanSx={{ fontSize: '12px !important', fontWeight: 400, color: colors.text.secondaryDark }}
              />
            )
        )}
      </Box>
    </SummaryBlock>
  );
};

const Loader = () => (
  <Box display='flex' justifyContent='center' alignItems='center' width='100%'>
    <ThreeDotLoader />
  </Box>
);

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
      updateNodeRecommendationState(nodeRecommendation?.generate_node_recommendations?.data || {});
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
    const response = await fetch('/api/recommendation/node-optimize', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        accountId: selectedCluster.value,
        graviton: includeGraviton,
        instance_groups: selectedCluster?.k8s_provider === 'EKS' ? ['m', 'c', 'r'] : [],
        number_of_recommendations: 1,
      }),
    });
    return await response.json();
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
      <Box sx={{ display: 'flex', gap: '12px' }} mt={2} mb={2}>
        <SummaryWidget
          title='Average cost'
          value={
            loading ? (
              <Loader />
            ) : (
              <Currency
                value={averageTotalCost}
                sx={{ color: colors.text.secondary, fontSize: '28px', lineHeight: '28px', fontWeight: 600 }}
                suffix={'/mo'}
                withTooltip={false}
                sxPrefix={{ color: colors.text.secondary, marginRight: '5px' }}
              />
            )
          }
        />
        {savingsData.map((item) => (
          <SummaryWidget
            key={item.title}
            title={item.title}
            variant='savings'
            value={
              loading ? (
                <Loader />
              ) : (
                <Currency
                  value={item.value}
                  sx={{ color: colors.text.secondary, fontSize: '28px', lineHeight: '28px', fontWeight: 600 }}
                  suffix={item.suffix}
                  withTooltip={false}
                  sxPrefix={{ color: colors.text.secondary, marginRight: '5px' }}
                />
              )
            }
          />
        ))}
      </Box>
      {data.map((category, _) => {
        return (
          <Box
            key={category.pIdx}
            sx={{
              display: 'grid',
              gridTemplateColumns: 'repeat(12, 1fr)',
              gap: 1,
              mt: '1px',
            }}
          >
            <Box sx={{ gridColumn: 'span 9' }}>
              <SummaryBlock
                key={category.pIdx}
                hideTitle={true}
                height='100%'
                sx={{
                  height: '100%',
                  border: '0.5px solid transparent !important',
                  backgroundColor: colors.background.white,
                  boxShadow: `${colors.shadow.softGray} !important`,
                  mt: '24px',
                }}
              >
                <Box>
                  <TextWithBorder
                    value={category.category}
                    sx={{ color: colors.text.secondary }}
                    borderWidth={'3px'}
                    borderColor={colors.border.primary}
                    borderStyle={'solid'}
                  />
                  {category.items.map((item) => (
                    <Box
                      key={item.name}
                      sx={{
                        padding: '14px',
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
                            padding: '14px 0px',
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
                          <TextWithBorder
                            value={`${item.name} (${item.count})`}
                            borderWidth={'2px'}
                            borderColor={colors.nudgebeeMain}
                            borderStyle={'solid'}
                            sx={{ '& p': { fontSize: '16px !important', color: colors.text.secondary } }}
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
                              pr: '10px',
                            },
                          }}
                        >
                          {item.description}
                        </Typography>
                      </Box>
                      <Box pt='8px'>
                        {loading ? (
                          <Loader />
                        ) : (
                          <Box
                            display={'flex'}
                            gap={'30px'}
                            sx={{
                              '@media (max-width: 1250px)': {
                                gap: '10px',
                              },
                            }}
                          >
                            <Box width={85}>
                              <Currency
                                value={item.potentialSavings.monthly}
                                sx={{ color: colors.text.secondary, fontSize: '24px', lineHeight: '34px', fontWeight: 600 }}
                                suffix={'/mo'}
                                withTooltip={false}
                                sxSuffix={{ color: colors.text.secondaryDark, marginLeft: '5px' }}
                              />
                            </Box>
                            <TextWithBorder
                              borderWidth={'1px'}
                              borderColor={colors.border.secondary}
                              borderStyle={'solid'}
                              padding='0px !important'
                            />
                            <Box width={85}>
                              <Currency
                                value={item.potentialSavings.yearly}
                                sx={{ color: colors.lowest, fontSize: '28px', lineHeight: '34px', fontWeight: 600 }}
                                suffix={'/yr'}
                                withTooltip={false}
                                sxSuffix={{ color: colors.text.secondaryDark, marginLeft: '5px' }}
                              />
                            </Box>
                          </Box>
                        )}
                        <Typography color={colors.text.secondaryDark} fontSize={'12px'} fontWeight={400} pt={'5px'}>
                          Potential Savings{' '}
                        </Typography>
                      </Box>
                      <Box
                        display={'flex'}
                        justifyContent={'space-between'}
                        sx={{
                          '& .borderedBox': {
                            borderLeft: `0.5px solid ${colors.border.vertical}`,
                            paddingLeft: '40px',
                            pt: '8px',
                            '@media (max-width: 1500px)': {
                              borderLeft: '0px ',
                              paddingLeft: '40px',
                            },
                            '@media (max-width: 1200px)': {
                              paddingLeft: '10px',
                            },
                          },
                        }}
                      >
                        <Box className='borderedBox' sx={{ marginLeft: '15px' }}>
                          <CustomButton
                            variant='tertiary'
                            size='Medium'
                            text={'Optimize Now'}
                            sx={{
                              '@media (max-width: 1030px)': {
                                fontSize: '12px',
                              },
                            }}
                            startIcon={
                              <OptimizeIcon iconColor={colors.text.primary} iconStyle={{ cursor: 'pointer', width: '22px', height: '23px' }} />
                            }
                            onClick={() => {
                              router.push(`/kubernetes/details/${selectedCluster.value}?accountId=${selectedCluster.value}#${item.fragment}`);
                            }}
                          />
                        </Box>
                        {item.optimizations.autoPilot ? (
                          <Box className='borderedBox'>
                            <Box display={'flex'} gap={'10px'}>
                              <Box>
                                <Box
                                  display={'flex'}
                                  alignItems={'center'}
                                  sx={{
                                    '& h4': { color: colors.text.secondary, fontSize: '20px', lineHeight: '23.44px', fontWeight: 600 },
                                    '& p': { color: colors.text.secondaryDark, marginLeft: '5px', fontSize: '12px', fontWeight: 400 },
                                  }}
                                >
                                  <Typography variant='h4'>{formatNumber(item.optimizations.autoPilot, '-', 0, 0)}</Typography>
                                  <Typography>Auto Optimize</Typography>
                                </Box>
                              </Box>
                            </Box>
                          </Box>
                        ) : null}
                      </Box>
                    </Box>
                  ))}
                </Box>
              </SummaryBlock>
            </Box>
            {category.pIdx == 1 ? (
              <Box sx={{ gridColumn: 'span 3' }}>
                <SummaryBlock
                  hideTitle
                  height='100%'
                  sx={{
                    height: '100%',
                    borderColor: 'transparent',
                    backgroundColor: colors.background.white,
                    boxShadow: colors.shadow.softGray,
                    mt: '24px',
                    '@media(max-width: 1170px)': {
                      padding: '16px !important',
                    },
                    '@media(max-width: 1330px)': {
                      minHeight: '430px',
                    },
                  }}
                >
                  <TextWithBorder
                    value='Auto Optimize Response'
                    borderColor={colors.border.primary}
                    borderWidth='3px'
                    sx={{ '& p': { fontSize: '20px', fontWeight: 600, color: colors.text.secondary } }}
                  />
                  <Box
                    display={'flex'}
                    flexDirection={'column'}
                    justifyContent={'space-between'}
                    height={'88%'}
                    gap={'85px'}
                    sx={{
                      '@media(max-width: 1345px)': {
                        gap: '45px',
                      },
                    }}
                  >
                    <Box>
                      <Box mt='24px'>
                        <Typography sx={{ color: colors.text.secondaryDark, fontSize: '12px', fontWeight: 400 }}>Auto Optimize Configured</Typography>
                        {loading ? (
                          <Loader />
                        ) : (
                          <Typography sx={{ color: colors.text.secondary, fontSize: '28px', fontWeight: 600 }}>
                            {formatNumber(category.autoPilot?.count, '-', 0, 0)}
                          </Typography>
                        )}
                      </Box>
                      <Divider sx={{ my: '15px', color: colors.text.divider }} />
                      <Box mt='24px'>
                        <Typography sx={{ color: colors.text.secondaryDark, fontSize: '12px', fontWeight: 400 }}>
                          Response Triggered Last 7 Days
                        </Typography>
                        {loading ? (
                          <Loader />
                        ) : (
                          <Typography sx={{ color: colors.text.secondary, fontSize: '28px', fontWeight: 400 }}>
                            {formatNumber(category.autoPilot?.execution, '-', 0, 0)}
                          </Typography>
                        )}
                      </Box>
                    </Box>

                    <Box
                      sx={{
                        display: 'flex',
                        justifyContent: 'flex-end',
                        gap: '8px',
                        '@media (max-width: 1470px)': {
                          flexWrap: 'wrap',
                          justifyContent: 'left',
                        },
                      }}
                    >
                      {hasWriteAccess(selectedCluster.value) && (
                        <ButtonMenu
                          title={'Add Auto Optimize'}
                          items={[
                            {
                              text: (
                                <span style={{ display: 'flex' }}>
                                  Continuous Vertical Right Sizing
                                  <SafeIcon src={BetaIcon} alt='Beta Icon' width={25} height={20} style={{ marginLeft: '5px' }} />
                                </span>
                              ),
                              onClick: () => handleOpenCreateAutoOptimize('continuous_rightsize'),
                            },
                            { text: 'Horizontal Right Sizing', onClick: () => handleOpenCreateAutoOptimize('horizontal_rightsize') },
                            { text: 'Scheduled Vertical Right Sizing', onClick: () => handleOpenCreateAutoOptimize('vertical_rightsize') },
                            { text: 'PVC Right Sizing', onClick: () => handleOpenCreateAutoOptimize('pvc_rightsize') },
                          ]}
                          sx={{
                            '@media (max-width: 1030px)': {
                              fontSize: '12px',
                            },
                          }}
                        />
                      )}
                      <CustomButton
                        sx={{
                          height: 'auto',
                          width: 'auto',
                          px: '16px',
                          '@media (max-width: 1030px)': {
                            fontSize: '12px',
                          },
                        }}
                        text={'View all'}
                        variant='secondary'
                        size='Small'
                        onClick={() => {
                          router.push(`/auto-pilot?accountId=${selectedCluster.value}#auto-optimize/optimizations`);
                        }}
                      />
                    </Box>
                  </Box>
                </SummaryBlock>{' '}
              </Box>
            ) : (
              <></>
            )}
          </Box>
        );
      })}
      <Box
        key={'4'}
        sx={{
          display: 'grid',
          gridTemplateColumns: 'repeat(12, 1fr)',
          gap: 1,
          mt: '1px',
        }}
      >
        <Box sx={{ gridColumn: 'span 9' }}>
          <SummaryBlock
            key={'4'}
            hideTitle={true}
            height='100%'
            sx={{
              height: '100%',
              border: '0.5px solid transparent !important',
              backgroundColor: colors.background.white,
              boxShadow: `${colors.shadow.softGray} !important`,
              mt: '24px',
            }}
          >
            <Box>
              <TextWithBorder
                value={'Modernization'}
                sx={{ color: colors.text.secondary }}
                borderWidth={'3px'}
                borderColor={colors.border.primary}
                borderStyle={'solid'}
              />
              <Box
                key={'32'}
                sx={{
                  padding: '14px',
                  display: 'grid',
                  gridTemplateColumns: '1fr',
                  gridColumnGap: '30px',
                  '@media (max-width: 1500px)': {
                    gridTemplateColumns: '1fr',
                  },
                  '@media (max-width: 1250px)': {
                    gridTemplateColumns: '1fr',
                    '@media (max-width: 1200px)': {
                      gridColumnGap: '10px',
                      padding: '14px 0px',
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
                    <TextWithBorder
                      value={`${nodeRecommendation.name}`}
                      borderWidth={'2px'}
                      borderColor={colors.nudgebeeMain}
                      borderStyle={'solid'}
                      sx={{ '& p': { fontSize: '16px !important', color: colors.text.secondary } }}
                      releaseIcon={BetaIcon}
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
                        pr: '10px',
                      },
                    }}
                  >
                    {nodeRecommendation.description}
                  </Typography>
                </Box>

                <Box mt={'20px'} display={'grid'} gridTemplateColumns={'repeat(2, 1fr)'} gap={'20px'}>
                  {nodeRecommendation.current_instance_type ? (
                    loading ? (
                      <Loader />
                    ) : (
                      <CostBlock
                        title='Current Cost'
                        data={nodeRecommendation.current_instance_type}
                        borderColor={colors.border.secondary}
                        backgroundColor={colors.background.white}
                      />
                    )
                  ) : (
                    <Loader />
                  )}
                  {nodeRecommendation?.recommended_instance_type?.map((f, _idx) => (
                    <Box key={`${uuidv4()}`}>
                      {loading ? (
                        <Loader />
                      ) : (
                        <CostBlock
                          key={Math.random()}
                          title={f.graviton ? 'Optimized Cost with Graviton' : 'Optimized Cost'}
                          data={f}
                          borderColor={colors.done}
                          backgroundColor={colors.background.costBlock}
                        />
                      )}
                    </Box>
                  ))}
                </Box>
              </Box>
            </Box>
          </SummaryBlock>
        </Box>
      </Box>
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: 'repeat(12, 1fr)',
          gap: 1,
          mt: '1px',
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
                mt: '24px',
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

import k8sApi from '@api1/kubernetes';
import apiUser from '@api1/user';
import { CpuIcon, LocationIcon, MemoryIcon, SyncIcon } from '@assets';
import { Text } from '@components1/common';
import BoxLayout2 from '@components1/common/BoxLayout2';
import CopyableText from '@components1/common/CopyableText';
import Currency from '@components1/common/format/Currency';
import Datetime from '@components1/common/format/Datetime';
import CustomButton from '@components1/common/NewCustomButton';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import ProgressBar from '@components1/common/widgets/ProgressBar';
import { getLast30Days } from '@lib/datetime';
import { StackedLineChartOutlined } from '@mui/icons-material';
import { Box, Dialog, DialogActions, DialogContent, DialogTitle, Divider, Grid, Tooltip, Typography } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';
import { useEffect, useState } from 'react';
import { colors } from 'src/utils/colors';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import KubernetesTable2, { KubernetesCostCharts } from '@components1/k8s/common/KubernetesTable2';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import { KubernetesNodesTrends } from './KubernetesNodesTrends';
import apiKubernetes1 from '@api1/kubernetes1';

const KubernetesNodesTable = ({ accountId, heading = 'All Nodes' }) => {
  const router = useRouter();
  const [data, setData] = useState([]);
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: getLast30Days().getTime() + 60 * 1000,
    endDate: new Date().getTime(),
  });
  const [selectedIsActive, setSelectedIsActive] = useState('Active');
  const [loading, setLoading] = useState(false);
  const loadingTrend = false;
  const [selectedName, setSelectedName] = useState(router.query.nodeName ?? '');
  const [clusterUtilization, setClusterUtilization] = useState({
    totalMemory: 0,
    totalCpu: 0,
    totalAllocatableMemory: 0,
    totalAllocatableCpu: 0,
    usedMemory: 0,
    usedCpu: 0,
    notReadyNodes: 0,
  });
  const [nodeDistribution, setNodeDistribution] = useState({
    on_demand: 0,
    spot: 0,
    fallback: 0,
    nodeTypes: {},
  });
  const [nodes, setNodes] = useState([]);
  const [showTrendChart, setShowTrendChart] = useState(false);
  const [totalActiveNodes, setTotalActiveNodes] = useState(0);
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [currentPage, setCurrentPage] = useState(0);

  const kubernetesNodeTable = 'kubernetesNodeTable';
  const isActiveFilter = ['Active', 'Deleted'];
  const NODE_HEADERS = [
    { name: 'Node Name', width: '20%' },
    { name: 'Type', width: '9.5%' },
    { name: 'Pods', width: '6%' },
    { name: 'IP', width: '10%' },
    { name: 'Cost', width: '7%' },
    { name: 'CPU', width: '15%', secondryText: ` (vCPU)` },
    { name: 'Memory', width: '15%', secondryText: ` (GiB)` },
    { name: 'Status', width: '8%' },
    { name: 'Created', width: '7%' },
  ];

  let isActiveValue = null;
  if (selectedIsActive === 'Deleted') {
    isActiveValue = false;
  } else if (selectedIsActive === 'Active') {
    isActiveValue = true;
  }

  const listNodes = () => {
    if (!accountId) {
      return;
    }
    setLoading(true);
    setNodes([]);
    setTotalActiveNodes(0);
    setData([]);
    k8sApi
      .getK8sNodes({
        accountId,
        isActive: isActiveValue,
        nodeName: selectedName,
        limit: recordsPerPage,
        offset: currentPage * recordsPerPage,
      })
      .then((res) => {
        let nodes = [];
        let data = res?.data?.k8s_nodes?.map((item) => {
          nodes.push({ name: item.name, internalIp: item?.internal_ip || item?.meta?.internal_ip });
          return [
            {
              component: (
                <Box>
                  <CopyableText copyableText={item.name}>
                    <Text value={item.name} showAutoEllipsis />
                  </CopyableText>
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
                    <Box sx={{ display: 'flex' }}>
                      <Box
                        sx={{
                          display: 'flex',
                          marginLeft: '15px',
                          alignItems: 'center',
                          gap: '4px',
                          '&::after': {
                            content: `''`,
                            height: '10px',
                            width: '0.5px',
                            backgroundColor: '#9F9F9F',
                            ml: '10px',
                          },
                        }}
                      >
                        <SafeIcon src={LocationIcon} alt='zone' />
                        <Text value={item.node_region} secondaryText showAutoEllipsis />
                      </Box>
                      <Box sx={{ display: 'flex', marginLeft: '10px', alignItems: 'center', gap: '4px' }}>
                        <SafeIcon src={SyncIcon} alt='sync' />
                        <Datetime value={item.updated_at} sx={{ color: '#9F9F9F', fontSize: '11px', marginBottom: '0px' }} />
                      </Box>
                    </Box>
                    <Box sx={{ display: 'flex' }}>
                      <Box
                        sx={{
                          display: 'flex',
                          marginLeft: '15px',
                          alignItems: 'center',
                          gap: '4px',
                          '&::after': {
                            content: `''`,
                            height: '10px',
                            width: '0.5px',
                            backgroundColor: '#9F9F9F',
                            ml: '10px',
                          },
                        }}
                      >
                        <SafeIcon src={CpuIcon} alt='cores' />
                        <Text value={item?.cpu_capacity + ' core'} secondaryText />
                      </Box>
                      <Box sx={{ display: 'flex', marginLeft: '10px', alignItems: 'center', gap: '4px' }}>
                        <SafeIcon src={MemoryIcon} alt='memory' />
                        <Text value={(item?.memory_capacity / 1024).toFixed(1) + 'GB'} secondaryText />
                      </Box>
                    </Box>
                  </Box>
                </Box>
              ),
              drilldownQuery: {
                nodeName: item?.name,
                subjectName: item?.name,
                subject_type: 'node',
                cost: item?.cost,
                cpu_capacity: item?.cpu_capacity,
                cpu_limit: item?.cpu_limits,
                cpu_request: item.meta?.cpu_allocated,
                memory_capacity: item?.memory_capacity,
                memory_limit: (item?.memory_limits ?? 0) * 1024 * 1024,
                memory_request: (item.meta?.memory_allocatable ?? 0) * 1024 * 1024,
                internalIp: item?.internal_ip || item?.meta?.internal_ip,
                nodeIp: item?.internal_ip || item?.meta?.internal_ip || '',
                data: item,
                isActive: item.is_active,
                accountId,
                kind: 'node',
              },
            },
            {
              component: ClusterNameWithRegion({
                name: item?.node_flavor || '',
                hideIcon: true,
                namespace: (
                  <Text
                    value={
                      item?.node_type?.toLowerCase() === 'on_demand' || item?.node_type?.toLowerCase() === 'on-demand' ? 'On Demand' : item.node_type
                    }
                    secondaryText
                  />
                ),
                namespaceFont: '12px',
              }),
            },
            { component: <Text value={item.meta?.pods_count} /> },
            {
              component: ClusterNameWithRegion({
                name: item?.meta?.internal_ip || '',
                hideIcon: true,
                ...(item?.meta?.external_ip && {
                  namespace: (
                    <Text value={`Public Ip - ${item.meta.external_ip}`} showAutoEllipsis sx={{ color: '#9F9F9F !important', fontSize: '12px' }} />
                  ),
                }),
              }),
            },
            {
              component: <Currency sx={{ fontSize: '14px' }} value={item.cost} suffix='/hr' precison={1} />,
            },
            {
              component: (
                <Box display={'flex'} flexDirection={'column'} gap={'10px'}>
                  <ProgressBar
                    blueVarient={true}
                    capacity={0}
                    value={0}
                    largeVariant={true}
                    tooltipRequired={true}
                    label='Used'
                    width={'100%'}
                    showParentheses
                  />
                  <ProgressBar
                    blueVarient={true}
                    capacity={0}
                    value={0}
                    largeVariant={true}
                    tooltipRequired={true}
                    label='Requested'
                    width={'100%'}
                    showParentheses
                  />
                </Box>
              ),
            },
            {
              component: (
                <Box display={'flex'} flexDirection={'column'} gap={'10px'}>
                  <ProgressBar blueVarient={true} capacity={0} value={0} largeVariant={true} tooltipRequired={true} width={'100%'} showParentheses />
                  <ProgressBar blueVarient={true} capacity={0} value={0} largeVariant={true} tooltipRequired={true} width={'100%'} showParentheses />
                </Box>
              ),
            },
            {
              component: (
                <>
                  <CustomLabels textTransform={'none'} showShadow text={item.is_active ? 'Active' : 'Deleted'} margin='auto' />
                  <Text
                    value={item?.meta?.conditions
                      ?.split(',')
                      .filter((item) => item.includes('Ready'))
                      .map((item) => item.replace(':True', ''))
                      .join(',')}
                    sx={{
                      textAlign: 'center',
                    }}
                    secondaryText
                  />
                </>
              ),
            },
            {
              component: (
                <Datetime
                  value={item.node_creation_time}
                  sxSuffix={{
                    pl: '3px',
                  }}
                />
              ),
            },
          ];
        });
        setNodes(nodes);
        setData(data);
        setTotalActiveNodes(res?.data?.k8s_nodes_aggregate?.aggregate?.count || 0);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  const getNodeInfographics = () => {
    if (!accountId) {
      return;
    }
    setClusterUtilization({
      totalMemory: 0,
      totalCpu: 0,
      totalAllocatableMemory: 0,
      totalAllocatableCpu: 0,
      usedMemory: 0,
      usedCpu: 0,
      notReadyNodes: 0,
    });
    k8sApi.getNodeInfographics({ accountId }).then((res) => {
      let nodeDistibution = { nodeTypes: {} };
      const fullNodeAggregateData = res?.data?.data?.data?.full_nodes || {};
      const totals = (fullNodeAggregateData?.nodes || [])
        .filter((node) => node.meta?.conditions.includes('Ready:True'))
        ?.reduce(
          (acc, node) => {
            acc.cpu_capacity += node?.meta?.cpu_capacity || 0;
            acc.cpu_allocatable += node?.meta?.cpu_allocatable || 0;
            acc.memory_capacity += node?.meta?.memory_capacity || 0;
            acc.memory_allocatable += node?.meta?.memory_allocatable || 0;
            return acc;
          },
          { cpu_capacity: 0, cpu_allocatable: 0, memory_capacity: 0, memory_allocatable: 0 }
        );
      const newClusterUtilization = {
        count: fullNodeAggregateData.aggregate?.count || 0,
        totalCpu: totals?.cpu_capacity || 0,
        totalMemory: totals?.memory_capacity || 0,
        totalAllocatableCpu: totals?.cpu_allocatable || 0,
        totalAllocatableMemory: totals?.memory_allocatable || 0,
        notReadyNodes: Array.isArray(fullNodeAggregateData.nodes)
          ? fullNodeAggregateData.nodes.filter((node) => !node.meta?.conditions.includes('Ready:True')).length
          : 0,
        usedCpu: 0,
        usedMemory: 0,
      };
      (fullNodeAggregateData?.nodes || []).map((item) => {
        if (item.node_type in nodeDistibution) {
          nodeDistibution[item.node_type?.toLowerCase()] += 1;
        } else {
          nodeDistibution[item.node_type?.toLowerCase()] = 1;
        }

        if (item.node_flavor in nodeDistibution.nodeTypes) {
          nodeDistibution.nodeTypes[item.node_flavor].count += 1;
          if (item.node_type?.toLowerCase() === 'spot') {
            nodeDistibution.nodeTypes[item.node_flavor].spotCount += 1;
          }
        } else {
          nodeDistibution.nodeTypes[item.node_flavor] = {
            count: 1,
            spotCount: item.node_type?.toLowerCase() === 'spot' ? 1 : 0,
          };
        }
      });
      setNodeDistribution(nodeDistibution);
      setClusterUtilization(newClusterUtilization);
    });
  };

  useEffect(() => {
    getNodeInfographics();
  }, [accountId, recordsPerPage, selectedIsActive]);

  useEffect(() => {
    listNodes();
  }, [accountId, selectedIsActive, currentPage, recordsPerPage]);

  useEffect(() => {
    if (!accountId) {
      return;
    }
    if (nodes.length == 0) {
      return;
    }
    let cancelled = false;
    k8sApi
      .getK8sMetrices({
        accountId,
        nodeName: nodes.map((f) => f.name),
        startDate: new Date(selectedDateRange?.startDate),
        endDate: new Date(selectedDateRange?.endDate),
        isActive: isActiveValue,
      })
      .then((res) => {
        if (cancelled) return;
        const instances = nodes
          .filter((n) => n.internalIp)
          .map((g) => g.internalIp + '.*')
          .join('|');
        const hosts = nodes
          .filter((n) => n.name)
          .map((g) => g.name + '.*')
          .join('|');
        const requestBody = {
          account_id: accountId,
          instant: true,
          startDate: selectedDateRange?.startDate,
          endDate: selectedDateRange?.endDate,
          metrics: ['cpu_usage_line', 'memory_usage_line'],
          internalIp: instances,
          nodeName: hosts,
        };

        apiKubernetes1.utilisationApi(requestBody).then((res2) => {
          if (cancelled) return;
          const results = res2 || [];
          let totalCpuUsed = 0;
          let totalMemoryUsed = 0;
          const cpu_usage_data = results.find((data) => data?.query_key === 'cpu_usage_line');
          const memory_usage_data = results.find((data) => data?.query_key === 'memory_usage_line');
          if (cpu_usage_data?.payload?.length > 0) {
            for (let r of cpu_usage_data.payload) {
              const cpuValue = Number(r.values[0]);
              totalCpuUsed += cpuValue;
              for (const dataItem of data) {
                if (
                  dataItem[0].drilldownQuery?.nodeIp == r.metric.instance?.split(':')[0] ||
                  dataItem[0].drilldownQuery?.nodeName == r.metric.node?.split(':')[0] ||
                  dataItem[0].drilldownQuery?.nodeName == r.metric.instance?.split(':')[0]
                ) {
                  dataItem[5] = {
                    component: (
                      <Box
                        display={'flex'}
                        flexDirection={'column'}
                        gap={'8px'}
                        sx={{
                          '& .MuiLinearProgress-root': {
                            width: '100%',
                          },
                        }}
                      >
                        <ProgressBar
                          blueVarient={true}
                          capacity={dataItem[0].drilldownQuery?.cpu_capacity ? `${dataItem[0].drilldownQuery?.cpu_capacity}` : 0}
                          value={Number(r.values[0]).toFixed(2) || 0}
                          largeVariant={true}
                          tooltipRequired={true}
                          showCapacity={false}
                          label='Used'
                          width={'100%'}
                          showParentheses
                        />
                        <ProgressBar
                          blueVarient={true}
                          capacity={dataItem[0].drilldownQuery?.cpu_capacity ? `${dataItem[0].drilldownQuery?.cpu_capacity}` : 0}
                          value={Number(
                            (
                              (dataItem?.[0]?.drilldownQuery?.data?.cpu_capacity ?? 0) - (dataItem?.[0]?.drilldownQuery?.data?.cpu_allocatable ?? 0)
                            ).toFixed(2)
                          )}
                          largeVariant={true}
                          tooltipRequired={true}
                          showCapacity={false}
                          label='Requested'
                          width={'100%'}
                          showParentheses
                        />
                      </Box>
                    ),
                  };
                }
              }
            }
          }
          if (memory_usage_data?.payload?.length > 0) {
            for (let r of memory_usage_data.payload) {
              const memoryValue = Number(r.values[0]);
              totalMemoryUsed += memoryValue;
              for (const dataItem of data) {
                if (
                  r.metric?.instance?.indexOf(dataItem[0].drilldownQuery.internalIp) > -1 ||
                  r.metric?.node?.indexOf(dataItem[0].drilldownQuery.nodeName) > -1 ||
                  r.metric?.instance?.indexOf(dataItem[0].drilldownQuery.nodeName) > -1
                ) {
                  dataItem[6] = {
                    component: (
                      <Box
                        sx={{
                          '& .MuiLinearProgress-root': {
                            width: '100%',
                          },
                        }}
                      >
                        <ProgressBar
                          blueVarient={true}
                          capacity={dataItem[0].drilldownQuery?.memory_capacity ? (dataItem[0].drilldownQuery?.memory_capacity / 1024).toFixed(2) : 0}
                          value={(Number(r.values[0]) / (1024 * 1024 * 1024)).toFixed(2) || 0}
                          largeVariant={true}
                          tooltipRequired={true}
                          showCapacity={false}
                          label='Used'
                          showParentheses
                        />
                        <ProgressBar
                          blueVarient={true}
                          capacity={dataItem[0].drilldownQuery?.memory_capacity ? (dataItem[0].drilldownQuery?.memory_capacity / 1024).toFixed(2) : 0}
                          value={
                            (
                              ((dataItem?.[0]?.drilldownQuery?.data?.memory_capacity ?? 0) -
                                (dataItem?.[0]?.drilldownQuery?.data?.memory_allocatable ?? 0)) /
                              1024
                            ).toFixed(2) || 0
                          }
                          largeVariant={true}
                          tooltipRequired={true}
                          showCapacity={false}
                          label='Requested'
                          showParentheses
                        />
                      </Box>
                    ),
                  };
                }
              }
            }
          }
          const podGroupingData = res?.data?.k8s_pod_groupings || [];
          if (podGroupingData.length > 0) {
            for (const dataItem of data) {
              let item = podGroupingData?.find((item) => item.node_name === dataItem[0].drilldownQuery.nodeName);
              if (item) {
                dataItem[4] = {
                  component: <Currency value={item.cost} precison={1} />,
                  data: item.cost,
                };
              }
            }
          } else {
            for (const dataItem of data) {
              dataItem[4] = { text: '-' };
            }
          }
          if (cancelled) return;
          setData([...data]);
          setClusterUtilization((prevState) => ({
            ...prevState,
            usedCpu: totalCpuUsed,
            usedMemory: totalMemoryUsed,
          }));
        });
      })
      .catch((error) => {
        if (!cancelled) console.error(error);
      });
    return () => {
      cancelled = true;
    };
  }, [nodes, selectedDateRange.startDate, selectedDateRange.endDate, accountId]);

  const onEnterPress = () => {
    if (currentPage === 0) {
      listNodes();
    } else {
      setCurrentPage(0);
    }
  };

  const handleDateRangeChange = (passedSelectedDateTime) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
    setCurrentPage(0);
  };

  const onIsActiveFilterChange = (e, _p) => {
    setSelectedIsActive(e?.target?.value);
    setCurrentPage(0);
  };

  const onNameFilterChange = (e, _p) => {
    setSelectedName(e?.target?.value);
  };

  const handleClearFilters = () => {
    setSelectedName('');
  };

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  const sortedNodes = Object.entries(nodeDistribution.nodeTypes).sort(([, a], [, b]) => b.count - a.count);

  return (
    <BoxLayout2
      id='all-nodes'
      heading={heading}
      dateTimeRange={{
        enabled: true,
        onChange: handleDateRangeChange,
        passedSelectedDateTime: {
          startTime: selectedDateRange.startDate,
          endTime: selectedDateRange.endDate,
        },
      }}
      filterOptions={[
        {
          type: 'dropdown',
          enabled: true,
          options: isActiveFilter,
          onSelect: onIsActiveFilterChange,
          value: selectedIsActive,
          minWidth: '150px',
          label: 'State',
        },
        {
          type: 'search',
          enabled: true,
          onSelect: onNameFilterChange,
          value: selectedName,
          minWidth: '300px',
          maxWidth: '300px',
          label: 'Node Name',
          onEnter: onEnterPress,
          onClear: handleClearFilters,
        },
      ]}
      sharingOptions={{
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: kubernetesNodeTable,
            };
          },
        },
        sharing: { enabled: true },
      }}
      extraOptions={[
        <CustomButton
          text={
            <Tooltip title='Node Trends'>
              <StackedLineChartOutlined />
            </Tooltip>
          }
          key={'nodeTrends'}
          onClick={() => setShowTrendChart(true)}
          variant='secondary'
        />,
      ]}
    >
      <Box margin={'20px 0px'}>
        {loadingTrend ? (
          <div className='shimmer' style={{ maxHeight: 126 }} />
        ) : (
          <>
            <Dialog
              open={showTrendChart}
              maxWidth='md'
              fullWidth
              onClose={() => {
                setShowTrendChart(false);
              }}
              aria-labelledby='alert-dialog-title'
              aria-describedby='alert-dialog-description'
            >
              <DialogTitle id='alert-dialog-title'>K8s Node Trends</DialogTitle>
              <DialogContent>
                <KubernetesNodesTrends accountId={accountId} />
              </DialogContent>
              <DialogActions
                sx={{
                  mx: '28px',
                  button: {
                    minWidth: '140px',
                  },
                }}
              >
                <CustomButton
                  text={'Close'}
                  variant='secondary'
                  size='Medium'
                  onClick={() => {
                    setShowTrendChart(false);
                  }}
                />
              </DialogActions>
            </Dialog>
            <Grid container spacing={{ xs: 2, md: 2 }}>
              <Grid item sm={12} lg={sortedNodes.length > 0 ? 6 : 4} md={sortedNodes.length > 0 ? 8 : 4} key={data.id}>
                <SummaryBlock
                  hideTitle
                  sx={{
                    border: '1px solid #60A5FA !important',
                    backgroundColor: '#ffffff',
                    boxShadow: '0px 2px 12px 2px #00000014',
                    '@media (max-width: 1130px)': {
                      padding: '12px',
                    },
                  }}
                >
                  <Box sx={{ display: 'flex', gap: '16px', alignItems: 'stretch' }}>
                    {/* Left: Active Nodes summary */}
                    <Box sx={{ flexShrink: 0, minWidth: '190px' }}>
                      <Box display={'flex'} alignItems={'baseline'} gap={'8px'}>
                        <Typography sx={{ fontSize: '13px', fontWeight: 400, color: colors.text.secondary, whiteSpace: 'nowrap' }}>
                          Active Nodes
                        </Typography>
                        <Typography sx={{ fontSize: '20px', fontWeight: 700, color: colors.secondary.default, lineHeight: 1.1 }}>
                          {clusterUtilization?.count > 0 ? clusterUtilization?.count - clusterUtilization.notReadyNodes : 0}
                        </Typography>
                        <Typography sx={{ fontSize: '14px', fontWeight: 400, color: '#9F9F9F' }}>/ {clusterUtilization?.count || 0}</Typography>
                      </Box>
                      <Divider sx={{ color: '#EBEBEB', my: '12px' }} />
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: '16px' }}>
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
                          <Box sx={{ height: '8px', width: '8px', backgroundColor: '#2563EB', borderRadius: '50%', flexShrink: 0 }} />
                          <Typography sx={{ fontSize: '12px', color: colors.text.tertiary, whiteSpace: 'nowrap' }}>On-Demand</Typography>
                          <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.secondary.default }}>
                            {nodeDistribution['on_demand'] || nodeDistribution['on-demand'] || 0}
                          </Typography>
                        </Box>
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
                          <Box
                            sx={{
                              height: '8px',
                              width: '8px',
                              backgroundColor: '#E6F1FF',
                              borderRadius: '50%',
                              border: '1px solid #D0D0D0',
                              flexShrink: 0,
                            }}
                          />
                          <Typography sx={{ fontSize: '12px', color: colors.text.tertiary, whiteSpace: 'nowrap' }}>Spot</Typography>
                          <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.secondary.default }}>
                            {nodeDistribution['spot'] || 0}
                          </Typography>
                        </Box>
                      </Box>
                    </Box>
                    {/* Vertical divider */}
                    {sortedNodes.length > 0 && <Divider orientation='vertical' flexItem />}
                    {/* Right: Node types table */}
                    {sortedNodes.length > 0 && (
                      <Box sx={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, overflow: 'hidden' }}>
                        {/* Sticky header row */}
                        <Box
                          sx={{
                            display: 'flex',
                            alignItems: 'center',
                            gap: '4px',
                            pb: '6px',
                            borderBottom: '1px solid #EBEBEB',
                            flexShrink: 0,
                          }}
                        >
                          <Box
                            sx={{
                              flex: 1,
                              minWidth: 0,
                              fontSize: '9px',
                              fontWeight: 600,
                              textTransform: 'uppercase',
                              color: '#9F9F9F',
                              letterSpacing: '0.5px',
                            }}
                          >
                            Node Type
                          </Box>
                          <Box
                            sx={{
                              width: '48px',
                              textAlign: 'right',
                              fontSize: '9px',
                              fontWeight: 600,
                              textTransform: 'uppercase',
                              color: '#9F9F9F',
                              letterSpacing: '0.5px',
                              flexShrink: 0,
                            }}
                          >
                            Total
                          </Box>
                          <Box
                            sx={{
                              width: '44px',
                              textAlign: 'right',
                              fontSize: '9px',
                              fontWeight: 600,
                              textTransform: 'uppercase',
                              color: '#9F9F9F',
                              letterSpacing: '0.5px',
                              flexShrink: 0,
                            }}
                          >
                            OD
                          </Box>
                          <Box
                            sx={{
                              width: '44px',
                              textAlign: 'right',
                              fontSize: '9px',
                              fontWeight: 600,
                              textTransform: 'uppercase',
                              color: '#9F9F9F',
                              letterSpacing: '0.5px',
                              flexShrink: 0,
                            }}
                          >
                            Spot
                          </Box>
                        </Box>
                        {/* Scrollable data rows */}
                        <Box
                          sx={{
                            flex: 1,
                            maxHeight: '70px',
                            overflowY: 'auto',
                            scrollbarWidth: 'thin',
                            '&::-webkit-scrollbar': { width: '4px' },
                            '&::-webkit-scrollbar-thumb': { backgroundColor: '#D0D0D0', borderRadius: '2px' },
                            pt: '4px',
                          }}
                        >
                          {sortedNodes.map(([key, value]) => {
                            const odCount = value.count - value.spotCount;
                            return (
                              <Box
                                key={`nodetype-${key}`}
                                sx={{
                                  display: 'flex',
                                  alignItems: 'center',
                                  gap: '4px',
                                  py: '2px',
                                }}
                              >
                                <Box
                                  sx={{
                                    flex: 1,
                                    minWidth: 0,
                                    fontSize: '11px',
                                    color: colors.text.secondary,
                                    whiteSpace: 'nowrap',
                                    overflow: 'hidden',
                                    textOverflow: 'ellipsis',
                                  }}
                                  title={key}
                                >
                                  {key}
                                </Box>
                                <Box
                                  sx={{
                                    width: '48px',
                                    textAlign: 'right',
                                    fontSize: '11px',
                                    fontWeight: 600,
                                    color: colors.secondary.default,
                                    flexShrink: 0,
                                  }}
                                >
                                  {value.count}
                                </Box>
                                <Box
                                  sx={{
                                    width: '44px',
                                    textAlign: 'right',
                                    fontSize: '11px',
                                    color: odCount > 0 ? '#9F9F9F' : '#D0D0D0',
                                    flexShrink: 0,
                                  }}
                                >
                                  {odCount > 0 ? odCount : '\u2014'}
                                </Box>
                                <Box
                                  sx={{
                                    width: '44px',
                                    textAlign: 'right',
                                    fontSize: '11px',
                                    color: value.spotCount > 0 ? '#9F9F9F' : '#D0D0D0',
                                    flexShrink: 0,
                                  }}
                                >
                                  {value.spotCount > 0 ? value.spotCount : '\u2014'}
                                </Box>
                              </Box>
                            );
                          })}
                        </Box>
                      </Box>
                    )}
                  </Box>
                </SummaryBlock>
              </Grid>
              <Grid item sm={12} md={4}>
                <SummaryBlock
                  hideTitle
                  sx={{
                    backgroundColor: '#ffffff',
                    border: '0.75px solid #4ADE80 !important',
                    boxShadow: '0px 2px 12px 2px #00000014',
                    '@media (max-width: 1130px)': {
                      padding: '12px',
                    },
                  }}
                >
                  <Grid container spacing={2} direction='row' justifyContent='center' alignItems='center' alignContent='center' wrap='wrap'>
                    <Grid
                      item
                      md={6}
                      sx={{
                        '& .MuiLinearProgress-root': {
                          width: '100% !important',
                        },
                      }}
                    >
                      <Box
                        display={'flex'}
                        alignItems={'center'}
                        justifyContent={'space-between'}
                        fontSize={'12px'}
                        fontWeight={400}
                        color={colors.text.tertiary}
                      >
                        Total Memory{' '}
                        <Box display={'flex'} alignItems={'center'} gap={'4px'}>
                          <Typography fontSize={'14px'} fontWeight={500} color={'#374151'}>
                            {clusterUtilization.totalMemory ? (clusterUtilization.totalMemory / 1024)?.toFixed(0) : '-'}
                          </Typography>
                          GB
                        </Box>
                      </Box>
                      <Divider sx={{ my: '8px' }} />
                      <ProgressBar
                        blueVarient={true}
                        capacity={clusterUtilization.totalMemory ? (clusterUtilization.totalMemory / 1024)?.toFixed() : '0'}
                        value={clusterUtilization.usedMemory ? (clusterUtilization.usedMemory / (1024 * 1024 * 1024))?.toFixed(1) : '0'}
                        largeVariant={true}
                        tooltipRequired={true}
                        label='GB (Max)'
                        width={'100%'}
                      />
                    </Grid>

                    <Grid
                      item
                      md={6}
                      sx={{
                        '& .MuiLinearProgress-root': {
                          width: '100% !important',
                        },
                      }}
                    >
                      <Box
                        display={'flex'}
                        alignItems={'center'}
                        justifyContent={'space-between'}
                        fontSize={'12px'}
                        fontWeight={400}
                        color={colors.text.tertiary}
                      >
                        Total CPU
                        <Box display={'flex'} alignItems={'center'} gap={'4px'}>
                          <Typography fontSize={'14px'} fontWeight={500} color={'#374151'}>
                            {clusterUtilization.totalCpu || '-'}
                          </Typography>
                          vCPU
                        </Box>
                      </Box>
                      <Divider sx={{ my: '8px' }} />
                      <ProgressBar
                        blueVarient={true}
                        capacity={clusterUtilization.totalCpu?.toFixed()}
                        value={clusterUtilization.usedCpu?.toFixed(1)}
                        largeVariant={true}
                        tooltipRequired={true}
                        label='vCPU (Max)'
                      />
                    </Grid>
                  </Grid>
                </SummaryBlock>
              </Grid>
            </Grid>
          </>
        )}
      </Box>
      <KubernetesTable2
        id={kubernetesNodeTable}
        headers={NODE_HEADERS}
        sort={{
          name: 'Created',
          order: 'desc',
        }}
        data={data?.filter((item) => {
          if (selectedName && item[0].data) {
            return item[0].data.toLowerCase().includes(selectedName.toLowerCase());
          }
          return true;
        })}
        expandable={{
          tabs: [
            {
              text: 'Details',
              value: 0,
              key: 'NodeDetails',
              componentFn: nodeDetailsFn,
            },
            { text: 'Pods', value: 1, key: 'pods' },
            { text: 'Utilization Trends', value: 2, key: 'utilization' },
            {
              text: 'Cost Trends',
              value: 3,
              key: 'cost',
              componentFn: function (_opt, drilldownQuery, row) {
                return (
                  <KubernetesCostCharts
                    row={row}
                    accountId={accountId}
                    query={drilldownQuery}
                    selectedDateRange={selectedDateRange}
                    actualCostTrend={true}
                  />
                );
              },
            },
            { text: 'Recent Events', value: 4, key: 'events' },
            { text: 'Network', value: 5, key: 'network' },
            { text: 'Storage', value: 6, key: 'node-storage' },
          ],
        }}
        rowsPerPage={recordsPerPage}
        showExpandable
        loading={loading}
        selectedDateRange={selectedDateRange}
        tableHeadingCenter={['Status']}
        onPageChange={onPageChange}
        pageNumber={currentPage + 1}
        totalRows={totalActiveNodes}
      />
    </BoxLayout2>
  );
};

function nodeDetailsFn(_accountId, drilldownQuery) {
  const mapLabels = (label) => {
    const labelArray = [];

    if (label) {
      for (let [k, v] of Object.entries(label)) {
        let name = k + '=' + v;
        labelArray.push(
          <CustomLabels
            textTransform={'none'}
            height='auto'
            margin='0px'
            wordBreak={'break-all'}
            displayTooltip
            key={k}
            text={name}
            variant={'grey'}
          />
        );
      }
    }
    return labelArray;
  };

  return (
    <Box>
      <Grid container sx={{ marginBottom: '8px' }}>
        <Grid item md={3}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Labels:
          </Typography>
        </Grid>
        <Grid
          item
          md={9}
          sx={{
            display: 'flex',
            flexDirection: 'row',
            flexWrap: 'wrap',
            gap: '12px',
            fontFamily: 'Roboto',
            fontSize: '14px',
            fontWeight: '500',
            lineHeight: '20px',
            color: '#2563EB',
            maxWidth: '360px',
          }}
        >
          {mapLabels(drilldownQuery?.data?.labels)}
        </Grid>
      </Grid>
      <Grid container sx={{ marginBottom: '8px' }}>
        <Grid item md={3}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Annotations:
          </Typography>
        </Grid>
        <Grid
          item
          md={9}
          sx={{
            display: 'flex',
            flexDirection: 'row',
            flexWrap: 'wrap',
            fontFamily: 'Roboto',
            gap: '12px',
            fontSize: '14px',
            fontWeight: '500',
            lineHeight: '20px',
            color: '#2563EB',
            maxWidth: '360px',
          }}
        >
          {mapLabels(drilldownQuery?.data?.meta?.node_info?.annotations)}
        </Grid>
      </Grid>
      <Grid container sx={{ marginBottom: '8px' }}>
        <Grid item md={3}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Taints:
          </Typography>
        </Grid>
        <Grid
          item
          md={9}
          sx={{
            display: 'flex',
            flexDirection: 'row',
            flexWrap: 'wrap',
            fontFamily: 'Roboto',
            gap: '12px',
            fontSize: '14px',
            fontWeight: '500',
            lineHeight: '20px',
            color: '#2563EB',
            maxWidth: '360px',
          }}
        >
          {mapLabels(
            drilldownQuery?.data?.taints
              ?.split(',')
              .map((taint) => taint.split('='))
              .reduce((a, v) => ({ ...a, [v[0]]: v[1] }), {})
          )}
        </Grid>
      </Grid>
      <Grid container sx={{ marginBottom: '8px' }}>
        <Grid item md={3}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            System Info:
          </Typography>
        </Grid>
        <Grid
          item
          md={9}
          sx={{
            display: 'flex',
            flexDirection: 'row',
            flexWrap: 'wrap',
            fontFamily: 'Roboto',
            gap: '12px',
            fontSize: '14px',
            fontWeight: '500',
            lineHeight: '20px',
            color: '#2563EB',
            maxWidth: '360px',
          }}
        >
          {mapLabels(drilldownQuery?.data?.meta?.node_info?.system)}
        </Grid>
      </Grid>
      <Grid container sx={{ marginBottom: '8px' }}>
        <Grid item md={3}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Addresses:
          </Typography>
        </Grid>
        <Grid
          item
          md={9}
          sx={{
            display: 'flex',
            flexDirection: 'row',
            flexWrap: 'wrap',
            fontFamily: 'Roboto',
            gap: '12px',
            fontSize: '14px',
            fontWeight: '500',
            lineHeight: '20px',
            color: '#2563EB',
            maxWidth: '360px',
          }}
        >
          {drilldownQuery?.data?.meta?.node_info?.addresses.map((address) => {
            return (
              <CustomLabels
                textTransform={'none'}
                height='auto'
                margin='0px'
                wordBreak={''}
                displayTooltip
                key={address}
                text={address}
                variant={'grey'}
              />
            );
          })}
        </Grid>
      </Grid>
      <Grid container sx={{ marginBottom: '8px' }}>
        <Grid item md={3}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Conditions:
          </Typography>
        </Grid>
        <Grid
          item
          md={9}
          sx={{
            display: 'flex',
            flexDirection: 'row',
            flexWrap: 'wrap',
            fontFamily: 'Roboto',
            gap: '12px',
            fontSize: '14px',
            fontWeight: '500',
            lineHeight: '20px',
            color: '#2563EB',
            maxWidth: '360px',
          }}
        >
          {drilldownQuery?.data?.meta?.conditions}
        </Grid>
      </Grid>
    </Box>
  );
}

KubernetesNodesTable.propTypes = {
  accountId: PropTypes.string.isRequired,
  heading: PropTypes.string,
};

export default KubernetesNodesTable;

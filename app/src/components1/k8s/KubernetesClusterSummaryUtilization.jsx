import { Box, Divider, Grid, Typography } from '@mui/material';
import React, { useEffect, useState, useRef } from 'react';
import { SummaryBlock } from './KubernetesClusterSummary';
import TextWithBorder from '@components1/common/TextWithBorder';
import CustomIconButton from '@components1/CustomIconButton';
import CustomTable from '@components1/common/tables/CustomTable2';
import { addIcon, ExternalLinkIcon, WhiteOptimizeIcon } from '@assets';
import k8sApi from '@api1/kubernetes';
import apiAutoPilot from '@api1/autoPilot';
import apiWorkflow from '@api1/workflow';
import Datetime from '@components1/common/format/Datetime';
import Text from '@components1/common/format/Text';
import CustomBorderCard from '@components1/common/CustomBorderCard';
import Currency from '@components1/common/format/Currency';
import { useRouter } from 'next/router';
import recommendationApi from '@api1/recommendation';
import CustomLink from '@components1/common/CustomLink';
import ShimmerLoading from '@components1/common/ShimmerLoading';
import CustomButton from '@components1/common/NewCustomButton';
import { hasWriteAccess } from '@lib/auth';
import { colors } from '@utils/colors';
import SafeIcon from '@components1/common/SafeIcon';

const KubernetesClusterSummaryUtilization = ({ accountId }) => {
  const router = useRouter();
  const eventRef = useRef();
  const optimizationRef = useRef();
  let startDate = new Date();
  startDate.setHours(-24, 0, 0, 0);
  const dateRange = {
    startDate: startDate,
    endDate: new Date(),
  };
  const optimizeCardsData = [
    {
      title: 'Workload right sizing',
      estimatedSavings: '-',
      optimizations: '-',
      fragment: 'optimize/right-sizing',
    },
    {
      title: 'Unused volumes',
      estimatedSavings: '-',
      optimizations: '-',
      fragment: 'optimize/unused-volume',
    },
    {
      title: 'Move to Graviton',
      estimatedSavings: '-',
      optimizations: 0,
      fragment: 'optimize/summary',
    },
    {
      title: 'Replica Right Sizing',
      estimatedSavings: '-',
      optimizations: '-',
      isHighlighted: true,
      fragment: 'optimize/replica-rightsizing',
    },
    {
      title: 'Abandoned workloads',
      estimatedSavings: '-',
      optimizations: '-',
      fragment: 'optimize/abandoned-resources',
    },
    {
      title: 'PV Right Sizing',
      estimatedSavings: '-',
      optimizations: '-',
      fragment: 'optimize/pv-rightsizing',
    },
    {
      title: 'Spot Instances',
      estimatedSavings: '-',
      optimizations: '-',
      fragment: 'optimize/spot-recommendation',
    },
  ];
  const defaultEventSummaryData = {
    severityData: [
      {
        value: 0,
        label: 'High',
        color: '#FFFFFF',
        background: '#E95252',
      },
      {
        value: 0,
        label: 'Medium',
        color: '#EF4444',
        background: '#FFF1F1',
      },
      {
        value: 0,
        label: 'Low',
        color: '#AEA124',
        background: '#FFFCE5',
      },
      {
        value: 0,
        label: 'Debug',
        color: '#3B82F6',
        background: '#EFF6FF',
      },
    ],
    highEvents: 0,
    applicationEvents: 0,
    podEvents: 0,
    nodeEvents: 0,
  };

  const [eventTypeData, setEventTypeData] = useState([]);
  const [applicationEventData, setApplicationEventData] = useState([]);
  const [apiErrorsByCount, setApiErrorsByCount] = useState([]);
  const [workflowData, setWorkflowData] = useState({ configuredCount: 0, actionedCount: 0 });
  const [autoOptimizeData, setAutoOptimizeData] = useState({});
  const [yearlyOptimizedSavings, setYearlyOptimizedSavings] = useState('-');
  const [totalOptimizeRecommendationsCount, setTotalOptimizeRecommendationsCount] = useState(0);
  const [eventSummaryData, setEventSummaryData] = useState(defaultEventSummaryData);
  const [optimizeBlockData, setOptimizeBlockData] = useState(optimizeCardsData);
  const [isEventAutoPilotElementVisible, setIsEventAutoPilotElementVisible] = useState(false);
  const [hasEventAutoPilotFetched, setHasEventAutoPilotFetched] = useState(false);
  const [isOptimizationElementVisible, setIsOptimizationElementVisible] = useState(false);
  const [hasOptimizationFetched, setHasOptimizationFetched] = useState(false);
  const [eventTotalCountLoading, setEventTotalCountLoading] = useState(false);
  const [apiErrorsByCountLoading, setApiErrorsByCountLoading] = useState(false);
  const [eventTypeDataLoading, setEventTypeDataLoading] = useState(false);
  const [applicationEventDataLoading, setApplicationEventDataLoading] = useState(false);

  useEffect(() => {
    setEventSummaryData(defaultEventSummaryData);
    setEventTypeData([]);
    setApplicationEventData([]);
    setApiErrorsByCount([]);
    setAutoOptimizeData({});
    setHasEventAutoPilotFetched(false);
    setHasOptimizationFetched(false);
  }, [accountId]);

  useEffect(() => {
    const eventObserver = new IntersectionObserver((entries) => {
      const entry = entries[0];
      setIsEventAutoPilotElementVisible(entry.isIntersecting);
    });
    const eventOptimizationObserver = new IntersectionObserver((entries) => {
      const entry = entries[0];
      setIsOptimizationElementVisible(entry.isIntersecting);
    });
    if (eventRef.current) {
      eventObserver.observe(eventRef.current);
    }
    if (optimizationRef.current) {
      eventOptimizationObserver.observe(optimizationRef.current);
    }
    return () => {
      eventObserver.disconnect();
      eventOptimizationObserver.disconnect();
    };
  }, []);

  useEffect(() => {
    if (!accountId || !isEventAutoPilotElementVisible || hasEventAutoPilotFetched) {
      return;
    }

    const fetchData = async () => {
      try {
        setEventTotalCountLoading(true);
        setApiErrorsByCountLoading(true);
        setEventTypeDataLoading(true);
        setApplicationEventDataLoading(true);
        const apiCalls = [
          {
            call: () =>
              k8sApi.getK8sEventGroupings(
                10,
                0,
                {
                  account_id: accountId,
                  start_date: dateRange.startDate,
                  end_date: dateRange.endDate,
                },
                ['tenant_id', 'account_id'],
                [
                  'event_count',
                  'count_priority_high',
                  'count_priority_medium',
                  'count_priority_low',
                  'count_priority_debug',
                  'count_priority_info',
                  'count_application_issues',
                  'count_node_issues',
                  'count_pod_issues',
                ]
              ),
            process: processEventSummary,
          },
          {
            call: () =>
              k8sApi.getK8sEventGroupings(
                5,
                0,
                {
                  account_id: accountId,
                  start_date: dateRange.startDate,
                  end_date: dateRange.endDate,
                  priority: 'HIGH',
                },
                ['tenant_id', 'account_id', 'aggregation_key'],
                ['max_created_at', 'event_count', 'aggregation_key'],
                { name: 'event_count', order: 'desc' }
              ),
            process: processEventType,
          },
          {
            call: () =>
              k8sApi.getK8sEventGroupings(
                5,
                0,
                {
                  account_id: accountId,
                  start_date: dateRange.startDate,
                  end_date: dateRange.endDate,
                  aggregation_key: ['HighErrorCriticalLogs', 'ApplicationAPIFailures'],
                  priority: 'HIGH',
                },
                ['tenant_id', 'account_id', 'subject_name'],
                ['max_created_at', 'event_count', 'subject_name'],
                { name: 'event_count', order: 'desc' }
              ),
            process: processApplicationEvent,
          },
          {
            call: () =>
              k8sApi.getK8sEventGroupings(
                5,
                0,
                {
                  account_id: accountId,
                  aggregation_key: 'ApplicationAPIFailures',
                },
                ['tenant_id', 'account_id', 'title'],
                ['max_created_at', 'event_count', 'subject_namespace', 'subject_name', 'title'],
                { name: 'event_count', order: 'desc' }
              ),
            process: processApiErrors,
          },
          {
            call: () => apiWorkflow.getWorkflowCount(accountId, { status: 'ACTIVE' }),
            process: processWorkflowCount,
          },
          {
            call: () => apiWorkflow.getWorkflowExecutionCount(accountId, { startDate: dateRange.startDate }),
            process: processWorkflowExecutionCount,
          },
          {
            call: () =>
              apiAutoPilot.getAutoPilotAggregate({
                accountId: accountId,
              }),
            process: processAutoPilot,
          },
        ];
        const results = await Promise.allSettled(apiCalls.map((api) => api.call()));
        results.forEach((result, index) => {
          if (result.status === 'fulfilled') {
            apiCalls[index].process(result.value);
          } else {
            console.error(`API call ${index} failed:`, result.reason);
          }
        });
        setHasEventAutoPilotFetched(true);
      } catch (error) {
        console.error('Error fetching event data:', error);
      } finally {
        setEventTotalCountLoading(false);
        setApiErrorsByCountLoading(false);
        setEventTypeDataLoading(false);
        setApplicationEventDataLoading(false);
      }
    };

    fetchData();
  }, [accountId, isEventAutoPilotElementVisible]);

  const processEventSummary = async (response) => {
    const firstRow = response.data?.event_groupings?.[0];
    if (firstRow) {
      setEventSummaryData({
        totalEvents: firstRow.event_count,
        nodeEvents: firstRow.count_node_issues,
        highEvents: firstRow.count_priority_high,
        applicationEvents: firstRow.count_application_issues,
        podEvents: firstRow.count_pod_issues,

        severityData: [
          { value: firstRow.count_priority_high, label: 'High', color: '#FFFFFF', background: '#E95252' },
          { value: firstRow.count_priority_medium, label: 'Medium', color: '#EF4444', background: '#FFF1F1' },
          { value: firstRow.count_priority_low, label: 'Low', color: '#AEA124', background: '#FFFCE5' },
          { value: firstRow.count_priority_debug, label: 'Debug', color: '#3B82F6', background: '#EFF6FF' },
        ],
      });
    }
  };

  const processEventType = async (response) => {
    const eventTypeTableData = response.data?.event_groupings?.map((item) => [
      {
        component: (
          <Box>
            <Text showAutoEllipsis value={item.aggregation_key} />
            <Box display={'flex'} alignItems={'center'}>
              <Text value={'Last occ:'} secondaryText />
              <Datetime value={item.max_created_at} sx={{ fontSize: '11px', pl: '3px', textAlign: 'right' }} sxSuffix={{ fontSize: '11px' }} />
            </Box>
          </Box>
        ),
      },
      {
        component: (
          <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
            <CustomLink
              href={`/kubernetes/details/${accountId}?eventAggregationKey=${item.aggregation_key}&eventPriority=HIGH#events/all-events`}
              style={{ color: '#2563EB', fontSize: '12px' }}
            >
              {item?.event_count}
            </CustomLink>
          </Box>
        ),
      },
    ]);
    setEventTypeData(eventTypeTableData);
  };

  const processApplicationEvent = async (response) => {
    const applicationEventTableData =
      response?.data?.event_groupings?.map((item) => [
        {
          component: (
            <Box>
              <Text showAutoEllipsis value={item.subject_name} />
              <Box display={'flex'} alignItems={'center'}>
                <Text value={'Last occ:'} secondaryText />
                <Datetime value={item.max_created_at} sx={{ fontSize: '11px', pl: '3px', textAlign: 'right' }} sxSuffix={{ fontSize: '11px' }} />
              </Box>
            </Box>
          ),
        },
        {
          component: (
            <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
              <CustomLink
                href={`/kubernetes/details/${accountId}?eventAggregationKey=HighErrorCriticalLogs,ApplicationAPIFailures#events/all-events`}
                style={{ color: colors.text.primary, fontSize: '12px' }}
              >
                {item?.event_count}
              </CustomLink>
            </Box>
          ),
        },
      ]) || [];
    setApplicationEventData(applicationEventTableData);
  };

  const processApiErrors = async (response) => {
    const apiErrorsTableData = response.data?.event_groupings?.map((data) => [
      {
        component: (
          <Box>
            <Text showAutoEllipsis value={data.title?.replace('High API Failure for', ' ')} />
            <Box display={'flex'} alignItems={'center'}>
              <Text value={'Last occ:'} secondaryText />
              <Datetime value={data.max_created_at} sx={{ fontSize: '11px', pl: '3px', textAlign: 'right' }} sxSuffix={{ fontSize: '11px' }} />
            </Box>
          </Box>
        ),
      },
      {
        component: (
          <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
            <CustomLink
              href={`/kubernetes/details/${accountId}?eventTitle=${data.title}&eventAggregationKey=ApplicationAPIFailures#events/all-events`}
              style={{ color: colors.text.primary, fontSize: '12px', fontWeight: 500 }}
            >
              {data?.event_count}
            </CustomLink>
          </Box>
        ),
      },
    ]);
    setApiErrorsByCount(apiErrorsTableData);
  };

  const processWorkflowCount = async (_response) => {
    const count = _response?.data?.workflow_get_count?.count ?? 0;
    setWorkflowData((prev) => ({ ...prev, configuredCount: count }));
  };

  const processWorkflowExecutionCount = async (_response) => {
    const count = _response?.data?.workflow_get_execution_count?.count ?? 0;
    setWorkflowData((prev) => ({ ...prev, actionedCount: count }));
  };

  const processAutoPilot = async (_response) => {
    setAutoOptimizeData(_response);
  };

  const getOptimizeCardsData = () => {
    recommendationApi.optimizeSummaryInfographic(accountId).then((res) => {
      const totalEstimatedSavings = Object.values(res?.data ?? {})
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
      setYearlyOptimizedSavings(totalEstimatedSavings * 12);
      setTotalOptimizeRecommendationsCount(res?.data?.count_optimize_recommendations);
      optimizeCardsData[0].estimatedSavings = res?.data?.workload_rightsize?.aggregate?.sum?.estimated_savings * 12 ?? 0;
      optimizeCardsData[0].optimizations = res?.data?.workload_rightsize?.aggregate?.count ?? 0;
      optimizeCardsData[1].estimatedSavings = res?.data?.unused_pvc?.aggregate?.sum?.estimated_savings * 12 ?? 0;
      optimizeCardsData[1].optimizations = res?.data?.unused_pvc?.aggregate?.count ?? 0;
      optimizeCardsData[3].estimatedSavings = res?.data?.replica_rightsize?.aggregate?.sum?.estimated_savings * 12 ?? 0;
      optimizeCardsData[3].optimizations = res?.data?.replica_rightsize?.aggregate?.count ?? 0;
      optimizeCardsData[4].estimatedSavings = res?.data?.abandoned_resource?.aggregate?.sum?.estimated_savings * 12 ?? 0;
      optimizeCardsData[4].optimizations = res?.data?.abandoned_resource?.aggregate?.count ?? 0;
      optimizeCardsData[5].estimatedSavings = res?.data?.pv_rightsize?.aggregate?.sum?.estimated_savings * 12 ?? 0;
      optimizeCardsData[5].optimizations = res?.data?.pv_rightsize?.aggregate?.count ?? 0;
      optimizeCardsData[6].estimatedSavings = res?.data?.spot_instance?.aggregate?.sum?.estimated_savings * 12 ?? 0;
      optimizeCardsData[6].optimizations = res?.data?.spot_instance?.aggregate?.count ?? 0;
      setOptimizeBlockData(optimizeCardsData);
      setHasOptimizationFetched(true);
    });
  };

  useEffect(() => {
    if (!accountId || !isOptimizationElementVisible || hasOptimizationFetched) {
      return;
    }

    getOptimizeCardsData();
  }, [accountId, isOptimizationElementVisible]);

  return (
    <>
      <Grid container columnSpacing={'15px'} mt={'1px'} alignItems='stretch'>
        <Grid item xs={9}>
          <CustomBorderCard
            borderColor='transparent'
            sx={{
              borderColor: 'transparent',
              backgroundColor: '#ffffff',
              boxShadow: '0px 4px 20px 0px #B4B4B41F',
              minHeight: '430px',
              '@media(max-width: 1170px)': {
                padding: '16px !important',
              },
            }}
          >
            <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
              <Box ref={eventRef} display='flex' alignItems={'center'}>
                <TextWithBorder
                  value='Events/Errors'
                  borderColor='#3B82F6'
                  borderWidth='3px'
                  sx={{ '& p': { fontSize: '20px', fontWeight: 600, color: '#374151' } }}
                />
                <ShimmerLoading isLoading={eventTotalCountLoading} height={'10px'} width={'70px'}>
                  <Typography
                    sx={{
                      border: '0.5px solid #F87171',
                      backgroundColor: '#FEF2F2',
                      p: '2px 8px',
                      borderRadius: '4px',
                      color: '#374151',
                      fontWeight: 500,
                    }}
                  >
                    {eventSummaryData.totalEvents || 0}
                  </Typography>
                </ShimmerLoading>
              </Box>
              <Box
                display={'flex'}
                gap='10px'
                sx={{
                  '@media(max-width: 1130px)': {
                    gap: '5px',
                  },
                }}
              >
                <ShimmerLoading isLoading={eventTotalCountLoading} height={'10px'} width={'430px'}>
                  {eventSummaryData.severityData.map((data, index) => (
                    <Box
                      display='flex'
                      alignItems={'center'}
                      key={data.label}
                      sx={{
                        '&::after': index !== eventSummaryData.severityData.length - 1 && {
                          content: '" "',
                          height: '16px',
                          border: `0.5px solid ${colors.border.secondary}`,
                        },
                      }}
                    >
                      <Typography
                        sx={{
                          backgroundColor: data.background,
                          p: '2px 10px',
                          borderRadius: '4px',
                          color: data.color,
                          boxShadow: '0px 1px 3px 0px #0000001A',
                          fontWeight: 700,
                          fontSize: '11px',
                          mr: '5px',
                        }}
                      >
                        {data.value || 0}
                      </Typography>
                      <Text value={data.label} secondaryText sx={{ mr: '10px' }} />
                    </Box>
                  ))}
                </ShimmerLoading>
              </Box>
            </Box>

            <Box display={'grid'} gridTemplateColumns={'1fr 1fr 1fr'} gap={'12px'} mt={'15px'}>
              <Box>
                <TextWithBorder
                  value='Critical Events - By Type'
                  borderColor='#FACF39'
                  borderWidth='2px'
                  sx={{ '& p': { fontSize: '14px !important', fontWeight: 500, color: '#374151' } }}
                  span={
                    <CustomIconButton
                      onClick={() => {
                        router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#events/grouped-events`);
                      }}
                    >
                      <SafeIcon src={ExternalLinkIcon} alt='redirect' />
                    </CustomIconButton>
                  }
                />
                <ShimmerLoading isLoading={eventTypeDataLoading} width='93%' height={'300px'}>
                  <CustomTable
                    tableData={eventTypeData}
                    headers={['Event type', { name: 'Count', width: '5%' }]}
                    showUpdatedTable
                    showEmptyStateText
                  />
                </ShimmerLoading>
              </Box>
              <Box>
                <TextWithBorder
                  value='Critical Events - Recent'
                  borderColor='#FACF39'
                  borderWidth='2px'
                  sx={{ '& p': { fontSize: '14px !important', fontWeight: 500, color: '#374151' } }}
                  span={
                    <CustomIconButton
                      onClick={() => {
                        router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#events/grouped-events`);
                      }}
                    >
                      <SafeIcon src={ExternalLinkIcon} alt='redirect' />
                    </CustomIconButton>
                  }
                />
                <ShimmerLoading isLoading={applicationEventDataLoading} width='93%' height={'300px'}>
                  <CustomTable
                    tableData={applicationEventData}
                    headers={['Application name', { name: 'Count', width: '5%' }]}
                    showUpdatedTable
                    showEmptyStateText
                  />
                </ShimmerLoading>
              </Box>{' '}
              <Box>
                <TextWithBorder
                  value='API Errors'
                  borderColor='#FACF39'
                  borderWidth='2px'
                  sx={{ '& p': { fontSize: '14px !important', fontWeight: 500, color: '#374151' } }}
                  span={
                    <CustomIconButton
                      onClick={() => {
                        router.push(`/kubernetes/details/${accountId}?eventAggregationKey=ApplicationAPIFailures#events/all-events`);
                      }}
                    >
                      <SafeIcon src={ExternalLinkIcon} alt='redirect' />
                    </CustomIconButton>
                  }
                />
                <ShimmerLoading isLoading={apiErrorsByCountLoading} width='93%' height={'300px'}>
                  <CustomTable
                    rowsPerPage={5}
                    tableData={apiErrorsByCount}
                    headers={['URL', { name: 'Count', width: '5%' }]}
                    showUpdatedTable
                    showEmptyStateText
                  />
                </ShimmerLoading>
              </Box>{' '}
            </Box>
          </CustomBorderCard>
        </Grid>
        <Grid item xs={3}>
          <CustomBorderCard
            borderColor='transparent'
            sx={{
              borderColor: 'transparent',
              backgroundColor: '#ffffff',
              boxShadow: '0px 4px 20px 0px #B4B4B41F',
              minHeight: '430px',
              height: '100%',
              boxSizing: 'border-box',
              '@media(max-width: 1170px)': {
                padding: '16px !important',
              },
              '@media(max-width: 1330px)': {
                minHeight: '430px',
              },
            }}
          >
            <Box display={'flex'} alignItems={'center'} justifyContent={'space-between'}>
              <TextWithBorder
                value='Automation'
                borderColor='#3B82F6'
                borderWidth='3px'
                sx={{ '& p': { fontSize: '20px', fontWeight: 600, color: '#374151' } }}
              />
              {hasWriteAccess(accountId) && (
                <CustomButton
                  text={'Add new'}
                  variant={'tertiary'}
                  startIcon={<SafeIcon src={addIcon} alt='add' />}
                  size='Small'
                  onClick={() => {
                    router.push(`/auto-pilot?accountId=${accountId}`);
                  }}
                />
              )}
            </Box>
            <Box
              display={'flex'}
              flexDirection={'column'}
              justifyContent={'space-between'}
              gap={'85px'}
              sx={{
                '@media(max-width: 1345px)': {
                  gap: '45px',
                },
              }}
            >
              <Box>
                <Box mt='24px'>
                  <TextWithBorder
                    value='Automation'
                    borderColor='#FACF39'
                    borderWidth='2px'
                    sx={{ '& p': { fontSize: '14px', fontWeight: 500, color: '#374151' } }}
                  />
                  <Box>
                    <Text value={'Configured'} secondaryText sx={{ pt: '10px' }} />
                    <TextWithBorder
                      value={workflowData.configuredCount}
                      borderColor='#FACF39'
                      borderWidth='0px'
                      sx={{
                        '&.MuiBox-root': {
                          padding: '0px !important',
                        },
                        '& p': { fontSize: '20px', fontWeight: 600, color: '#374151' },
                        span: {
                          button: {
                            padding: '0px',
                          },
                        },
                      }}
                      span={
                        <CustomIconButton
                          onClick={() => {
                            router.push(`/auto-pilot?accountId=${accountId}`);
                          }}
                        >
                          <SafeIcon src={ExternalLinkIcon} alt='redirect' />
                        </CustomIconButton>
                      }
                    />
                  </Box>
                </Box>
                <Divider sx={{ my: '15px', color: '#EBEBEB' }} />
                <Box>
                  <Text secondaryText value={'Actioned'} sx={{ pt: '10px' }} />
                  <TextWithBorder
                    value={workflowData.actionedCount}
                    borderColor='#FACF39'
                    borderWidth='0px'
                    sx={{
                      '&.MuiBox-root': {
                        padding: '0px !important',
                      },
                      '& p': { fontSize: '20px', fontWeight: 600, color: '#374151' },
                      span: {
                        fontSize: '12px',
                        fontWeight: 400,
                        color: '#737373',
                      },
                    }}
                    span={'times in last 24 hours'}
                  />
                </Box>
                <Divider sx={{ my: '15px', color: '#EBEBEB' }} />
                <Box>
                  <TextWithBorder
                    value='Optimization rules'
                    borderColor='#FACF39'
                    borderWidth='2px'
                    sx={{ '& p': { fontSize: '14px', fontWeight: 500, color: '#374151' } }}
                  />
                  <Text secondaryText value={'Configured'} sx={{ pt: '10px' }} />
                  <TextWithBorder
                    value={autoOptimizeData?.auto_pilot_aggregate?.aggregate.count}
                    borderColor='#FACF39'
                    borderWidth='0px'
                    sx={{
                      '&.MuiBox-root': {
                        padding: '0px !important',
                      },
                      '& p': { fontSize: '20px', fontWeight: 600, color: '#374151' },
                      span: {
                        button: {
                          padding: '0px',
                        },
                      },
                    }}
                    span={
                      <CustomIconButton
                        onClick={() => {
                          router.push(`/auto-pilot?accountId=${accountId}#auto-optimize`);
                        }}
                      >
                        <SafeIcon src={ExternalLinkIcon} alt='redirect' />
                      </CustomIconButton>
                    }
                  />
                  <Divider sx={{ my: '15px', color: '#EBEBEB' }} />
                  <Text value={'Actioned'} secondaryText sx={{ pt: '10px' }} />
                  <TextWithBorder
                    value={autoOptimizeData?.auto_pilot_task_aggregate?.aggregate.count}
                    borderColor='#FACF39'
                    borderWidth='0px'
                    sx={{
                      '&.MuiBox-root': {
                        padding: '0px !important',
                      },
                      '& p': { fontSize: '20px', fontWeight: 600, color: '#374151' },
                      span: {
                        fontSize: '12px',
                        fontWeight: 400,
                        color: '#737373',
                      },
                    }}
                    span={'times in last 7 days'}
                  />
                </Box>
              </Box>
            </Box>
          </CustomBorderCard>{' '}
        </Grid>
      </Grid>
      <Grid container my={'16px'}>
        <Grid item xs={12}>
          <CustomBorderCard padding='20px 24px' borderColor='transparent' borderLeftColor={'#BBF7D0'} borderWidth='4px'>
            <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
              <Box ref={optimizationRef} display='flex' alignItems={'center'}>
                <TextWithBorder
                  value='Optimizations'
                  borderColor='#3B82F6'
                  borderWidth='3px'
                  sx={{ '& p': { fontSize: '20px', fontWeight: 600, color: '#374151' } }}
                />
              </Box>
              <Box display={'flex'} gap='10px'>
                <SummaryBlock
                  hideTitle
                  sx={{
                    padding: '4px 16px',
                    borderRadius: '4px',
                    border: '0.5px solid #93C5FD',
                    backgroundColor: '#EFF6FF',
                    width: '150px',
                  }}
                >
                  <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
                    <Text value={'Total Count'} sx={{ fontSize: '12px' }} />
                    <Text value={totalOptimizeRecommendationsCount || '-'} sx={{ fontSize: '16px', fontWeight: 600 }} />
                  </Box>
                </SummaryBlock>
                <SummaryBlock
                  hideTitle
                  sx={{
                    padding: '4px 16px',
                    borderRadius: '4px',
                    border: '0.5px solid #86EFAC !important',
                    backgroundColor: '#F0FDF4',
                    width: '225px',
                  }}
                >
                  <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
                    <Text value={'Savings Potential'} sx={{ fontSize: '12px' }} showAutoEllipsis />
                    <Currency
                      sx={{ color: '#22C55E', fontSize: '16px', fontWeight: 500 }}
                      suffix='/yr'
                      value={yearlyOptimizedSavings}
                      isSavingPotential={true}
                      recommendationLabel='Some of cluster optimization recommendations'
                    />
                  </Box>
                </SummaryBlock>
              </Box>
            </Box>
            <Grid container spacing={2} my={'20px'}>
              {optimizeBlockData.map((data, index) => (
                <Grid item md={4} key={index}>
                  <SummaryBlock
                    hideTitle
                    sx={{
                      backgroundColor: '#FFFFFF',
                      border: '0.5px solid #D0D0D0 !important',
                      borderRadius: '8px',
                      padding: '10px 12px',
                      '& button': {
                        border: '0.75px solid #3B82F6 !important',
                        color: '#3B82F6 !important',
                        img: {
                          filter:
                            'brightness(0) saturate(100%) invert(39%) sepia(99%) saturate(1293%) hue-rotate(201deg) brightness(97%) contrast(99%)',
                        },
                      },
                    }}
                  >
                    <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
                      <TextWithBorder
                        value={data.title}
                        borderColor='#FACF39'
                        borderWidth='2px'
                        sx={{ '& p': { fontSize: '16px', fontWeight: 600, color: '#374151' } }}
                      />
                      <Box>
                        <Text value={'Est. Savings'} secondaryText sx={{ fontSize: '10px', textAlign: 'end' }} />
                        <Box display='flex' alignItems={'end'}>
                          <Currency
                            sx={{ color: '#22C55E', fontSize: '16px', fontWeight: 500, textAlign: 'right' }}
                            sxPrefix={{
                              pr: '5px',
                            }}
                            suffix=' /yr'
                            value={data.estimatedSavings}
                          />
                        </Box>
                      </Box>
                    </Box>
                    <Divider sx={{ my: '15px', color: '#EBEBEB' }} />
                    <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
                      <Text
                        value={`${data.optimizations} ${data.optimizations > 1 ? 'Optimizations' : 'Optimization'}`}
                        secondaryText
                        sx={{ '&:hover': { color: '#3B82F6', cursor: 'pointer' } }}
                        onClick={() => {
                          router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#${data.fragment}`);
                        }}
                      />
                      <Box>
                        <CustomButton
                          onClick={() => {
                            router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#${data.fragment}`);
                          }}
                          variant={'secondary'}
                          size='Small'
                          startIcon={<SafeIcon src={WhiteOptimizeIcon} alt='optimize' height={18} width={18} />}
                          text={'Optimize'}
                        />
                      </Box>
                    </Box>
                  </SummaryBlock>
                </Grid>
              ))}
            </Grid>
          </CustomBorderCard>
        </Grid>
      </Grid>
    </>
  );
};

export default KubernetesClusterSummaryUtilization;

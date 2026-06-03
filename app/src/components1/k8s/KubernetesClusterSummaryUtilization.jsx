import { Box, Divider, Grid, Typography } from '@mui/material';
import React, { useEffect, useState, useRef } from 'react';
import HeadingWithBorder from '@common-new/HeadingWithBorder';
import { Button as DSButton } from '@components1/ds/Button';
import CustomTable from '@common-new/tables/CustomTable2';
import { addIcon, ExternalLinkIcon } from '@assets';
import k8sApi from '@api1/kubernetes';
import apiAutoPilot from '@api1/autoPilot';
import apiWorkflow from '@api1/workflow';
import Datetime from '@common-new/format/Datetime';
import Text from '@common-new/format/Text';
import DSCard from '@components1/ds/Card';
import Currency from '@common-new/format/Currency';
import { useRouter } from 'next/router';
import recommendationApi from '@api1/recommendation';
import DSLink from '@components1/ds/Link';
import { Skeleton } from '@components1/ds/Skeleton';
import { SeverityIcon } from '@components1/ds/SeverityIcon';
import { hasWriteAccess } from '@lib/auth';
import SafeIcon from '@components1/common/SafeIcon';

// Maps the legacy severity labels to ds/SeverityIcon levels (Debug has no DS level → info).
const SEVERITY_LEVEL_BY_LABEL = {
  High: 'high',
  Medium: 'medium',
  Low: 'low',
  Debug: 'info',
};

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
        color: 'var(--ds-background-100)',
        background: 'var(--ds-red-500)',
      },
      {
        value: 0,
        label: 'Medium',
        color: 'var(--ds-red-500)',
        background: 'var(--ds-red-100)',
      },
      {
        value: 0,
        label: 'Low',
        color: 'var(--ds-amber-700)',
        background: 'var(--ds-amber-100)',
      },
      {
        value: 0,
        label: 'Debug',
        color: 'var(--ds-blue-500)',
        background: 'var(--ds-blue-100)',
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
          { value: firstRow.count_priority_high, label: 'High', color: 'var(--ds-background-100)', background: 'var(--ds-red-500)' },
          { value: firstRow.count_priority_medium, label: 'Medium', color: 'var(--ds-red-500)', background: 'var(--ds-red-100)' },
          { value: firstRow.count_priority_low, label: 'Low', color: 'var(--ds-amber-700)', background: 'var(--ds-amber-100)' },
          { value: firstRow.count_priority_debug, label: 'Debug', color: 'var(--ds-blue-500)', background: 'var(--ds-blue-100)' },
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
              <Datetime
                value={item.max_created_at}
                sx={{ fontSize: 'var(--ds-text-caption)', pl: '3px', textAlign: 'right' }}
                sxSuffix={{ fontSize: 'var(--ds-text-caption)' }}
              />
            </Box>
          </Box>
        ),
      },
      {
        component: (
          <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
            <DSLink
              href={`/kubernetes/details/${accountId}?eventAggregationKey=${item.aggregation_key}&eventPriority=HIGH#events/all-events`}
              style={{ color: 'var(--ds-blue-600)', fontSize: 'var(--ds-text-small)' }}
            >
              {item?.event_count}
            </DSLink>
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
                <Datetime
                  value={item.max_created_at}
                  sx={{ fontSize: 'var(--ds-text-caption)', pl: '3px', textAlign: 'right' }}
                  sxSuffix={{ fontSize: 'var(--ds-text-caption)' }}
                />
              </Box>
            </Box>
          ),
        },
        {
          component: (
            <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
              <DSLink
                href={`/kubernetes/details/${accountId}?eventAggregationKey=HighErrorCriticalLogs,ApplicationAPIFailures#events/all-events`}
                style={{ color: 'var(--ds-blue-600)', fontSize: 'var(--ds-text-small)' }}
              >
                {item?.event_count}
              </DSLink>
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
              <Datetime
                value={data.max_created_at}
                sx={{ fontSize: 'var(--ds-text-caption)', pl: '3px', textAlign: 'right' }}
                sxSuffix={{ fontSize: 'var(--ds-text-caption)' }}
              />
            </Box>
          </Box>
        ),
      },
      {
        component: (
          <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
            <DSLink
              href={`/kubernetes/details/${accountId}?eventTitle=${data.title}&eventAggregationKey=ApplicationAPIFailures#events/all-events`}
              style={{ color: 'var(--ds-blue-600)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}
            >
              {data?.event_count}
            </DSLink>
          </Box>
        ),
      },
    ]);
    setApiErrorsByCount(apiErrorsTableData);
  };

  const processWorkflowCount = async (_response) => {
    const count = _response?.data?.workflows_count?.count ?? 0;
    setWorkflowData((prev) => ({ ...prev, configuredCount: count }));
  };

  const processWorkflowExecutionCount = async (_response) => {
    const count = _response?.data?.workflows_count_executions?.count ?? 0;
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
          <DSCard sx={{ minHeight: '430px' }}>
            <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
              <Box ref={eventRef} display='flex' alignItems={'center'}>
                <HeadingWithBorder
                  value='Events/Errors'
                  borderColor='var(--ds-blue-500)'
                  borderWidth='3px'
                  sx={{ '& p': { fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' } }}
                />
                {eventTotalCountLoading ? (
                  <Skeleton shape='text' width='70px' />
                ) : (
                  <Typography
                    sx={{
                      border: '0.5px solid var(--ds-red-400)',
                      backgroundColor: 'var(--ds-red-100)',
                      p: '2px 8px',
                      borderRadius: 'var(--ds-radius-sm)',
                      color: 'var(--ds-gray-700)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                    }}
                  >
                    {eventSummaryData.totalEvents || 0}
                  </Typography>
                )}
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
                {eventTotalCountLoading ? (
                  <Skeleton shape='text' width='430px' />
                ) : (
                  eventSummaryData.severityData.map((data, index) => (
                    <Box
                      display='flex'
                      alignItems={'center'}
                      key={data.label}
                      sx={{
                        gap: 'var(--ds-space-3)',
                        '&::after': index !== eventSummaryData.severityData.length - 1 && {
                          content: '" "',
                          height: '16px',
                          border: `0.5px solid var(--ds-gray-200)`,
                        },
                      }}
                    >
                      <SeverityIcon
                        level={SEVERITY_LEVEL_BY_LABEL[data.label] || 'info'}
                        count={data.value || 0}
                        variant='square'
                        size={16}
                        aria-label={data.label}
                      />
                    </Box>
                  ))
                )}
              </Box>
            </Box>

            <Box display={'grid'} gridTemplateColumns={'1fr 1fr 1fr'} gap={'12px'} mt={'15px'}>
              <Box>
                <HeadingWithBorder
                  value='Critical Events - By Type'
                  borderColor='var(--ds-yellow-500)'
                  borderWidth='2px'
                  sx={{
                    '& p': { fontSize: 'var(--ds-text-body-lg) !important', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' },
                  }}
                  span={
                    <DSButton
                      tone='ghost'
                      composition='icon-only'
                      aria-label='Open grouped events'
                      icon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />}
                      onClick={() => {
                        router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#events/grouped-events`);
                      }}
                    />
                  }
                />
                {eventTypeDataLoading ? (
                  <Skeleton shape='rect' width='93%' height={300} />
                ) : (
                  <CustomTable
                    tableData={eventTypeData}
                    headers={['Event type', { name: 'Count', width: '5%' }]}
                    showUpdatedTable
                    showEmptyStateText
                  />
                )}
              </Box>
              <Box>
                <HeadingWithBorder
                  value='Critical Events - Recent'
                  borderColor='var(--ds-yellow-500)'
                  borderWidth='2px'
                  sx={{
                    '& p': { fontSize: 'var(--ds-text-body-lg) !important', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' },
                  }}
                  span={
                    <DSButton
                      tone='ghost'
                      composition='icon-only'
                      aria-label='Open grouped events'
                      icon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />}
                      onClick={() => {
                        router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#events/grouped-events`);
                      }}
                    />
                  }
                />
                {applicationEventDataLoading ? (
                  <Skeleton shape='rect' width='93%' height={300} />
                ) : (
                  <CustomTable
                    tableData={applicationEventData}
                    headers={['Application name', { name: 'Count', width: '5%' }]}
                    showUpdatedTable
                    showEmptyStateText
                  />
                )}
              </Box>{' '}
              <Box>
                <HeadingWithBorder
                  value='API Errors'
                  borderColor='var(--ds-yellow-500)'
                  borderWidth='2px'
                  sx={{
                    '& p': { fontSize: 'var(--ds-text-body-lg) !important', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' },
                  }}
                  span={
                    <DSButton
                      tone='ghost'
                      composition='icon-only'
                      aria-label='Open API errors'
                      icon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />}
                      onClick={() => {
                        router.push(`/kubernetes/details/${accountId}?eventAggregationKey=ApplicationAPIFailures#events/all-events`);
                      }}
                    />
                  }
                />
                {apiErrorsByCountLoading ? (
                  <Skeleton shape='rect' width='93%' height={300} />
                ) : (
                  <CustomTable
                    rowsPerPage={5}
                    tableData={apiErrorsByCount}
                    headers={['URL', { name: 'Count', width: '5%' }]}
                    showUpdatedTable
                    showEmptyStateText
                  />
                )}
              </Box>{' '}
            </Box>
          </DSCard>
        </Grid>
        <Grid item xs={3}>
          <DSCard sx={{ minHeight: '430px', height: '100%', boxSizing: 'border-box' }}>
            <Box display={'flex'} alignItems={'center'} justifyContent={'space-between'}>
              <HeadingWithBorder
                value='Automation'
                borderColor='var(--ds-blue-500)'
                borderWidth='3px'
                sx={{ '& p': { fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' } }}
              />
              {hasWriteAccess(accountId) && (
                <DSButton
                  tone='secondary'
                  size='sm'
                  icon={<SafeIcon src={addIcon} alt='add' />}
                  onClick={() => {
                    router.push(`/auto-pilot?accountId=${accountId}`);
                  }}
                >
                  Add new
                </DSButton>
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
                  <HeadingWithBorder
                    value='Automation'
                    borderColor='var(--ds-yellow-500)'
                    borderWidth='2px'
                    sx={{ '& p': { fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' } }}
                  />
                  <Box>
                    <Text value={'Configured'} secondaryText sx={{ pt: '10px' }} />
                    <HeadingWithBorder
                      value={workflowData.configuredCount}
                      borderColor='var(--ds-yellow-500)'
                      borderWidth='0px'
                      sx={{
                        '&.MuiBox-root': {
                          padding: '0px !important',
                        },
                        '& p': { fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' },
                        span: {
                          button: {
                            padding: '0px',
                          },
                        },
                      }}
                      span={
                        <DSButton
                          tone='ghost'
                          composition='icon-only'
                          aria-label='Open automation'
                          icon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />}
                          onClick={() => {
                            router.push(`/auto-pilot?accountId=${accountId}`);
                          }}
                        />
                      }
                    />
                  </Box>
                </Box>
                <Divider sx={{ my: '15px', color: 'var(--ds-gray-200)' }} />
                <Box>
                  <Text secondaryText value={'Actioned'} sx={{ pt: '10px' }} />
                  <HeadingWithBorder
                    value={workflowData.actionedCount}
                    borderColor='var(--ds-yellow-500)'
                    borderWidth='0px'
                    sx={{
                      '&.MuiBox-root': {
                        padding: '0px !important',
                      },
                      '& p': { fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' },
                      span: {
                        fontSize: 'var(--ds-text-small)',
                        fontWeight: 'var(--ds-font-weight-regular)',
                        color: 'var(--ds-gray-600)',
                      },
                    }}
                    span={'times in last 24 hours'}
                  />
                </Box>
                <Divider sx={{ my: '15px', color: 'var(--ds-gray-200)' }} />
                <Box>
                  <HeadingWithBorder
                    value='Optimization rules'
                    borderColor='var(--ds-yellow-500)'
                    borderWidth='2px'
                    sx={{ '& p': { fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' } }}
                  />
                  <Text secondaryText value={'Configured'} sx={{ pt: '10px' }} />
                  <HeadingWithBorder
                    value={autoOptimizeData?.auto_pilot_aggregate?.aggregate.count}
                    borderColor='var(--ds-yellow-500)'
                    borderWidth='0px'
                    sx={{
                      '&.MuiBox-root': {
                        padding: '0px !important',
                      },
                      '& p': { fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' },
                      span: {
                        button: {
                          padding: '0px',
                        },
                      },
                    }}
                    span={
                      <DSButton
                        tone='ghost'
                        composition='icon-only'
                        aria-label='Open auto-optimize'
                        icon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />}
                        onClick={() => {
                          router.push(`/auto-pilot?accountId=${accountId}#auto-optimize`);
                        }}
                      />
                    }
                  />
                  <Divider sx={{ my: '15px', color: 'var(--ds-gray-200)' }} />
                  <Text value={'Actioned'} secondaryText sx={{ pt: '10px' }} />
                  <HeadingWithBorder
                    value={autoOptimizeData?.auto_pilot_task_aggregate?.aggregate.count}
                    borderColor='var(--ds-yellow-500)'
                    borderWidth='0px'
                    sx={{
                      '&.MuiBox-root': {
                        padding: '0px !important',
                      },
                      '& p': { fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' },
                      span: {
                        fontSize: 'var(--ds-text-small)',
                        fontWeight: 'var(--ds-font-weight-regular)',
                        color: 'var(--ds-gray-600)',
                      },
                    }}
                    span={'times in last 7 days'}
                  />
                </Box>
              </Box>
            </Box>
          </DSCard>{' '}
        </Grid>
      </Grid>
      <Grid container my={'var(--ds-space-4)'}>
        <Grid item xs={12}>
          <DSCard variant='accent' tone='success' size='md'>
            <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
              <Box ref={optimizationRef} display='flex' alignItems={'center'}>
                <HeadingWithBorder
                  value='Optimizations'
                  borderColor='var(--ds-blue-500)'
                  borderWidth='3px'
                  sx={{ '& p': { fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' } }}
                />
              </Box>
              <Box display={'flex'} gap='10px'>
                <Box
                  sx={{
                    padding: '4px var(--ds-space-4)',
                    borderRadius: 'var(--ds-radius-sm)',
                    border: '0.5px solid var(--ds-blue-300)',
                    backgroundColor: 'var(--ds-blue-100)',
                    width: '150px',
                  }}
                >
                  <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
                    <Text value={'Total Count'} sx={{ fontSize: 'var(--ds-text-small)' }} />
                    <Text
                      value={totalOptimizeRecommendationsCount || '-'}
                      sx={{ fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)' }}
                    />
                  </Box>
                </Box>
                <Box
                  sx={{
                    padding: '4px var(--ds-space-4)',
                    borderRadius: 'var(--ds-radius-sm)',
                    border: '0.5px solid var(--ds-green-300)',
                    backgroundColor: 'var(--ds-green-100)',
                    width: '225px',
                  }}
                >
                  <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
                    <Text value={'Savings Potential'} sx={{ fontSize: 'var(--ds-text-small)' }} showAutoEllipsis />
                    <Currency
                      sx={{ color: 'var(--ds-green-500)', fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-medium)' }}
                      suffix='/yr'
                      value={yearlyOptimizedSavings}
                      isSavingPotential={true}
                      recommendationLabel='Some of cluster optimization recommendations'
                    />
                  </Box>
                </Box>
              </Box>
            </Box>
            <Grid container spacing={2} my={'20px'}>
              {optimizeBlockData.map((data, index) => (
                <Grid item md={4} key={index}>
                  <DSCard variant='outlined' size='sm'>
                    <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
                      <HeadingWithBorder
                        value={data.title}
                        borderColor='var(--ds-yellow-500)'
                        borderWidth='2px'
                        sx={{
                          '& p': { fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' },
                        }}
                      />
                      <Box>
                        <Text value={'Est. Savings'} secondaryText sx={{ fontSize: 'var(--ds-text-caption)', textAlign: 'end' }} />
                        <Box display='flex' alignItems={'end'}>
                          <Currency
                            sx={{
                              color: 'var(--ds-green-500)',
                              fontSize: 'var(--ds-text-title)',
                              fontWeight: 'var(--ds-font-weight-medium)',
                              textAlign: 'right',
                            }}
                            sxPrefix={{
                              pr: '5px',
                            }}
                            suffix=' /yr'
                            value={data.estimatedSavings}
                          />
                        </Box>
                      </Box>
                    </Box>
                    <Divider sx={{ my: 'var(--ds-space-3)', borderColor: 'var(--ds-gray-200)' }} />
                    <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
                      <Text
                        value={`${data.optimizations} ${data.optimizations > 1 ? 'Optimizations' : 'Optimization'}`}
                        secondaryText
                        sx={{ '&:hover': { color: 'var(--ds-blue-500)', cursor: 'pointer' } }}
                        onClick={() => {
                          router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#${data.fragment}`);
                        }}
                      />
                      <Box>
                        <DSButton
                          tone='secondary'
                          size='sm'
                          onClick={() => {
                            router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#${data.fragment}`);
                          }}
                        >
                          Optimize
                        </DSButton>
                      </Box>
                    </Box>
                  </DSCard>
                </Grid>
              ))}
            </Grid>
          </DSCard>
        </Grid>
      </Grid>
    </>
  );
};

export default KubernetesClusterSummaryUtilization;

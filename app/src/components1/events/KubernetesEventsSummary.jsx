import { Box, Divider, Grid, Typography } from '@mui/material';
import React, { useEffect, useState } from 'react';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import TextWithBorder from '@components1/common/TextWithBorder';
import CustomIconButton from '@components1/CustomIconButton';
import { addIcon, ExternalLinkIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import Datetime from '@components1/common/format/Datetime';
import Text from '@components1/common/format/Text';
import CustomTable from '@components1/common/tables/CustomTable2';
import { useRouter } from 'next/router';
import k8sApi from '@api1/kubernetes';
import apiWorkflow from '@api1/workflow';
import { hasWriteAccess } from '@lib/auth';
import { getLast24Hrs } from '@lib/datetime';
import CustomLink from '@components1/common/CustomLink';
import ShimmerLoading from '@components1/common/ShimmerLoading';
import CustomButton from '@components1/common/NewCustomButton';
import { colors } from 'src/utils/colors';
import { titleCaseForAggregationKey } from 'src/utils/common';

export default function KubernetesEventsSummary({ accountId }) {
  const router = useRouter();
  const dateRange = {
    startDate: getLast24Hrs(),
    endDate: new Date(),
  };

  const [eventTypeData, setEventTypeData] = useState([]);
  const [applicationEventData, setApplicationEventData] = useState([]);
  const [recentData, setRecentData] = useState([]);
  const [nodeErrorTableData, setNodeErrorTableData] = useState([]);
  const [eventSummaryData, setEventSummaryData] = useState({
    severityData: [
      {
        value: 0,
        label: 'High',
        color: colors.text.white,
        background: colors.background.criticalRed,
      },
      {
        value: 0,
        label: 'Medium',
        color: colors.highest,
        background: colors.background.medium,
      },
      {
        value: 0,
        label: 'Low',
        color: colors.text.low,
        background: colors.background.low,
      },
      {
        value: 0,
        label: 'Debug',
        color: colors.text.primaryDark,
        background: colors.background.primaryLightest,
      },
    ],
    highEvents: 0,
    applicationEvents: 0,
    podEvents: 0,
    nodeEvents: 0,
  });
  const [apiErrorsByCount, setApiErrorsByCount] = useState([]);
  const [apiErrorsRecent, setApiErrorsRecent] = useState([]);
  const [workflowData, setWorkflowData] = useState({ totalCount: 0, configuredCount: 0, actionedCount: 0 });
  const [loadingData, setLoadingData] = useState({
    eventTypeDataLoading: false,
    applicationEventDataLoading: false,
    eventRecentDataLoading: false,
    nodeErrorTableDataLoading: false,
    apiErrorsByCountLoading: false,
    apiErrorsRecentLoading: false,
    eventTotalCountLoading: false,
    workflowDataLoading: false,
  });

  // summary data
  useEffect(() => {
    if (!accountId) {
      return;
    }
    setLoadingData((prev) => ({ ...prev, eventTotalCountLoading: true }));
    k8sApi
      .getK8sEventGroupings(
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
      )
      .then((response) => {
        let firstRow = response.data?.event_groupings?.[0];
        if (firstRow) {
          eventSummaryData.nodeEvents = firstRow.count_node_issues;
          eventSummaryData.highEvents = firstRow.count_priority_high;
          eventSummaryData.applicationEvents = firstRow.count_application_issues;
          eventSummaryData.podEvents = firstRow.count_pod_issues;
          eventSummaryData.severityData[0].value = firstRow.count_priority_high;
          eventSummaryData.severityData[1].value = firstRow.count_priority_medium;
          eventSummaryData.severityData[2].value = firstRow.count_priority_low;
          eventSummaryData.severityData[3].value = firstRow.count_priority_debug;
        }
        setEventSummaryData({ ...eventSummaryData });
      })
      .finally(() => {
        setLoadingData((prev) => ({ ...prev, eventTotalCountLoading: false }));
      });
  }, [accountId]);

  // eventTypeData table
  useEffect(() => {
    if (!accountId) {
      return;
    }
    setLoadingData((prev) => ({ ...prev, eventTypeDataLoading: true }));
    k8sApi
      .getK8sEventGroupings(
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
      )
      .then((response) => {
        let tableData =
          response?.data?.event_groupings?.map((item) => {
            return [
              {
                component: (
                  <Box>
                    <Text showAutoEllipsis value={titleCaseForAggregationKey(item.aggregation_key)} />
                    <Box display={'flex'} alignItems={'center'}>
                      <Text value={'Last occ:'} secondaryText />
                      <Datetime value={item.max_created_at} sx={{ fontSize: '12px', pl: '3px', textAlign: 'right' }} />
                    </Box>
                  </Box>
                ),
              },
              {
                component: (
                  <Typography textAlign={'end'}>
                    <CustomLink
                      href={`/kubernetes/details/${accountId}?eventAggregationKey=${item.aggregation_key}&eventPriority=HIGH#events/all-events`}
                      style={{ color: colors.text.primary, fontSize: '12px' }}
                    >
                      {item?.event_count}
                    </CustomLink>
                  </Typography>
                ),
              },
            ];
          }) || [];
        setEventTypeData(tableData);
      })
      .finally(() => {
        setLoadingData((prev) => ({ ...prev, eventTypeDataLoading: false }));
      });
  }, [accountId]);

  // application events
  useEffect(() => {
    if (!accountId) {
      return;
    }
    setLoadingData((prev) => ({ ...prev, applicationEventDataLoading: true }));
    k8sApi
      .getK8sEventGroupings(
        5,
        0,
        {
          account_id: accountId,
          start_date: dateRange.startDate,
          end_date: dateRange.endDate,
          aggregation_key: [],
        },
        ['tenant_id', 'account_id', 'subject_owner', 'subject_namespace'],
        ['max_created_at', 'event_count', 'subject_owner', 'subject_namespace'],
        { name: 'event_count', order: 'desc' }
      )
      .then((response) => {
        let tableData =
          (response?.data?.event_groupings || [])?.map((item) => {
            return [
              {
                component: (
                  <Box>
                    <Text showAutoEllipsis value={item.subject_owner} />
                    <Box sx={{ display: 'flex', gap: '10px' }}>
                      <Box display={'flex'} alignItems={'center'}>
                        <Text value={'Last occ:'} secondaryText />
                        <Datetime value={item.max_created_at} sx={{ fontSize: '12px', pl: '3px', textAlign: 'right' }} />
                      </Box>
                      <Box display={'flex'} alignItems={'center'}>
                        <Text value={'ns: '} secondaryText />
                        <Text value={item.subject_namespace} showAutoEllipsis sx={{ fontSize: '12px' }} />
                      </Box>
                    </Box>
                  </Box>
                ),
              },
              {
                component: (
                  <Typography textAlign={'end'}>
                    <CustomLink
                      href={`/kubernetes/details/${accountId}?eventAggregationKey=HighErrorCriticalLogs,ApplicationAPIFailures&eventNamespace=${item.subject_namespace}&eventSubjectName=${item.subject_owner}&exact=true#events/all-events`}
                      style={{ color: colors.text.primary, fontSize: '12px' }}
                    >
                      {item?.event_count}
                    </CustomLink>
                  </Typography>
                ),
              },
            ];
          }) || [];
        setApplicationEventData(tableData);
      })
      .finally(() => {
        setLoadingData((prev) => ({ ...prev, applicationEventDataLoading: false }));
      });
  }, [accountId]);

  // eventRecentData table
  useEffect(() => {
    if (!accountId) {
      return;
    }
    setLoadingData((prev) => ({ ...prev, eventRecentDataLoading: true }));
    k8sApi
      .getK8sEvents(5, 0, {
        account_id: accountId,
        start_date: dateRange.startDate,
        end_date: dateRange.endDate,
      })
      .then((response) => {
        let tableData =
          response?.data?.events?.map((item) => {
            return [
              {
                component: <Text showAutoEllipsis value={item.aggregation_key} />,
              },
              {
                component: (
                  <Box display={'flex'} justifyContent={'flex-end'}>
                    <CustomLink href={`/kubernetes/details/${accountId}#events/all-events`} style={{ color: colors.text.secondary }}>
                      <Datetime value={item.starts_at} sx={{ pl: '3px', textAlign: 'right' }} />
                    </CustomLink>
                  </Box>
                ),
              },
            ];
          }) || [];
        setRecentData(tableData);
      })
      .finally(() => {
        setLoadingData((prev) => ({ ...prev, eventRecentDataLoading: false }));
      });
  }, [accountId]);

  // node error
  useEffect(() => {
    if (!accountId) {
      return;
    }
    setLoadingData((prev) => ({ ...prev, nodeErrorTableDataLoading: true }));
    k8sApi
      .getK8sEventGroupings(
        5,
        0,
        {
          account_id: accountId,
          start_date: dateRange.startDate,
          end_date: dateRange.endDate,
          subject_type: 'node',
        },
        ['tenant_id', 'account_id', 'subject_name'],
        ['max_created_at', 'event_count', 'subject_name'],
        { name: 'event_count', order: 'desc' }
      )
      .then((response) => {
        let tableData =
          response?.data?.event_groupings?.map((data) => {
            return [
              {
                component: <Text showAutoEllipsis value={data.subject_name} />,
              },
              {
                component: (
                  <CustomLink
                    href={`/kubernetes/details/${accountId}?eventSubjectName=${data.subject_name}&eventSubjectType=node#events/all-events`}
                    style={{ color: colors.text.primary, fontSize: '12px', fontWeight: 500 }}
                  >
                    {data?.event_count}
                  </CustomLink>
                ),
              },
              {
                component: (
                  <Box display={'flex'} justifyContent={'flex-end'}>
                    <Datetime value={data.max_created_at} sx={{ pl: '3px', textAlign: 'right' }} sxSuffix={{ fontSize: '11px' }} />
                  </Box>
                ),
              },
            ];
          }) || [];
        setNodeErrorTableData(tableData);
      })
      .finally(() => {
        setLoadingData((prev) => ({ ...prev, nodeErrorTableDataLoading: false }));
      });
  }, [accountId]);

  // api error
  useEffect(() => {
    if (!accountId) {
      return;
    }
    setLoadingData((prev) => ({ ...prev, apiErrorsByCountLoading: true }));
    k8sApi
      .getK8sEventGroupings(
        5,
        0,
        {
          account_id: accountId,
          start_date: dateRange.startDate,
          end_date: dateRange.endDate,
          aggregation_key: 'ApplicationAPIFailures',
        },
        ['tenant_id', 'account_id', 'title'],
        ['max_created_at', 'event_count', 'subject_namespace', 'subject_owner', 'title'],
        { name: 'event_count', order: 'desc' }
      )
      .then((response) => {
        let tableData =
          response?.data?.event_groupings?.map((data) => {
            return [
              {
                component: (
                  <>
                    <Text showAutoEllipsis value={data.title?.replace('High API Failure for', '')} />
                    <Text secondaryText value={'ns: ' + data.subject_namespace} />
                    <Text secondaryText value={'app: ' + data.subject_owner} />
                  </>
                ),
              },
              {
                component: (
                  <CustomLink
                    href={`/kubernetes/details/${accountId}?eventTitle=${data.title}&eventAggregationKey=ApplicationAPIFailures#events/all-events`}
                    style={{ color: colors.text.primary, fontSize: '12px', fontWeight: 500 }}
                  >
                    {data?.event_count}
                  </CustomLink>
                ),
              },
              {
                component: (
                  <Box display={'flex'} justifyContent={'flex-end'}>
                    <Datetime value={data.max_created_at} sx={{ pl: '3px', textAlign: 'right' }} sxSuffix={{ fontWeight: 400 }} />
                  </Box>
                ),
              },
            ];
          }) || [];
        setApiErrorsByCount(tableData);
      })
      .finally(() => {
        setLoadingData((prev) => ({ ...prev, apiErrorsByCountLoading: false }));
      });
  }, [accountId]);

  // api RecentData table
  useEffect(() => {
    if (!accountId) {
      return;
    }
    setLoadingData((prev) => ({ ...prev, apiErrorsRecentLoading: true }));
    k8sApi
      .getK8sEvents(5, 0, {
        account_id: accountId,
        aggregation_key: 'ApplicationAPIFailures',
        start_date: dateRange.startDate,
        end_date: dateRange.endDate,
        onlyData: true,
      })
      .then((response) => {
        let tableData =
          response?.data?.events?.map((item) => {
            return [
              {
                component: (
                  <>
                    <Text showAutoEllipsis value={item.title?.replace('High API Failure for', '')} />
                    <Text secondaryText value={'ns: ' + item.subject_namespace} />
                    <Text secondaryText value={'app: ' + item.subject_owner} />
                  </>
                ),
              },
              {
                component: (
                  <Box display={'flex'} justifyContent={'flex-end'}>
                    <Datetime value={item.starts_at} sx={{ pl: '3px', textAlign: 'right' }} />
                  </Box>
                ),
              },
            ];
          }) || [];
        setApiErrorsRecent(tableData);
      })
      .finally(() => {
        setLoadingData((prev) => ({ ...prev, apiErrorsRecentLoading: false }));
      });
  }, [accountId]);

  // workflowData
  useEffect(() => {
    if (!accountId) {
      return;
    }
    setLoadingData((prev) => ({ ...prev, workflowDataLoading: true }));

    const fetchWorkflowData = async () => {
      try {
        const [totalResponse, configuredResponse, actionedResponse] = await Promise.all([
          apiWorkflow.getWorkflowCount(accountId, { status: 'ACTIVE' }),
          apiWorkflow.getWorkflowCount(accountId, { status: 'ACTIVE', triggerType: 'event' }),
          apiWorkflow.getWorkflowExecutionCount(accountId, { startDate: dateRange.startDate, triggerType: 'event' }),
        ]);

        setWorkflowData({
          totalCount: totalResponse?.data?.workflow_get_count?.count ?? 0,
          configuredCount: configuredResponse?.data?.workflow_get_count?.count ?? 0,
          actionedCount: actionedResponse?.data?.workflow_get_execution_count?.count ?? 0,
        });
      } catch (error) {
        console.error('Failed to fetch workflow data:', error);
      } finally {
        setLoadingData((prev) => ({ ...prev, workflowDataLoading: false }));
      }
    };

    fetchWorkflowData();
  }, [accountId]);

  return (
    <>
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: 'repeat(10, 1fr)',
          gap: 1,
          mt: '1px',
        }}
      >
        <Box sx={{ gridColumn: 'span 8 ' }}>
          <SummaryBlock
            hideTitle
            height='100%'
            sx={{
              height: '100%',
              borderColor: 'transparent',
              backgroundColor: colors.background.white,
              boxShadow: colors.shadow.softGray,
              '@media(max-width: 1170px)': {
                padding: '16px !important',
              },
            }}
          >
            <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
              <Box display='flex' alignItems={'center'}>
                <TextWithBorder
                  value='Last 24hrs'
                  borderColor={colors.text.primaryDark}
                  borderWidth='3px'
                  sx={{ '& p': { fontSize: '16px', fontWeight: 600, color: colors.text.secondary } }}
                />
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
                <ShimmerLoading isLoading={loadingData.eventTotalCountLoading} height={'10px'} width={'430px'}>
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
                          boxShadow: colors.shadow.softBlack,
                          fontWeight: 700,
                          fontSize: '11px',
                          mr: '5px',
                        }}
                      >
                        {data.value || 0}
                      </Typography>
                      <Typography sx={{ fontSize: '12px', fontWeight: 400, color: colors.text.tertiary, mr: '10px' }}>{data.label}</Typography>
                    </Box>
                  ))}
                </ShimmerLoading>
              </Box>
            </Box>

            <Box display={'grid'} gridTemplateColumns={'1fr 1fr 1fr'} gap={'12px'} mt={'12px'}>
              <Box>
                <TextWithBorder
                  value='By Event type'
                  borderColor={colors.nudgebeeMain}
                  borderWidth='2px'
                  sx={{ '& p': { fontSize: '13px !important', fontWeight: 500, color: colors.text.secondary } }}
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
                <ShimmerLoading isLoading={loadingData.eventTypeDataLoading} width='93%'>
                  <CustomTable
                    tableData={eventTypeData}
                    headers={[
                      { name: 'Event type', width: '80%' },
                      { name: 'Count', width: '20%' },
                    ]}
                    showUpdatedTable
                    showEmptyStateText
                    rowsPerPage={eventTypeData.length}
                    totalRows={eventTypeData.length}
                  />
                </ShimmerLoading>
              </Box>
              <Box>
                <TextWithBorder
                  value='By Applications'
                  borderColor={colors.nudgebeeMain}
                  borderWidth='2px'
                  sx={{ '& p': { fontSize: '13px !important', fontWeight: 500, color: colors.text.secondary } }}
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
                <ShimmerLoading isLoading={loadingData.applicationEventDataLoading} width='93%'>
                  <CustomTable
                    tableData={applicationEventData}
                    headers={[
                      { name: 'Application name', width: '80%' },
                      { name: 'Count', width: '20%' },
                    ]}
                    showUpdatedTable
                    showEmptyStateText
                    rowsPerPage={applicationEventData.length}
                    totalRows={applicationEventData.length}
                  />
                </ShimmerLoading>
              </Box>{' '}
              <Box>
                <TextWithBorder
                  value='Most Recent'
                  borderColor={colors.nudgebeeMain}
                  borderWidth='2px'
                  sx={{ '& p': { fontSize: '13px !important', fontWeight: 500, color: colors.text.secondary } }}
                  span={
                    <CustomIconButton
                      onClick={() => {
                        router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#events/all-events`);
                      }}
                    >
                      <SafeIcon src={ExternalLinkIcon} alt='redirect' />
                    </CustomIconButton>
                  }
                />
                <ShimmerLoading isLoading={loadingData.eventRecentDataLoading} width='93%'>
                  <CustomTable
                    tableData={recentData}
                    headers={[
                      { name: 'Event', width: '65%' },
                      { name: 'Last occurred', width: '35%' },
                    ]}
                    showUpdatedTable
                    showEmptyStateText
                    rowsPerPage={recentData.length}
                    totalRows={recentData.length}
                  />
                </ShimmerLoading>
              </Box>{' '}
            </Box>
          </SummaryBlock>
        </Box>
        <Box sx={{ gridColumn: 'span 2' }}>
          <SummaryBlock
            hideTitle
            height='100%'
            sx={{
              height: '100%',
              borderColor: 'transparent',
              backgroundColor: colors.background.white,
              boxShadow: colors.shadow.softGray,
              minHeight: '430px',
              '@media(max-width: 1170px)': {
                padding: '16px !important',
              },
              '@media(max-width: 1330px)': {
                minHeight: '430px',
              },
            }}
          >
            <TextWithBorder
              value='Automations'
              borderColor={colors.text.primaryDark}
              borderWidth='3px'
              sx={{
                '& p': {
                  fontSize: '16px',
                  fontWeight: 600,
                  color: colors.text.secondary,
                  '@media(max-width: 1350px)': {
                    fontSize: '18px !important',
                  },
                },
              }}
            />
            <Box
              display={'flex'}
              flexDirection={'column'}
              justifyContent={'space-between'}
              sx={{
                height: '94%',
                '@media(max-width: 1345px)': {
                  height: '85%',
                },
              }}
            >
              <Box>
                <Box mt='24px'>
                  <Typography sx={{ color: colors.text.secondaryDark, fontSize: '12px', fontWeight: 400 }}>Automations</Typography>
                  <ShimmerLoading isLoading={loadingData.workflowDataLoading} height={'10px'} width={'70px'}>
                    <Typography sx={{ color: colors.text.secondary, fontSize: '28px', fontWeight: 600 }}>{workflowData.totalCount || '-'}</Typography>
                  </ShimmerLoading>
                </Box>
                <Divider sx={{ my: '15px', color: colors.text.divider }} />
                <Box mt='24px'>
                  <Typography sx={{ color: colors.text.secondaryDark, fontSize: '12px', fontWeight: 400 }}>Event Automations Configured</Typography>
                  <ShimmerLoading isLoading={loadingData.workflowDataLoading} height={'10px'} width={'70px'}>
                    <Typography sx={{ color: colors.text.secondary, fontSize: '28px', fontWeight: 600 }}>{workflowData.configuredCount}</Typography>
                  </ShimmerLoading>
                </Box>
                <Divider sx={{ my: '15px', color: colors.text.divider }} />
                <Box mt='24px'>
                  <Typography sx={{ color: colors.text.secondaryDark, fontSize: '12px', fontWeight: 400 }}>
                    Event Automations Triggered in Last 24 Hours
                  </Typography>
                  <ShimmerLoading isLoading={loadingData.workflowDataLoading} height={'10px'} width={'70px'}>
                    <Typography sx={{ color: colors.text.secondary, fontSize: '28px', fontWeight: 400 }}>{workflowData.actionedCount}</Typography>
                  </ShimmerLoading>
                </Box>
              </Box>
              <Box sx={{ mt: 'auto' }}>
                <Box
                  display={'flex'}
                  gap={'8px'}
                  mt='auto'
                  justifyContent={'center'}
                  sx={{
                    '& > *': { flex: 1 },
                    '& button': { whiteSpace: 'nowrap' },
                    '@media(max-width: 1330px)': {
                      gap: '5px',
                      '& button': {
                        padding: '0px 10px',
                        whiteSpace: 'nowrap',
                      },
                    },
                    '@media(max-width: 1030px)': {
                      flexDirection: 'column',
                      mt: '10px',
                      alignItems: 'center',
                    },
                  }}
                >
                  {hasWriteAccess(accountId) ? (
                    <CustomButton
                      text={'Add new'}
                      variant='tertiary'
                      size='xSmall'
                      startIcon={<SafeIcon src={addIcon} alt='add' />}
                      onClick={() => {
                        router.push(`/workflow/new?accountId=${accountId}`);
                      }}
                    />
                  ) : (
                    <></>
                  )}
                  <CustomButton
                    text={'View all'}
                    variant='secondary'
                    size='xSmall'
                    onClick={() => {
                      router.push(`/auto-pilot?accountId=${accountId}`);
                    }}
                  />
                </Box>
              </Box>
            </Box>
          </SummaryBlock>{' '}
        </Box>
      </Box>
      <Grid container spacing={1} mt={'1px'}>
        <Grid item xs={7}>
          <SummaryBlock
            hideTitle
            height='100%'
            sx={{
              height: '100%',
              borderColor: 'transparent',
              backgroundColor: colors.background.white,
              boxShadow: colors.shadow.softGray,
              '@media(max-width: 1170px)': {
                padding: '16px !important',
              },
            }}
          >
            <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
              <Box display='flex' alignItems={'center'}>
                <TextWithBorder
                  value='Application Errors'
                  borderColor={colors.text.primaryDark}
                  borderWidth='3px'
                  sx={{ '& p': { fontSize: '16px', fontWeight: 600, color: colors.text.secondary } }}
                />
                <Typography
                  sx={{
                    border: `0.5px solid ${colors.high}`,
                    backgroundColor: colors.background.accordionSummay,
                    p: '2px 6px',
                    borderRadius: '4px',
                    color: colors.text.secondary,
                    fontWeight: 500,
                    fontSize: '12px',
                  }}
                >
                  {eventSummaryData.applicationEvents}
                </Typography>
              </Box>
            </Box>

            <Box display={'grid'} gridTemplateColumns={'1fr 1fr'} gap={'12px'} mt={'12px'}>
              <Box>
                <TextWithBorder
                  value='API Errors - By Count'
                  borderColor={colors.nudgebeeMain}
                  borderWidth='2px'
                  sx={{ '& p': { fontSize: '13px !important', fontWeight: 500, color: colors.text.secondary } }}
                  span={
                    <CustomIconButton
                      onClick={() => {
                        router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#events/app-errors`);
                      }}
                    >
                      <SafeIcon src={ExternalLinkIcon} alt='redirect' />
                    </CustomIconButton>
                  }
                />
                <ShimmerLoading isLoading={loadingData.apiErrorsByCountLoading} width='93%'>
                  <CustomTable
                    tableData={apiErrorsByCount}
                    headers={[{ name: 'API', width: '70%' }, { name: 'Count' }, { name: 'Last occurred' }]}
                    showUpdatedTable
                    showEmptyStateText
                    rowsPerPage={apiErrorsByCount.length}
                    totalRows={apiErrorsByCount.length}
                  />
                </ShimmerLoading>
              </Box>
              <Box>
                <TextWithBorder
                  value='API Errors - Most Recent'
                  borderColor={colors.nudgebeeMain}
                  borderWidth='2px'
                  sx={{ '& p': { fontSize: '13px !important', fontWeight: 500, color: colors.text.secondary } }}
                  span={
                    <CustomIconButton
                      onClick={() => {
                        router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#events/app-errors`);
                      }}
                    >
                      <SafeIcon src={ExternalLinkIcon} alt='redirect' />
                    </CustomIconButton>
                  }
                />
                <ShimmerLoading isLoading={loadingData.apiErrorsRecentLoading} width='93%'>
                  <CustomTable
                    tableData={apiErrorsRecent}
                    headers={[{ name: 'API', width: '70%' }, 'Last occurred']}
                    showUpdatedTable
                    showEmptyStateText
                    rowsPerPage={apiErrorsRecent.length}
                    totalRows={apiErrorsRecent.length}
                  />
                </ShimmerLoading>
              </Box>
            </Box>
          </SummaryBlock>
        </Grid>
        <Grid item xs={5}>
          <SummaryBlock
            hideTitle
            height='100%'
            sx={{
              height: '100%',
              borderColor: 'transparent',
              backgroundColor: colors.background.white,
              boxShadow: colors.shadow.softGray,
              '@media(max-width: 1170px)': {
                padding: '16px !important',
              },
            }}
          >
            <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
              <Box display='flex' alignItems={'center'}>
                <TextWithBorder
                  value='Node Errors'
                  borderColor={colors.text.primaryDark}
                  borderWidth='3px'
                  sx={{ '& p': { fontSize: '16px', fontWeight: 600, color: colors.text.secondary } }}
                />
                <Typography
                  sx={{
                    border: `0.5px solid ${colors.high}`,
                    backgroundColor: colors.background.accordionSummay,
                    p: '2px 6px',
                    borderRadius: '4px',
                    color: colors.text.secondary,
                    fontWeight: 500,
                    fontSize: '12px',
                  }}
                >
                  {eventSummaryData.nodeEvents}
                </Typography>
              </Box>
            </Box>

            <Box display={'grid'} gridTemplateColumns={'1fr'} gap={'12px'} mt={'12px'}>
              <Box>
                <TextWithBorder
                  value='By Node'
                  borderColor={colors.nudgebeeMain}
                  borderWidth='2px'
                  sx={{ '& p': { fontSize: '13px !important', fontWeight: 500, color: colors.text.secondary } }}
                  span={
                    <CustomIconButton
                      onClick={() => {
                        router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#events/node-errors`);
                      }}
                    >
                      <SafeIcon src={ExternalLinkIcon} alt='redirect' />
                    </CustomIconButton>
                  }
                />
                <ShimmerLoading isLoading={loadingData.nodeErrorTableDataLoading} width='95%'>
                  <CustomTable
                    tableData={nodeErrorTableData}
                    headers={[{ name: 'Node name', width: '60%' }, 'Count', 'Last occurred']}
                    showUpdatedTable
                    showEmptyStateText
                    rowsPerPage={nodeErrorTableData.length}
                    totalRows={nodeErrorTableData.length}
                  />
                </ShimmerLoading>
              </Box>
            </Box>
          </SummaryBlock>
        </Grid>
      </Grid>
    </>
  );
}

import { Box, Grid, Typography } from '@mui/material';
import React, { useEffect, useState } from 'react';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import TextWithBorder from '@components1/common/TextWithBorder';
import { Button as DsButton } from '@components1/ds/Button';
import { addIcon, ExternalLinkIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import Datetime from '@components1/common/format/Datetime';
import Text from '@components1/common/format/Text';
import CustomTable from '@common-new/tables/CustomTable2';
import { useRouter } from 'next/router';
import k8sApi from '@api1/kubernetes';
import apiWorkflow from '@api1/workflow';
import { hasWriteAccess } from '@lib/auth';
import { getLast24Hrs } from '@lib/datetime';
import { Link } from '@components1/ds/Link';
import { Skeleton } from '@components1/ds/Skeleton';
import { Divider } from '@components1/ds/Divider';
import { colors, ds } from 'src/utils/colors';
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
        color: ds.background[100],
        background: ds.red[500],
      },
      {
        value: 0,
        label: 'Medium',
        color: ds.red[500],
        background: ds.red[100],
      },
      {
        value: 0,
        label: 'Low',
        color: ds.yellow[700],
        background: ds.yellow[100],
      },
      {
        value: 0,
        label: 'Debug',
        color: ds.blue[500],
        background: ds.blue[100],
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
                      <Datetime value={item.max_created_at} sx={{ fontSize: 'var(--ds-text-small)', pl: 'var(--ds-space-1)', textAlign: 'right' }} />
                    </Box>
                  </Box>
                ),
              },
              {
                component: (
                  <Typography textAlign={'end'}>
                    <Link
                      href={`/kubernetes/details/${accountId}?eventAggregationKey=${item.aggregation_key}&eventPriority=HIGH#events/all-events`}
                      style={{ color: ds.blue[500], fontSize: 'var(--ds-text-small)' }}
                    >
                      {item?.event_count}
                    </Link>
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
                    <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)' }}>
                      <Box display={'flex'} alignItems={'center'}>
                        <Text value={'Last occ:'} secondaryText />
                        <Datetime
                          value={item.max_created_at}
                          sx={{ fontSize: 'var(--ds-text-small)', pl: 'var(--ds-space-1)', textAlign: 'right' }}
                        />
                      </Box>
                      <Box display={'flex'} alignItems={'center'}>
                        <Text value={'ns: '} secondaryText />
                        <Text value={item.subject_namespace} showAutoEllipsis sx={{ fontSize: 'var(--ds-text-small)' }} />
                      </Box>
                    </Box>
                  </Box>
                ),
              },
              {
                component: (
                  <Typography textAlign={'end'}>
                    <Link
                      href={`/kubernetes/details/${accountId}?eventAggregationKey=HighErrorCriticalLogs,ApplicationAPIFailures&eventNamespace=${item.subject_namespace}&eventSubjectName=${item.subject_owner}&exact=true#events/all-events`}
                      style={{ color: ds.blue[500], fontSize: 'var(--ds-text-small)' }}
                    >
                      {item?.event_count}
                    </Link>
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
                    <Link href={`/kubernetes/details/${accountId}#events/all-events`} style={{ color: ds.gray[700] }}>
                      <Datetime value={item.starts_at} sx={{ pl: 'var(--ds-space-1)', textAlign: 'right' }} />
                    </Link>
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
                  <Link
                    href={`/kubernetes/details/${accountId}?eventSubjectName=${data.subject_name}&eventSubjectType=node#events/all-events`}
                    style={{ color: ds.blue[500], fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}
                  >
                    {data?.event_count}
                  </Link>
                ),
              },
              {
                component: (
                  <Box display={'flex'} justifyContent={'flex-end'}>
                    <Datetime
                      value={data.max_created_at}
                      sx={{ pl: 'var(--ds-space-1)', textAlign: 'right' }}
                      sxSuffix={{ fontSize: 'var(--ds-text-caption)' }}
                    />
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
                  <Link
                    href={`/kubernetes/details/${accountId}?eventTitle=${data.title}&eventAggregationKey=ApplicationAPIFailures#events/all-events`}
                    style={{ color: ds.blue[500], fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}
                  >
                    {data?.event_count}
                  </Link>
                ),
              },
              {
                component: (
                  <Box display={'flex'} justifyContent={'flex-end'}>
                    <Datetime
                      value={data.max_created_at}
                      sx={{ pl: 'var(--ds-space-1)', textAlign: 'right' }}
                      sxSuffix={{ fontWeight: 'var(--ds-font-weight-regular)' }}
                    />
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
                    <Datetime value={item.starts_at} sx={{ pl: 'var(--ds-space-1)', textAlign: 'right' }} />
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
          totalCount: totalResponse?.data?.workflows_count?.count ?? 0,
          configuredCount: configuredResponse?.data?.workflows_count?.count ?? 0,
          actionedCount: actionedResponse?.data?.workflows_count_executions?.count ?? 0,
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
          gap: ds.space[2],
          mt: 'var(--ds-space-1)',
        }}
      >
        <Box sx={{ gridColumn: 'span 8 ' }}>
          <SummaryBlock
            hideTitle
            height='100%'
            sx={{
              height: '100%',
              borderColor: 'transparent',
              backgroundColor: ds.background[100],
              boxShadow: colors.shadow.softGray,
              '@media(max-width: 1170px)': {
                padding: 'var(--ds-space-4) !important',
              },
            }}
          >
            <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
              <Box display='flex' alignItems={'center'}>
                <TextWithBorder
                  value='Last 24hrs'
                  borderColor={ds.blue[500]}
                  borderWidth='3px'
                  sx={{ '& p': { fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] } }}
                />
              </Box>
              <Box
                display={'flex'}
                gap={ds.space.mul(0, 5)}
                sx={{
                  '@media(max-width: 1130px)': {
                    gap: 'var(--ds-space-1)',
                  },
                }}
              >
                {loadingData.eventTotalCountLoading ? (
                  <Skeleton width={ds.space.mul(0, 215)} height={ds.space.mul(0, 5)} />
                ) : (
                  <>
                    {eventSummaryData.severityData.map((data, index) => (
                      <Box
                        display='flex'
                        alignItems={'center'}
                        key={data.label}
                        sx={{
                          '&::after': index !== eventSummaryData.severityData.length - 1 && {
                            content: '" "',
                            height: ds.space[4],
                            border: `0.5px solid ${ds.gray[300]}`,
                          },
                        }}
                      >
                        <Typography
                          sx={{
                            backgroundColor: data.background,
                            p: 'var(--ds-space-1) var(--ds-space-2)',
                            borderRadius: 'var(--ds-radius-sm)',
                            color: data.color,
                            boxShadow: colors.shadow.softBlack,
                            fontWeight: 'var(--ds-font-weight-semibold)',
                            fontSize: 'var(--ds-text-caption)',
                            mr: 'var(--ds-space-1)',
                          }}
                        >
                          {data.value || 0}
                        </Typography>
                        <Typography
                          sx={{
                            fontSize: 'var(--ds-text-small)',
                            fontWeight: 'var(--ds-font-weight-regular)',
                            color: ds.gray[600],
                            mr: 'var(--ds-space-2)',
                          }}
                        >
                          {data.label}
                        </Typography>
                      </Box>
                    ))}
                  </>
                )}
              </Box>
            </Box>

            <Box display={'grid'} gridTemplateColumns={'1fr 1fr 1fr'} gap={ds.space[3]} mt={ds.space[3]}>
              <Box>
                <TextWithBorder
                  value='By Event type'
                  borderColor={ds.yellow[500]}
                  borderWidth='2px'
                  sx={{
                    '& p': { fontSize: 'var(--ds-text-body) !important', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] },
                  }}
                  span={
                    <DsButton
                      tone='ghost'
                      composition='icon-only'
                      aria-label='Open in details'
                      icon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />}
                      onClick={() => {
                        router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#events/grouped-events`);
                      }}
                    />
                  }
                />
                {loadingData.eventTypeDataLoading ? (
                  <Skeleton width='93%' />
                ) : (
                  <>
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
                  </>
                )}
              </Box>
              <Box>
                <TextWithBorder
                  value='By Applications'
                  borderColor={ds.yellow[500]}
                  borderWidth='2px'
                  sx={{
                    '& p': { fontSize: 'var(--ds-text-body) !important', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] },
                  }}
                  span={
                    <DsButton
                      tone='ghost'
                      composition='icon-only'
                      aria-label='Open in details'
                      icon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />}
                      onClick={() => {
                        router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#events/grouped-events`);
                      }}
                    />
                  }
                />
                {loadingData.applicationEventDataLoading ? (
                  <Skeleton width='93%' />
                ) : (
                  <>
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
                  </>
                )}
              </Box>{' '}
              <Box>
                <TextWithBorder
                  value='Most Recent'
                  borderColor={ds.yellow[500]}
                  borderWidth='2px'
                  sx={{
                    '& p': { fontSize: 'var(--ds-text-body) !important', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] },
                  }}
                  span={
                    <DsButton
                      tone='ghost'
                      composition='icon-only'
                      aria-label='Open in details'
                      icon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />}
                      onClick={() => {
                        router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#events/all-events`);
                      }}
                    />
                  }
                />
                {loadingData.eventRecentDataLoading ? (
                  <Skeleton width='93%' />
                ) : (
                  <>
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
                  </>
                )}
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
              backgroundColor: ds.background[100],
              boxShadow: colors.shadow.softGray,
              minHeight: ds.space.mul(0, 215),
              '@media(max-width: 1170px)': {
                padding: 'var(--ds-space-4) !important',
              },
              '@media(max-width: 1330px)': {
                minHeight: ds.space.mul(0, 215),
              },
            }}
          >
            <TextWithBorder
              value='Automations'
              borderColor={ds.blue[500]}
              borderWidth='3px'
              sx={{
                '& p': {
                  fontSize: 'var(--ds-text-title)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  color: ds.gray[700],
                  '@media(max-width: 1350px)': {
                    fontSize: 'var(--ds-text-title) !important',
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
                <Box mt={ds.space[5]}>
                  <Typography sx={{ color: ds.gray[400], fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}>
                    Automations
                  </Typography>
                  {loadingData.workflowDataLoading ? (
                    <Skeleton width={ds.space.mul(0, 35)} height={ds.space.mul(0, 5)} />
                  ) : (
                    <>
                      <Typography sx={{ color: ds.gray[700], fontSize: 'var(--ds-text-display)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                        {workflowData.totalCount || '-'}
                      </Typography>
                    </>
                  )}
                </Box>
                <Divider sx={{ my: 'var(--ds-space-4)', color: ds.gray[200] }} />
                <Box mt={ds.space[5]}>
                  <Typography sx={{ color: ds.gray[400], fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}>
                    Event Automations Configured
                  </Typography>
                  {loadingData.workflowDataLoading ? (
                    <Skeleton width={ds.space.mul(0, 35)} height={ds.space.mul(0, 5)} />
                  ) : (
                    <>
                      <Typography sx={{ color: ds.gray[700], fontSize: 'var(--ds-text-display)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                        {workflowData.configuredCount}
                      </Typography>
                    </>
                  )}
                </Box>
                <Divider sx={{ my: 'var(--ds-space-4)', color: ds.gray[200] }} />
                <Box mt={ds.space[5]}>
                  <Typography sx={{ color: ds.gray[400], fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}>
                    Event Automations Triggered in Last 24 Hours
                  </Typography>
                  {loadingData.workflowDataLoading ? (
                    <Skeleton width={ds.space.mul(0, 35)} height={ds.space.mul(0, 5)} />
                  ) : (
                    <>
                      <Typography sx={{ color: ds.gray[700], fontSize: 'var(--ds-text-display)', fontWeight: 'var(--ds-font-weight-regular)' }}>
                        {workflowData.actionedCount}
                      </Typography>
                    </>
                  )}
                </Box>
              </Box>
              <Box sx={{ mt: 'auto' }}>
                <Box
                  display={'flex'}
                  gap={ds.space[2]}
                  mt='auto'
                  justifyContent={'center'}
                  sx={{
                    '& > *': { flex: 1 },
                    '& button': { whiteSpace: 'nowrap' },
                    '@media(max-width: 1330px)': {
                      gap: 'var(--ds-space-1)',
                      '& button': {
                        padding: '0px var(--ds-space-2)',
                        whiteSpace: 'nowrap',
                      },
                    },
                    '@media(max-width: 1030px)': {
                      flexDirection: 'column',
                      mt: 'var(--ds-space-2)',
                      alignItems: 'center',
                    },
                  }}
                >
                  {hasWriteAccess(accountId) ? (
                    <DsButton
                      tone='secondary'
                      size='xs'
                      icon={<SafeIcon src={addIcon} alt='add' />}
                      onClick={() => {
                        router.push(`/workflow/new?accountId=${accountId}`);
                      }}
                    >
                      Add new
                    </DsButton>
                  ) : (
                    <></>
                  )}
                  <DsButton
                    tone='secondary'
                    size='xs'
                    onClick={() => {
                      router.push(`/auto-pilot?accountId=${accountId}`);
                    }}
                  >
                    View all
                  </DsButton>
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
              backgroundColor: ds.background[100],
              boxShadow: colors.shadow.softGray,
              '@media(max-width: 1170px)': {
                padding: 'var(--ds-space-4) !important',
              },
            }}
          >
            <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
              <Box display='flex' alignItems={'center'}>
                <TextWithBorder
                  value='Application Errors'
                  borderColor={ds.blue[500]}
                  borderWidth='3px'
                  sx={{ '& p': { fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] } }}
                />
                <Typography
                  sx={{
                    border: `0.5px solid ${ds.red[500]}`,
                    backgroundColor: ds.red[100],
                    p: 'var(--ds-space-1) var(--ds-space-1)',
                    borderRadius: 'var(--ds-radius-sm)',
                    color: ds.gray[700],
                    fontWeight: 'var(--ds-font-weight-medium)',
                    fontSize: 'var(--ds-text-small)',
                  }}
                >
                  {eventSummaryData.applicationEvents}
                </Typography>
              </Box>
            </Box>

            <Box display={'grid'} gridTemplateColumns={'1fr 1fr'} gap={ds.space[3]} mt={ds.space[3]}>
              <Box>
                <TextWithBorder
                  value='API Errors - By Count'
                  borderColor={ds.yellow[500]}
                  borderWidth='2px'
                  sx={{
                    '& p': { fontSize: 'var(--ds-text-body) !important', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] },
                  }}
                  span={
                    <DsButton
                      tone='ghost'
                      composition='icon-only'
                      aria-label='Open in details'
                      icon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />}
                      onClick={() => {
                        router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#events/app-errors`);
                      }}
                    />
                  }
                />
                {loadingData.apiErrorsByCountLoading ? (
                  <Skeleton width='93%' />
                ) : (
                  <>
                    <CustomTable
                      tableData={apiErrorsByCount}
                      headers={[{ name: 'API', width: '70%' }, { name: 'Count' }, { name: 'Last occurred' }]}
                      showUpdatedTable
                      showEmptyStateText
                      rowsPerPage={apiErrorsByCount.length}
                      totalRows={apiErrorsByCount.length}
                    />
                  </>
                )}
              </Box>
              <Box>
                <TextWithBorder
                  value='API Errors - Most Recent'
                  borderColor={ds.yellow[500]}
                  borderWidth='2px'
                  sx={{
                    '& p': { fontSize: 'var(--ds-text-body) !important', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] },
                  }}
                  span={
                    <DsButton
                      tone='ghost'
                      composition='icon-only'
                      aria-label='Open in details'
                      icon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />}
                      onClick={() => {
                        router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#events/app-errors`);
                      }}
                    />
                  }
                />
                {loadingData.apiErrorsRecentLoading ? (
                  <Skeleton width='93%' />
                ) : (
                  <>
                    <CustomTable
                      tableData={apiErrorsRecent}
                      headers={[{ name: 'API', width: '70%' }, 'Last occurred']}
                      showUpdatedTable
                      showEmptyStateText
                      rowsPerPage={apiErrorsRecent.length}
                      totalRows={apiErrorsRecent.length}
                    />
                  </>
                )}
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
              backgroundColor: ds.background[100],
              boxShadow: colors.shadow.softGray,
              '@media(max-width: 1170px)': {
                padding: 'var(--ds-space-4) !important',
              },
            }}
          >
            <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
              <Box display='flex' alignItems={'center'}>
                <TextWithBorder
                  value='Node Errors'
                  borderColor={ds.blue[500]}
                  borderWidth='3px'
                  sx={{ '& p': { fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] } }}
                />
                <Typography
                  sx={{
                    border: `0.5px solid ${ds.red[500]}`,
                    backgroundColor: ds.red[100],
                    p: 'var(--ds-space-1) var(--ds-space-1)',
                    borderRadius: 'var(--ds-radius-sm)',
                    color: ds.gray[700],
                    fontWeight: 'var(--ds-font-weight-medium)',
                    fontSize: 'var(--ds-text-small)',
                  }}
                >
                  {eventSummaryData.nodeEvents}
                </Typography>
              </Box>
            </Box>

            <Box display={'grid'} gridTemplateColumns={'1fr'} gap={ds.space[3]} mt={ds.space[3]}>
              <Box>
                <TextWithBorder
                  value='By Node'
                  borderColor={ds.yellow[500]}
                  borderWidth='2px'
                  sx={{
                    '& p': { fontSize: 'var(--ds-text-body) !important', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] },
                  }}
                  span={
                    <DsButton
                      tone='ghost'
                      composition='icon-only'
                      aria-label='Open in details'
                      icon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />}
                      onClick={() => {
                        router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#events/node-errors`);
                      }}
                    />
                  }
                />
                {loadingData.nodeErrorTableDataLoading ? (
                  <Skeleton width='95%' />
                ) : (
                  <>
                    <CustomTable
                      tableData={nodeErrorTableData}
                      headers={[{ name: 'Node name', width: '60%' }, 'Count', 'Last occurred']}
                      showUpdatedTable
                      showEmptyStateText
                      rowsPerPage={nodeErrorTableData.length}
                      totalRows={nodeErrorTableData.length}
                    />
                  </>
                )}
              </Box>
            </Box>
          </SummaryBlock>
        </Grid>
      </Grid>
    </>
  );
}

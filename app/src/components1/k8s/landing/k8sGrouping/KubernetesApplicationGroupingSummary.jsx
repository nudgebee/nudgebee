import PropTypes from 'prop-types';
import { ExternalLinkIcon } from '@assets';
import Text from '@common-new/format/Text';
import Currency from '@common-new/format/Currency';
import Datetime from '@common-new/format/Datetime';
import CustomTable from '@common-new/tables/CustomTable2';
import TextWithBorder from '@components1/common/TextWithBorder';
import { Button as DsButton } from '@components1/ds/Button';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import { Box, Typography } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import { useRouter } from 'next/router';
import React, { useEffect, useState, useRef } from 'react';
import k8sApi from '@api1/kubernetes';
import { Link } from '@components1/ds/Link';
import apiKubernetes1 from '@api1/kubernetes1';
import apiAppGrouping from '@api1/application-groupings';
import { formatDateForTrace } from 'src/utils/common';
import { getLast30Days, getSpecificTime } from '@lib/datetime';
import { Skeleton } from '@components1/ds/Skeleton';
import KubernetesApplicationGroupingSummaryDashboard from './KubernetesApplicationGroupingSummaryDashboard';
import { ds } from '@utils/colors';
import apiTrace from '@api1/kubernetes/trace';
import Tooltip from '@components1/ds/Tooltip';

const KubernetesApplicationGroupingSummary = ({ accountId, applications, setTab, setRenderForApplicationIssue }) => {
  const router = useRouter();
  const endDate = new Date();
  const startDate = new Date(endDate.getTime() - 24 * 60 * 60 * 1000);

  const dateRange = { startDate, endDate };

  // Separate loading states for each API call
  const [loadingStates, setLoadingStates] = useState({
    workloadKind: false,
    clusterSummary: false,
    eventSummary: false,
    eventType: false,
    applicationEvent: false,
    traceGroup: false,
  });
  const resourceIds = React.useMemo(() => applications?.map((item) => item?.cloud_resource_id) || [], [applications]);
  const [groupName, setGroupName] = useState('');
  const [eventTypeData, setEventTypeData] = useState([]);
  const [applicationEventData, setApplicationEventData] = useState([]);
  const [traceGroupData, setTraceGroupData] = useState([]);
  const [clusterSummary, setClusterSummary] = useState();
  const [eventSummaryData, setEventSummaryData] = useState({
    severityData: [
      {
        value: 0,
        label: 'High',
        color: 'var(--ds-background-100)',
        background: 'var(--ds-red-500)',
      },
      {
        value: 0,
        label: 'Med',
        color: 'var(--ds-red-500)',
        background: 'var(--ds-red-100)',
      },
      {
        value: 0,
        label: 'Low',
        color: 'var(--ds-yellow-700)',
        background: 'var(--ds-yellow-100)',
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
  });
  const [workloadKindCounts, setWorkloadKindCounts] = useState([]);
  const [sloData, setSLOData] = useState({ count: 0, firingCount: 0, firingWorkloads: [] });
  const eventTypeApplicationTypeTraceGroupRef = useRef(null);

  // Helper function to update loading state
  const updateLoadingState = (key, value) => {
    setLoadingStates((prev) => ({ ...prev, [key]: value }));
  };

  // Helper functions moved outside of useEffect
  const formatWorkloadName = (key) => {
    return key.replace('_count', '').replace(/\b\w/g, (char) => char.toUpperCase());
  };

  const formatWorkloadKinds = (workloadCounts) => {
    return Object.entries(workloadCounts)
      .filter(([_key, value]) => value > 0)
      .reduce((result, [key, value]) => {
        result[formatWorkloadName(key)] = value;
        return result;
      }, {});
  };

  const createEventTableData = (items, isAggregation = false) => {
    return (
      items?.map((item) => {
        const keyField = isAggregation ? item.aggregation_key : item.subject_owner;
        const linkParams = isAggregation ? `eventAggregationKey=${item.aggregation_key}&eventPriority=HIGH` : '';

        return [
          {
            component: (
              <Box>
                <Text value={keyField} showAutoEllipsis />
                <Box display={'flex'} alignItems={'center'}>
                  <Text secondaryText value={'Last occ:'} />
                  <Datetime
                    value={item.max_created_at}
                    sx={{ fontSize: 'var(--ds-text-caption)', pl: 'var(--ds-space-1)', textAlign: 'right' }}
                    sxSuffix={{ fontSize: 'var(--ds-text-caption)' }}
                  />
                </Box>
              </Box>
            ),
          },
          {
            component: (
              <Typography textAlign={'end'}>
                <Link
                  href={`/kubernetes/details/${accountId}?${linkParams}#events/all-events`}
                  style={{ color: 'var(--ds-brand-500)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}
                >
                  {item?.event_count}
                </Link>
              </Typography>
            ),
          },
        ];
      }) || []
    );
  };

  const createTraceGroupData = (items) => {
    return (
      items?.map((item) => [
        {
          component: (
            <Box>
              <Text value={item.count || '-'} showAutoEllipsis />
              <Text secondaryText value={`Error Count: ${item.error_count} `} showAutoEllipsis />
            </Box>
          ),
        },
        {
          component: (
            <Box>
              <Text value={item.destination_workload_name || '-'} showAutoEllipsis />

              <Box display='flex' justifyContent='space-between'>
                <Text secondaryText value={`Namespace: ${item.destination_workload_namespace}`} showAutoEllipsis />
                <Text secondaryText value={`Status: ${item.http_status_code}`} showAutoEllipsis />
              </Box>

              <Box display='flex' justifyContent='space-between'>
                <Text secondaryText value={`Resource: ${item.resource}`} showAutoEllipsis />
                <Text secondaryText value={`Method: ${item.span_name}`} showAutoEllipsis />
              </Box>
            </Box>
          ),
        },
      ]) || []
    );
  };

  // Individual API call functions that handle their own loading states
  const fetchWorkloadKindCount = async (accountId, resource_ids) => {
    updateLoadingState('workloadKind', true);
    try {
      const response = await apiKubernetes1.listK8sWorkloadKindCount(accountId, '', resource_ids);
      const data = response?.data?.data?.workload_counts?.rows[0] ?? {};
      if (data) {
        const workloadKindsArray = formatWorkloadKinds(data);
        setWorkloadKindCounts(workloadKindsArray);
      }
    } catch (error) {
      console.error('Error fetching workload kind count:', error);
    } finally {
      updateLoadingState('workloadKind', false);
    }
  };

  const fetchClusterSummary = async (accountId, resource_ids) => {
    updateLoadingState('clusterSummary', true);
    try {
      const response = await apiAppGrouping.getK8sClusterSummaryData(accountId, { resource_ids: resource_ids });
      setClusterSummary(response?.data);
    } catch (error) {
      console.error('Error fetching cluster summary:', error);
    } finally {
      updateLoadingState('clusterSummary', false);
    }
  };

  const fetchEventSummary = async (accountId, resource_ids) => {
    updateLoadingState('eventSummary', true);
    try {
      const response = await k8sApi.getK8sEventGroupings(
        10,
        0,
        {
          account_id: accountId,
          start_date: dateRange.startDate,
          end_date: dateRange.endDate,
          resource_ids: resource_ids,
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
      );

      const firstRow = response.data?.event_groupings?.[0];
      if (firstRow) {
        setEventSummaryData((prev) => ({
          ...prev,
          nodeEvents: firstRow.count_node_issues,
          highEvents: firstRow.count_priority_high,
          applicationEvents: firstRow.count_application_issues,
          podEvents: firstRow.count_pod_issues,
          severityData: [
            { ...prev.severityData[0], value: firstRow.count_priority_high },
            { ...prev.severityData[1], value: firstRow.count_priority_medium },
            { ...prev.severityData[2], value: firstRow.count_priority_low },
            { ...prev.severityData[3], value: firstRow.count_priority_debug },
          ],
        }));
      }
    } catch (error) {
      console.error('Error fetching event summary:', error);
    } finally {
      updateLoadingState('eventSummary', false);
    }
  };

  const fetchEventTypeData = async (accountId, resource_ids) => {
    updateLoadingState('eventType', true);
    try {
      const response = await k8sApi.getK8sEventGroupings(
        5,
        0,
        {
          account_id: accountId,
          start_date: dateRange.startDate,
          end_date: dateRange.endDate,
          priority: 'HIGH',
          resource_ids: resource_ids,
        },
        ['tenant_id', 'account_id', 'aggregation_key'],
        ['max_created_at', 'event_count', 'aggregation_key'],
        { name: 'event_count', order: 'desc' }
      );

      const tableData = createEventTableData(response?.data?.event_groupings, true);
      setEventTypeData(tableData);
    } catch (error) {
      console.error('Error fetching event type data:', error);
    } finally {
      updateLoadingState('eventType', false);
    }
  };

  const fetchApplicationEventData = async (accountId, resource_ids) => {
    updateLoadingState('applicationEvent', true);
    try {
      const response = await k8sApi.getK8sEventGroupings(
        5,
        0,
        {
          account_id: accountId,
          start_date: dateRange.startDate,
          end_date: dateRange.endDate,
          aggregation_key: [],
          priority: 'HIGH',
          resource_ids: resource_ids,
        },
        ['tenant_id', 'account_id', 'subject_owner'],
        ['max_created_at', 'event_count', 'subject_owner'],
        { name: 'event_count', order: 'desc' }
      );

      const tableData = createEventTableData(response?.data?.event_groupings, false);
      setApplicationEventData(tableData);
    } catch (error) {
      console.error('Error fetching application event data:', error);
    } finally {
      updateLoadingState('applicationEvent', false);
    }
  };

  const fetchTraceGroup = async (accountId, namespaceNames, workloadNames) => {
    updateLoadingState('traceGroup', true);
    try {
      const response = await apiTrace.traceGroupV2(
        accountId,
        '',
        '',
        namespaceNames,
        workloadNames,
        '',
        5,
        0,
        formatDateForTrace(getSpecificTime(60)),
        formatDateForTrace(new Date().getTime()),
        '',
        '',
        ''
      );

      const tableData = createTraceGroupData(response?.traces_groupings?.rows || []);
      setTraceGroupData(tableData);
    } catch (error) {
      console.error('Error fetching recent events:', error);
    } finally {
      updateLoadingState('traceGroup', false);
    }
  };

  const fetchSLOObsersavation = async (accountId, namespaces, workloads, timestamp) => {
    try {
      const response = await apiKubernetes1.getSLOObservation({ accountId, namespaces, workloads, timestamp });
      const sloResponseData = response?.data?.data?.slo_report_observation_v2?.rows || [];
      if (sloResponseData.length > 0) {
        const statusMap = {};
        sloResponseData.forEach((item) => {
          const key = `${item.workload_namespace}/${item.workload_name}`;
          if (!statusMap[key]) {
            statusMap[key] = item.status;
          } else if (item.status === 'FIRING') {
            statusMap[key] = 'FIRING';
          }
        });
        const firingCount = Object.values(statusMap).filter((status) => status === 'FIRING').length;
        const distinctCount = Object.keys(statusMap).length;
        const firingArray = Object.entries(statusMap)
          .filter(([_, _status]) => status === 'FIRING')
          .map(([key, _status]) => {
            const [workload_namespace, workload_name] = key.split('/');
            return { workload_namespace, workload_name };
          });
        setSLOData({
          count: distinctCount,
          firingCount,
          firingWorkloads: firingArray.length > 0 ? firingArray.map((f) => `${f.workload_namespace}/${f.workload_name}`) : [],
        });
      }
    } catch (error) {
      console.error('Error fetching SLO observations:', error);
    }
  };

  // Main effect for all data fetching with parallel execution
  useEffect(() => {
    if (!accountId) {
      return;
    }

    const resource_ids = applications?.map((item) => item?.cloud_resource_id) || [];
    if (!resource_ids.length) {
      return;
    }

    const workloadNames = [...new Set(applications?.map((item) => item.workload_name))];
    const namespaceNames = [...new Set(applications?.map((item) => item.namespace_name))];

    // Execute all API calls in parallel without waiting for each other
    // Each function handles its own loading state and renders independently
    fetchWorkloadKindCount(accountId, resource_ids);
    fetchClusterSummary(accountId, resource_ids);
    fetchEventSummary(accountId, resource_ids);
    fetchSLOObsersavation(accountId, namespaceNames, workloadNames, getLast30Days(new Date()).toISOString());
  }, [accountId, applications]);

  // Lazy load eventTypeData, applicationEventData, and traceGroupData when the user scrolls to the section
  useEffect(() => {
    if (!accountId || applications.length === 0) {
      return;
    }

    const observer = new IntersectionObserver(
      (entries) => {
        if (entries[0].isIntersecting) {
          const resource_ids = applications?.map((item) => item?.cloud_resource_id) || [];
          if (!resource_ids.length) {
            return;
          }

          const workloadNames = [...new Set(applications?.map((item) => item.workload_name))];
          const namespaceNames = [...new Set(applications?.map((item) => item.namespace_name))];
          fetchEventTypeData(accountId, resource_ids);
          fetchApplicationEventData(accountId, resource_ids);
          fetchTraceGroup(accountId, namespaceNames, workloadNames);
          observer.disconnect();
        }
      },
      { threshold: 0.1 }
    );

    if (eventTypeApplicationTypeTraceGroupRef.current) {
      observer.observe(eventTypeApplicationTypeTraceGroupRef.current);
    }

    return () => observer.disconnect();
  }, [accountId, applications]);

  useEffect(() => {
    if (!router?.query?.groupId) {
      return;
    }
    apiAppGrouping.getAppGroupByPK(router?.query?.groupId).then((res) => {
      setGroupName(res?.data?.data?.application_group_by_pk?.name || '');
    });
  }, [router?.query?.groupId]);
  // Helper function to check if a specific section is loading
  const isSectionLoading = (sections) => {
    return sections.some((section) => loadingStates[section]);
  };

  if (!accountId || !applications || applications.length === 0) {
    return (
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'center',
          alignItems: 'center',
          minHeight: ds.space.mul(0, 100),
          backgroundColor: 'var(--ds-background-100)',
          borderRadius: 'var(--ds-radius-lg)',
          boxShadow: '0px 4px 20px 0px #B4B4B41F',
          margin: 'var(--ds-space-4) 0',
        }}
      >
        <Typography
          sx={{
            fontSize: 'var(--ds-text-title)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-gray-600)',
            textAlign: 'center',
          }}
        >
          No data available. Please configure application.
        </Typography>
      </Box>
    );
  }

  return (
    <Box>
      <SummaryBlock
        hideTitle
        sx={{
          borderColor: 'transparent',
          backgroundColor: 'var(--ds-background-100)',
          boxShadow: '0px 4px 20px 0px #B4B4B41F',
          padding: 'var(--ds-space-3) var(--ds-space-4) !important',
          minHeight: 'unset',
          mb: 'var(--ds-space-5)',
          mt: 'var(--ds-space-4)',
        }}
      >
        <Box display='flex' alignItems={'center'} justifyContent={'space-between'} mb={ds.space[4]}>
          <TextWithBorder
            value='Application Summary'
            borderColor={ds.blue[500]}
            borderWidth='3px'
            sx={{
              minWidth: 'auto',
              height: ds.space.mul(0, 11),
              padding: 'var(--ds-space-1) var(--ds-space-2)',
              '& p': { fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-500)' },
            }}
          />
        </Box>

        {isSectionLoading(['workloadKind', 'podStatus', 'eventSummary', 'clusterSummary']) ? (
          <Skeleton shape='rect' height={ds.space[7]} />
        ) : (
          <Box display='grid' gridTemplateColumns={{ xs: '1fr', sm: '3fr 2fr 5fr' }} gap={ds.space[4]}>
            {/* Applications */}
            <Box sx={{ p: '0 var(--ds-space-2)', borderRight: '1px solid var(--ds-gray-200)' }}>
              <Typography variant='subtitle2' color='textSecondary'>
                Applications
              </Typography>
              <Box display='flex' alignItems='baseline' gap={ds.space[2]} mt={ds.space[1]}>
                {loadingStates.workloadKind ? (
                  <Skeleton shape='rect' height={ds.space[5]} width={ds.space.mul(0, 30)} />
                ) : (
                  <Typography
                    variant='h5'
                    fontWeight={600}
                    onClick={() => {
                      if (workloadKindCounts?.Count > 0) {
                        setRenderForApplicationIssue(false);
                        setTab(2);
                      }
                    }}
                    sx={{
                      cursor: workloadKindCounts?.Count > 0 ? 'pointer' : 'default',
                      '&:hover': workloadKindCounts?.Count > 0 ? { color: 'var(--ds-blue-500)' } : {},
                    }}
                  >
                    {workloadKindCounts?.Count ?? '-'}
                  </Typography>
                )}
                <Box display='flex' flexWrap='wrap' gap={ds.space[2]} ml={ds.space[2]}>
                  {loadingStates.workloadKind ? (
                    <Skeleton shape='rect' height={ds.space[4]} width={ds.space.mul(0, 50)} />
                  ) : (
                    Object.entries(workloadKindCounts)
                      .filter(([name]) => name !== 'Count')
                      .map(([name, count]) => (
                        <Box key={name} display='flex' alignItems='center' gap={ds.space[1]} mr={ds.space[2]}>
                          <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }} color={ds.gray[400]}>
                            {name}
                          </Typography>
                          <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }} color={ds.gray[700]}>
                            {count}
                          </Typography>
                        </Box>
                      ))
                  )}
                </Box>
              </Box>
            </Box>
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                borderRight: '1px solid var(--ds-gray-200)',
                padding: 'var(--ds-space-4) var(--ds-space-2)',
              }}
            >
              <Box>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-400)' }}>
                  SLO
                </Typography>
                <Tooltip
                  placement='top'
                  title={
                    sloData.firingWorkloads.length > 0 ? (
                      <div>
                        <span style={{ fontWeight: 'bold', marginBottom: ds.space[1] }}>SLO Status for Selected Application</span>
                        <div style={{ fontWeight: 'bold', marginBottom: ds.space[1] }}>Attention: Firing SLO (30 Days Observation)</div>
                        {sloData.firingWorkloads.map((workload, index) => (
                          <div key={index}>{workload}</div>
                        ))}
                        <div style={{ fontWeight: 'bold', marginTop: ds.space[1] }}>{sloData.count} SLO Configured</div>
                      </div>
                    ) : (
                      ''
                    )
                  }
                >
                  <Typography
                    variant='h4'
                    sx={{
                      fontSize: 'var(--ds-text-heading)',
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      color: 'var(--ds-brand-500)',
                      cursor: sloData.count > 0 ? 'pointer' : 'default',
                    }}
                    onClick={() => sloData.count > 0 && router.push(`/kubernetes/details/${accountId}#monitoring/slo`)}
                  >
                    <span style={{ color: sloData.firingCount > 0 ? ds.red[500] : ds.gray[700] }}>{sloData.firingCount}</span> / {sloData.count}
                  </Typography>
                </Tooltip>
              </Box>
            </Box>

            {/* Events & Optimizations */}
            <Box sx={{ p: '0 var(--ds-space-2)', display: 'flex', flexDirection: 'row', justifyContent: 'space-around' }}>
              <Box mb={ds.space[4]}>
                <Typography variant='subtitle2' color='textSecondary'>
                  Events
                </Typography>
                {loadingStates.eventSummary ? (
                  <Skeleton shape='rect' height={ds.space[5]} width={ds.space.mul(0, 30)} />
                ) : (
                  <Tooltip placement='top' title='Application issues (High Error Critical Logs & API Failures)'>
                    <Typography
                      variant='h5'
                      fontWeight={600}
                      onClick={() => {
                        if (eventSummaryData.applicationEvents > 0) {
                          setRenderForApplicationIssue(true);
                          setTab(1);
                        }
                      }}
                    >
                      <Currency
                        prefix=''
                        sx={{
                          fontSize: 'var(--ds-text-heading)',
                          fontWeight: 'var(--ds-font-weight-semibold)',
                          color: 'var(--ds-brand-500)',
                          cursor: eventSummaryData.applicationEvents > 0 ? 'pointer' : 'default',
                          '&:hover': eventSummaryData.applicationEvents > 0 ? { color: 'var(--ds-blue-500)' } : {},
                        }}
                        withTooltip={false}
                        value={eventSummaryData.applicationEvents}
                      />
                    </Typography>
                  </Tooltip>
                )}
              </Box>
              <Box>
                <Typography variant='subtitle2' color='textSecondary'>
                  Optimizations
                </Typography>
                <Box display='flex' alignItems='baseline' gap={ds.space[2]} mt={ds.space[1]}>
                  {loadingStates.clusterSummary ? (
                    <Skeleton shape='rect' height={ds.space[5]} width={ds.space.mul(0, 30)} />
                  ) : clusterSummary?.total_recommendations?.length > 0 ? (
                    <>
                      <Typography variant='h5' fontWeight={600}>
                        {clusterSummary?.total_recommendations.reduce((totalCounts, recommendation) => totalCounts + recommendation.count, 0) ?? '-'}
                      </Typography>
                      <Box display='flex' flexWrap='wrap' gap={ds.space[2]} ml={ds.space[2]}>
                        {clusterSummary?.total_recommendations.map(({ category, count }) => {
                          const hashMap = {
                            'Right Sizing': 'optimize/right-sizing',
                            'Unused Volumes': 'optimize/unused-volume',
                            'Spot Recommendations': 'optimize/spot-recommendation',
                          };
                          const hash = hashMap[category];
                          return (
                            <Box key={category} display='flex' alignItems='center' gap={ds.space[1]} mr={ds.space[2]}>
                              <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }} color={ds.gray[400]}>
                                {category}
                              </Typography>
                              <Typography
                                sx={{
                                  fontSize: 'var(--ds-text-small)',
                                  fontWeight: 'var(--ds-font-weight-regular)',
                                  cursor: count > 0 && hash !== undefined ? 'pointer' : 'default',
                                  '&:hover': count > 0 && hash !== undefined ? { color: 'var(--ds-blue-500)' } : ds.gray[700],
                                }}
                                color={ds.gray[700]}
                                onClick={() => {
                                  if (count > 0 && hash !== undefined) {
                                    router.push({
                                      pathname: `/kubernetes/details/${accountId}`,
                                      query: {
                                        resourceIds,
                                        groupName,
                                      },
                                      hash: hash,
                                    });
                                  }
                                }}
                              >
                                {count}
                              </Typography>
                            </Box>
                          );
                        })}
                      </Box>
                    </>
                  ) : (
                    '-'
                  )}
                </Box>
              </Box>
            </Box>
          </Box>
        )}
      </SummaryBlock>

      <KubernetesApplicationGroupingSummaryDashboard accountId={accountId} applications={applications} />

      <SummaryBlock
        hideTitle
        sx={{
          borderColor: 'transparent',
          backgroundColor: 'var(--ds-background-100)',
          boxShadow: '0px 4px 20px 0px #B4B4B41F',
          '@media(max-width: 1170px)': {
            padding: 'var(--ds-space-4) var(--ds-space-5) !important',
          },
        }}
      >
        <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
          <Box display='flex' alignItems={'center'}>
            <TextWithBorder
              value='Events/Errors'
              borderColor={ds.blue[500]}
              borderWidth='3px'
              sx={{ '& p': { fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-500)' } }}
            />
            {loadingStates.eventSummary ? (
              <Skeleton shape='rect' height={ds.space[5]} width={ds.space[6]} />
            ) : (
              <Typography
                sx={{
                  border: '0.5px solid var(--ds-red-400)',
                  backgroundColor: 'var(--ds-red-100)',
                  p: 'var(--ds-space-1) var(--ds-space-2)',
                  borderRadius: 'var(--ds-radius-sm)',
                  color: 'var(--ds-brand-500)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                }}
              >
                {eventSummaryData.applicationEvents}
              </Typography>
            )}
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
            {loadingStates.eventSummary ? (
              <Skeleton shape='rect' height={ds.space[5]} width={ds.space.mul(0, 100)} />
            ) : (
              eventSummaryData.severityData
                .filter((h) => h.value > 0)
                .map((data, index, filteredSeverityData) => (
                  <Box
                    display='flex'
                    alignItems={'center'}
                    key={data.label}
                    sx={{
                      '&::after': index !== filteredSeverityData.length - 1 && {
                        content: '" "',
                        height: ds.space[4],
                        border: '0.5px solid var(--ds-brand-200)',
                      },
                    }}
                  >
                    <Typography
                      sx={{
                        minWidth: 'auto',
                        height: ds.space.mul(0, 11),
                        padding: 'var(--ds-space-1) var(--ds-space-2)',
                        backgroundColor: data.background,
                        borderRadius: 'var(--ds-radius-sm)',
                        color: data.color,
                        boxShadow: '0px 1px 3px 0px #0000001A',
                        fontWeight: 'var(--ds-font-weight-semibold)',
                        fontSize: 'var(--ds-text-caption)',
                        mr: 'var(--ds-space-1)',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                      }}
                    >
                      {data.value}
                    </Typography>
                    <Typography
                      sx={{
                        fontSize: 'var(--ds-text-body)',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        color: 'var(--ds-brand-500)',
                        mr: 'var(--ds-space-1)',
                      }}
                    >
                      {data.label}
                    </Typography>
                  </Box>
                ))
            )}
          </Box>
        </Box>

        <Box display={'grid'} gridTemplateColumns={'1.2fr 1.2fr 1.7fr'} gap={ds.space[2]} mt={ds.space.mul(0, 5)}>
          <Box ref={eventTypeApplicationTypeTraceGroupRef}>
            <TextWithBorder
              value='By Event type'
              borderColor={ds.yellow[500]}
              borderWidth='2px'
              sx={{ '& p': { fontSize: 'var(--ds-text-body) !important', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-brand-500)' } }}
              span={
                <DsButton
                  tone='ghost'
                  composition='icon-only'
                  aria-label='Open in details'
                  icon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />}
                  onClick={() => {
                    router.push(`/kubernetes/details/${accountId}?accountId=${accountId}&section=0#events/grouped-events`);
                  }}
                />
              }
            />
            <CustomTable
              tableData={eventTypeData}
              headers={[
                { name: 'Event type', width: '80%' },
                { name: 'Count', width: '20%' },
              ]}
              showUpdatedTable
              showEmptyStateText
              loading={loadingStates.eventType}
            />
          </Box>
          <Box ref={eventTypeApplicationTypeTraceGroupRef}>
            <TextWithBorder
              value='By Applications'
              borderColor={ds.yellow[500]}
              borderWidth='2px'
              sx={{ '& p': { fontSize: 'var(--ds-text-body) !important', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-brand-500)' } }}
              span={
                <DsButton
                  tone='ghost'
                  composition='icon-only'
                  aria-label='Open in details'
                  icon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />}
                  onClick={() => {
                    router.push(`/kubernetes/details/${accountId}?accountId=${accountId}&section=1#events/grouped-events`);
                  }}
                />
              }
            />
            <CustomTable
              tableData={applicationEventData}
              headers={[
                { name: 'Application name', width: '80%' },
                { name: 'Count', width: '20%' },
              ]}
              showUpdatedTable
              showEmptyStateText
              loading={loadingStates.applicationEvent}
            />
          </Box>
          <Box ref={eventTypeApplicationTypeTraceGroupRef}>
            <TextWithBorder
              value='Trace Group'
              borderColor={ds.yellow[500]}
              borderWidth='2px'
              sx={{ '& p': { fontSize: 'var(--ds-text-body) !important', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-brand-500)' } }}
              span={
                <DsButton
                  tone='ghost'
                  composition='icon-only'
                  aria-label='Open in details'
                  icon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />}
                  onClick={() => {
                    router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#monitoring/grouping`);
                  }}
                />
              }
            />
            <CustomTable
              tableData={traceGroupData}
              headers={[
                { name: 'Request Count', width: '25%' },
                { name: 'Resource Info', width: '75%' },
              ]}
              showUpdatedTable
              showEmptyStateText
              loading={loadingStates.traceGroup}
            />
          </Box>
        </Box>
      </SummaryBlock>
    </Box>
  );
};

KubernetesApplicationGroupingSummary.propTypes = {
  accountId: PropTypes.string.isRequired,
  applications: PropTypes.array.isRequired,
  setTab: PropTypes.func.isRequired,
  setRenderForApplicationIssue: PropTypes.func.isRequired,
};

export default KubernetesApplicationGroupingSummary;
